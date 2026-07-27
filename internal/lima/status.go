package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// instanceRecord is the subset of `limactl list --json` fields the V1 adapter
// needs — the top-level name and status, verified against real output from an
// installed Lima 2.2.0 (docs/spike-results/evidence/etap-0d-lima-adapter/).
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

// currentInstance returns the record matching InstanceName, or nil if no such
// instance exists (a normal, non-error outcome).
func (a *Adapter) currentInstance(ctx context.Context, op string) (*instanceRecord, error) {
	recs, err := a.listInstances(ctx, op)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if recs[i].Name == InstanceName {
			return &recs[i], nil
		}
	}
	return nil, nil
}
