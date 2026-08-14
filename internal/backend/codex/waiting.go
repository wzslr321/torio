package codex

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// WaitingMarkerHelper is the fixed guest path the system configuration runs as
// a hook. It is root-owned for the same reason the configuration that names it
// is: the agent cannot silently retune the integration between sessions. The
// marker it writes is agent-owned and remains an operational signal, never a
// boundary or a proof against that same agent.
//
// The path is the one the other process backend uses. A box runs one backend
// (ADR-0009), so the two never meet on a guest, and the digest proven here is
// the selected backend's own embedded script.
const WaitingMarkerHelper = "/usr/local/bin/torio-waiting-marker"

const waitingMarkerPath = Home + "/.torio-waiting.json"

var initialWaitingMarker = []byte(`{"schema_version":"2","waits":[]}` + "\n")

//go:embed templates/torio-waiting-marker.sh
var embeddedWaitingMarker []byte

// reconcileWaitingMarkerHelper proves the hook helper is the one Torio wrote.
//
// It is installed when absent and reported, never rewritten, when it has
// drifted: the same rule the system configuration follows, and for the same
// reason. A box bootstrapped before this existed reports it missing until
// `torio vm bootstrap` runs again, and reports its agent's waiting state as
// unknown in the meantime, which is the honest answer for a box whose hooks were
// never installed.
func reconcileWaitingMarkerHelper(ctx context.Context, r backend.StepRunner) error {
	const name = "codex_waiting_marker_helper"
	remediation := "run `torio vm bootstrap`, which installs " + WaitingMarkerHelper + " root-owned 0755"
	if err := verifyWaitingMarkerDependencies(ctx, r); err != nil {
		return err
	}

	kind, err := r.Probe(ctx, name, "stat", "-c", "%F", WaitingMarkerHelper)
	if err != nil {
		return err
	}
	if kind.ExitCode == 1 && trimmed(kind.Stdout) == "" {
		absent, err := r.Probe(ctx, name, "test", "!", "-e", WaitingMarkerHelper)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return r.Fail(name, "could not prove the hook helper path is absent", remediation)
		}
		if !r.Reconcile() {
			return r.Fail(name, "no waiting-marker helper at "+WaitingMarkerHelper, remediation)
		}
		res, err := r.ProbeInput(ctx, name, embeddedWaitingMarker, waitingMarkerHelperInstallArgv())
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return r.Fail(name, "could not install the waiting-marker helper",
				"confirm passwordless root provisioning is intact and re-run bootstrap")
		}
		r.Record(name+"_installed", true, "installed embedded helper atomically")
		kind, err = r.Probe(ctx, name, "stat", "-c", "%F", WaitingMarkerHelper)
		if err != nil {
			return err
		}
	}
	if kind.ExitCode != 0 || trimmed(kind.Stdout) != "regular file" {
		return r.Fail(name, "the waiting-marker helper is not a regular file",
			"a symlink here would move what the agent's hooks run; remove it and re-run bootstrap")
	}

	og, err := r.Probe(ctx, name, "stat", "-c", "%U:%G %a", WaitingMarkerHelper)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseOwnershipMode(og.Stdout)
	if og.ExitCode != 0 || !ok {
		return r.Fail(name, "could not read the hook helper ownership/mode", remediation)
	}
	if owner != "root" || group != "root" {
		return r.Fail(name, fmt.Sprintf("hook helper owned by %s:%s, want root:root", owner, group),
			"a helper the agent owns is a helper the agent can rewrite between sessions")
	}
	if writable, parsed := modeGrantsForeignWrite(mode); !parsed || writable {
		return r.Fail(name, "hook helper mode "+mode+" is group- or world-writable", "reinstall it 0755 root:root")
	}

	sum, err := r.Probe(ctx, name, "sha256sum", "--", WaitingMarkerHelper)
	if err != nil {
		return err
	}
	got := strings.Fields(string(sum.Stdout))
	want := sha256.Sum256(embeddedWaitingMarker)
	wantHex := hex.EncodeToString(want[:])
	if sum.ExitCode != 0 || len(got) == 0 {
		return r.Fail(name, "could not read the hook helper digest", remediation)
	}
	if got[0] != wantHex {
		return r.Fail(name, "the waiting-marker helper has drifted from the version Torio installs",
			"inspect "+WaitingMarkerHelper+"; drift is reported, never repaired in place")
	}
	r.Record(name, true, "root:root "+mode+" sha256="+wantHex[:12])
	return nil
}

// verifyWaitingMarkerDependencies proves the parser the helper invokes, as the
// backend identity, because that is who runs the hook. A missing parser would
// make every hook fail before writing anything, which from the outside looks
// exactly like an agent that is never waiting.
func verifyWaitingMarkerDependencies(ctx context.Context, r backend.StepRunner) error {
	const (
		name        = "codex_waiting_marker_dependencies"
		wantVersion = "jq-1.7"
	)
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "-H", "--", "/usr/bin/jq", "--version")
	if err != nil {
		return err
	}
	got := trimmed(res.Stdout)
	if res.ExitCode != 0 || got != wantVersion {
		return r.Fail(name, fmt.Sprintf("jq version %q, want %q", got, wantVersion),
			"install the jq 1.7 runtime dependency on this guest, then re-run `torio vm bootstrap`")
	}
	r.Record(name, true, wantVersion)
	return nil
}

// waitingMarkerHelperInstallArgv writes the helper atomically through a
// root-owned staging path in the same directory, so no session ever runs a
// partial script.
func waitingMarkerHelperInstallArgv() []string {
	dir := WaitingMarkerHelper[:strings.LastIndex(WaitingMarkerHelper, "/")]
	script := `
tmp="$(mktemp ` + dir + `/.torio-waiting-marker.XXXXXX)"
trap 'rm -f -- "$tmp"' EXIT
cat >"$tmp"
chown root:root "$tmp"
chmod 0755 "$tmp"
sync -f "$tmp"
mv -T -- "$tmp" ` + WaitingMarkerHelper + `
sync -f ` + dir + `
trap - EXIT
`
	return []string{"sudo", "-n", "/bin/bash", "-ceu", script}
}

// reconcileWaitingMarkerState initializes the persistent empty document that
// proves this box has the managed waiting integration. This reads an agent-owned
// file as a drift detector, not a boundary: the agent can remove or forge it,
// but absence must then degrade status to unknown rather than quietly claiming
// that no session needs attention.
func reconcileWaitingMarkerState(ctx context.Context, r backend.StepRunner) error {
	const name = "codex_waiting_marker_state"
	remediation := "run `torio vm bootstrap` to initialize " + waitingMarkerPath
	kind, err := r.Probe(ctx, name, "stat", "-c", "%F", waitingMarkerPath)
	if err != nil {
		return err
	}
	if kind.ExitCode == 1 && trimmed(kind.Stdout) == "" {
		absent, err := r.Probe(ctx, name, "test", "!", "-e", waitingMarkerPath)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return r.Fail(name, "could not prove the waiting marker path is absent", remediation)
		}
		if !r.Reconcile() {
			return r.Fail(name, "waiting marker state is not initialized", remediation)
		}
		res, err := r.ProbeInput(ctx, name, initialWaitingMarker, waitingMarkerStateInstallArgv())
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return r.Fail(name, "could not initialize waiting marker state", remediation)
		}
		r.Record(name+"_initialized", true, "installed empty agent-owned marker atomically")
		r.Record(name, true, User+":"+User+" 600 (agent-owned drift detector)")
		return nil
	}
	if kind.ExitCode != 0 || trimmed(kind.Stdout) != "regular file" {
		return r.Fail(name, "waiting marker state is not a regular file", remediation)
	}
	og, err := r.Probe(ctx, name, "stat", "-c", "%U:%G %a", waitingMarkerPath)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseOwnershipMode(og.Stdout)
	if og.ExitCode != 0 || !ok || owner != User || group != User || mode != "600" {
		return r.Fail(name, "waiting marker state must be "+User+":"+User+" 600", remediation)
	}
	r.Record(name, true, User+":"+User+" 600 (agent-owned drift detector)")
	return nil
}

func waitingMarkerStateInstallArgv() []string {
	script := `
tmp="$(mktemp ` + Home + `/.torio-waiting.XXXXXX)"
trap 'rm -f -- "$tmp"' EXIT
cat >"$tmp"
chown ` + User + `:` + User + ` "$tmp"
chmod 0600 "$tmp"
sync -f "$tmp"
mv -T -- "$tmp" ` + waitingMarkerPath + `
sync -f ` + Home + `
trap - EXIT
`
	return []string{"sudo", "-n", "/bin/bash", "-ceu", script}
}
