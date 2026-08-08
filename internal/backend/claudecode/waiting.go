package claudecode

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// WaitingMarkerHelper is the fixed guest path the managed settings run as a
// hook. It is root-owned for the same reason the settings that name it are: the
// agent runs it, and an agent that could rewrite it could decide for itself
// whether the operator is ever told it is waiting.
const WaitingMarkerHelper = "/usr/local/bin/torio-waiting-marker"

//go:embed templates/torio-waiting-marker.sh
var embeddedWaitingMarker []byte

// WaitingMarkerScript returns the helper's exact bytes, exported so a test can
// lock them. What the agent's hooks write into its own home, and in what shape,
// is the thing a status surface then renders.
func WaitingMarkerScript() []byte { return embeddedWaitingMarker }

// reconcileWaitingMarkerHelper proves the hook helper is the one Torio wrote.
//
// It is installed when absent and reported, never rewritten, when it has
// drifted — the same rule the managed settings follow, and for the same reason:
// a guardrail that repairs itself silently is a guardrail whose changes nobody
// reviews. A box bootstrapped before this existed reports it missing until
// `torio vm bootstrap` runs again, and reports its agent's waiting state as
// unknown in the meantime, which is the honest answer for a box whose hooks
// were never installed.
func reconcileWaitingMarkerHelper(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_waiting_marker_helper"
	remediation := "run `torio vm bootstrap`, which installs " + WaitingMarkerHelper + " root-owned 0755"

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
		if err := installWaitingMarkerHelper(ctx, r, name); err != nil {
			return err
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
		return r.Fail(name, "hook helper mode "+mode+" is group- or world-writable",
			"reinstall it 0755 root:root")
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

// installWaitingMarkerHelper writes the helper atomically through a root-owned
// staging path in the same directory, so no session ever runs a partial script.
func installWaitingMarkerHelper(ctx context.Context, r backend.StepRunner, name string) error {
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
	res, err := r.ProbeInput(ctx, name, embeddedWaitingMarker,
		[]string{"sudo", "-n", "/bin/bash", "-ceu", script})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "could not install the waiting-marker helper",
			"confirm passwordless root provisioning is intact and re-run bootstrap")
	}
	return nil
}
