package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/wzslr321/torio/internal/config"
)

// instanceRecord is the subset of `limactl list --json` fields the V1 adapter
// needs — the top-level name and status, verified against real output from an
// installed Lima 2.2.0. That output is NDJSON: one JSON object per line, not a
// JSON array, which is why Status runs a streaming json.Decoder loop instead of
// unmarshalling a single document (TestStatusMultipleInstancesNDJSON).
// Unknown fields are ignored (this is external tool output the adapter
// consumes, not an authority document it produces — contrast with
// internal/config's DisallowUnknownFields on its own on-disk documents).
// Config is optional for status/start/stop; Init requires it when verifying
// an existing instance against Gate 0 pins.
type instanceRecord struct {
	Name   string          `json:"name"`
	Status string          `json:"status"`
	Config *instanceConfig `json:"config"`
}

// instanceConfig is the nested limactl list config subset used by Init
// compatibility checks (image pin, mounts, forwardAgent, vmType, arch).
type instanceConfig struct {
	VMType string `json:"vmType"`
	Arch   string `json:"arch"`
	Images []struct {
		Location string `json:"location"`
		Digest   string `json:"digest"`
	} `json:"images"`
	Mounts []struct {
		Location string `json:"location"`
	} `json:"mounts"`
	SSH struct {
		ForwardAgent bool `json:"forwardAgent"`
	} `json:"ssh"`
}

// State is the adapter's own instance-state enum, mapped from limactl's
// status strings (verified from `limactl list --json` output and the
// `main.brokenInstance` / status string constants present in the installed
// limactl 2.2.0 binary: Running, Stopped, Broken, Unknown).
type State string

const (
	// StateNotFound means no instance named InstanceName exists yet.
	StateNotFound State = "not_found"
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateBroken   State = "broken"
	// StateUnknownLima mirrors limactl's own "Unknown" status value — a
	// state Lima itself reports, distinct from an unrecognized/parse-failure
	// status string (which fails closed instead, see mapLimaStatus).
	StateUnknownLima State = "unknown"
)

// Status is the structured status of the Torio target VM.
type Status struct {
	State State
	// RawStatus is the verbatim limactl status string ("" for StateNotFound).
	RawStatus string
}

// Status reports the structured status of InstanceName, based on the
// verified `limactl list --json` output. It never treats a bare non-zero
// exit or an unrecognized status string as a known state — those fail
// closed as KindCommandFailed / KindMalformedOutput.
func (a *Adapter) Status(ctx context.Context) (Status, error) {
	rec, err := a.currentInstance(ctx, "status")
	if err != nil {
		return Status{}, err
	}
	if rec == nil {
		return Status{State: StateNotFound}, nil
	}
	st, ok := mapLimaStatus(rec.Status)
	if !ok {
		return Status{}, &Error{Op: "status", Kind: KindMalformedOutput, Err: fmt.Errorf("unrecognized lima status %q", rec.Status)}
	}
	return Status{State: st, RawStatus: rec.Status}, nil
}

func mapLimaStatus(s string) (State, bool) {
	switch s {
	case "Running":
		return StateRunning, true
	case "Stopped":
		return StateStopped, true
	case "Broken":
		return StateBroken, true
	case "Unknown":
		return StateUnknownLima, true
	default:
		return "", false
	}
}

// listInstances runs `limactl list --json` and decodes its newline-separated
// JSON documents (verified: real output is one JSON object per line, not a
// JSON array) using a streaming decoder, which correctly handles any
// whitespace-separated document sequence regardless of the exact separator.
func (a *Adapter) listInstances(ctx context.Context, op string) ([]instanceRecord, error) {
	res, err := a.run(ctx, "list", "--json")
	if err != nil {
		return nil, classifyRunErr(op, err)
	}
	if res.ExitCode != 0 {
		return nil, commandFailed(op, res.ExitCode, res.Stderr)
	}
	// execx bounds retained child output and reports truncation. A truncated
	// stream may have been cut mid-record, so decoding it could yield a
	// silently short instance list (or a "not found" that is really "we never
	// saw the rest"). Never treat a truncated list as ground truth.
	if res.StdoutTruncated {
		return nil, &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("`limactl list --json` output was truncated; refusing to parse a partial instance list")}
	}

	dec := json.NewDecoder(bytes.NewReader(res.Stdout))
	var out []instanceRecord
	for {
		var rec instanceRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return nil, &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("decode `limactl list --json` output: %w", err)}
		}
		// A record missing a required field is semantically incomplete.
		// Skipping it would let a malformed list masquerade as "no VM" (or
		// hide the target instance); fail closed on the whole list instead.
		if rec.Name == "" || rec.Status == "" {
			return nil, &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("`limactl list --json` record missing required name/status field")}
		}
		out = append(out, rec)
	}
	return out, nil
}

// currentInstance returns the record matching the adapter's target instance, or
// nil if no such instance exists (a normal, non-error outcome).
func (a *Adapter) currentInstance(ctx context.Context, op string) (*instanceRecord, error) {
	recs, err := a.listInstances(ctx, op)
	if err != nil {
		return nil, err
	}
	target := a.target()
	for i := range recs {
		if recs[i].Name == target {
			return &recs[i], nil
		}
	}
	return nil, nil
}

// InstanceInfo is one Torio-owned box and the state Lima reports for it.
type InstanceInfo struct {
	Name  string
	State State
	// RawStatus is the verbatim limactl status string.
	RawStatus string
}

// ListTorioInstances returns every Torio-owned box on the host, name-ordered.
//
// Ownership is decided by name, because that is the only thing Lima records
// about a box that Torio chose: the default instance, and every instance whose
// name Torio derived from a backend. Lima carries no label Torio could stamp,
// and inspecting each box to ask what it is would mean entering VMs to answer a
// question about which VMs to enter.
//
// That leaves one gap, and it is the caller's to close: a box TORIO_INSTANCE
// named directly bears no derived name and is invisible here. Callers pass such
// names as extra — the CLI passes the instance this invocation resolved — and
// the documentation states that other directly-named boxes are outside the
// poll. Guessing instead, by treating every listed box as possibly Torio's,
// would report other people's VMs as agents that are not running.
//
// Names arrive from external tool output and are validated as instance names
// before they are returned: an unparseable name reaches an argv and a rendered
// line, and a box whose name Torio could not have created is not Torio's box.
// An unrecognized status string fails the whole call, exactly as it does for a
// single instance — it means Torio's model of limactl is wrong, which is not a
// fact about one box.
func (a *Adapter) ListTorioInstances(ctx context.Context, extra ...string) ([]InstanceInfo, error) {
	const op = "status"
	recs, err := a.listInstances(ctx, op)
	if err != nil {
		return nil, err
	}

	named := make(map[string]bool, len(extra))
	for _, n := range extra {
		if n != "" {
			named[n] = true
		}
	}

	var out []InstanceInfo
	for _, rec := range recs {
		if !config.ValidInstanceName(rec.Name) {
			continue
		}
		derived := rec.Name == config.DefaultInstance || strings.HasPrefix(rec.Name, config.InstancePrefix)
		if !derived && !named[rec.Name] {
			continue
		}
		st, ok := mapLimaStatus(rec.Status)
		if !ok {
			return nil, &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("unrecognized lima status %q", rec.Status)}
		}
		out = append(out, InstanceInfo{Name: rec.Name, State: st, RawStatus: rec.Status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
