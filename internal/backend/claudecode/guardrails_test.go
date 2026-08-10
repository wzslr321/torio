package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// digestLine is what `sha256sum -- <path>` writes: the hex sum, two spaces and
// the path. The checks read only the first field, and a fixture that carried
// the sum alone would pass a check that had stopped splitting.
func digestLine(content []byte, path string) execx.Result {
	sum := sha256.Sum256(content)
	return out(hex.EncodeToString(sum[:]) + "  " + path + "\n")
}

const (
	settingsKind   = "stat -c %F " + managedSettingsPath
	settingsOG     = "stat -c %U:%G %a " + managedSettingsPath
	settingsSum    = "sha256sum -- " + managedSettingsPath
	helperJQ       = "sudo -n -u " + User + " -H -- /usr/bin/jq --version"
	helperKind     = "stat -c %F " + WaitingMarkerHelper
	helperOG       = "stat -c %U:%G %a " + WaitingMarkerHelper
	helperSum      = "sha256sum -- " + WaitingMarkerHelper
	markerKind     = "stat -c %F " + waitingMarkerPath
	markerOG       = "stat -c %U:%G %a " + waitingMarkerPath
	mcpConfigProbe = "sudo -n -u " + User + " -- test -f " + mcpConfigPath
)

// healthyGuest answers every probe VerifyGuardrails makes on a box that is
// exactly as Torio left it. Tests copy it and spoil one answer, so what a case
// is about is the line it overrides rather than the twelve it repeats.
func healthyGuest() map[string]execx.Result {
	return map[string]execx.Result{
		settingsKind:   out("regular file\n"),
		settingsOG:     out("root:root 644\n"),
		settingsSum:    digestLine(embeddedManagedSettings, managedSettingsPath),
		helperJQ:       out("jq-1.7\n"),
		helperKind:     out("regular file\n"),
		helperOG:       out("root:root 755\n"),
		helperSum:      digestLine(embeddedWaitingMarker, WaitingMarkerHelper),
		markerKind:     out("regular file\n"),
		markerOG:       out(User + ":" + User + " 600\n"),
		mcpConfigProbe: exit(1),
	}
}

func guestWith(overrides map[string]execx.Result) *fakeRunner {
	answers := healthyGuest()
	for probe, result := range overrides {
		answers[probe] = result
	}
	return newFakeRunner(answers)
}

// TestGuardrailsPassOnABoxTorioLeftAlone is the baseline the drift cases are
// read against. Without it a spoiled fixture could fail for the wrong reason
// and every case below would still look like it proved something.
func TestGuardrailsPassOnABoxTorioLeftAlone(t *testing.T) {
	r := guestWith(nil)
	if err := New().VerifyGuardrails(context.Background(), r); err != nil {
		t.Fatalf("VerifyGuardrails on an untouched box: %v", err)
	}
	for _, name := range []string{"claude_managed_settings", "claude_waiting_marker_helper"} {
		if r.records[name] == "" {
			t.Errorf("a passing check recorded nothing for %q", name)
		}
	}
}

// TestManagedSettingsContentDriftIsReportedNeverRepaired covers the case an
// operator actually meets: a box bootstrapped by an earlier release carries the
// settings that release installed, and they are no longer the ones Torio ships.
//
// The rule under test is that this is reported and refused rather than
// overwritten. A guardrail that silently rewrites itself is one whose changes
// nobody reviews, so the remedy has to reach the operator as text — which is
// why the message is asserted, not only the error.
func TestManagedSettingsContentDriftIsReportedNeverRepaired(t *testing.T) {
	r := guestWith(map[string]execx.Result{
		settingsSum: digestLine([]byte(`{"permissions":{"defaultMode":"acceptEdits"}}`), managedSettingsPath),
	})

	err := reconcileManagedSettings(context.Background(), r)
	if err == nil {
		t.Fatal("settings that are not the ones Torio installs passed the check")
	}
	if !strings.Contains(r.failed, "drifted") {
		t.Errorf("failure does not say the content drifted: %q", r.failed)
	}
	// The reconciling runner must not have been asked to write anything: drift
	// is answered by an operator removing the file, never by Torio replacing it.
	if r.records["claude_managed_settings_installed"] != "" {
		t.Error("drifted settings were reinstalled in place")
	}
}

// TestManagedSettingsRefuseEveryStateTheyCannotProve walks the branches that a
// mutation survived before this test existed: each one turns an unreadable or
// unexpected answer into a refusal, and each was previously constrained by
// nothing. A check that reads "I could not tell" as "fine" is worse than no
// check, because it reports a boundary it never verified.
func TestManagedSettingsRefuseEveryStateTheyCannotProve(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override map[string]execx.Result
		says     string
	}{
		{
			// stat says nothing at all, and the absence probe cannot confirm it
			// either. Neither answer proves the file is missing, so installing
			// one would be writing over something unseen.
			name: "absence that cannot be proven",
			override: map[string]execx.Result{
				settingsKind:                       exit(1),
				"test ! -e " + managedSettingsPath: exit(2),
			},
			says: "absent",
		},
		{
			// A symlink here moves the settings to a path the agent may own,
			// which is the whole point of pinning root ownership.
			name:     "a path that is not a regular file",
			override: map[string]execx.Result{settingsKind: out("symbolic link\n")},
			says:     "regular file",
		},
		{
			// The ownership probe answered, but failed. Reading the fields
			// anyway would accept a stale or partial line as proof of root.
			name:     "ownership that could not be read",
			override: map[string]execx.Result{settingsOG: execx.Result{ExitCode: 2, Stdout: []byte("root:root 644\n")}},
			says:     "ownership",
		},
		{
			name:     "ownership in an unparseable shape",
			override: map[string]execx.Result{settingsOG: out("root 644\n")},
			says:     "ownership",
		},
		{
			name:     "settings the agent's group can rewrite",
			override: map[string]execx.Result{settingsOG: out("root:root 664\n")},
			says:     "writable",
		},
		{
			name:     "settings owned by the identity they constrain",
			override: map[string]execx.Result{settingsOG: out(User + ":" + User + " 644\n")},
			says:     "want root:root",
		},
		{
			name:     "a digest the guest would not produce",
			override: map[string]execx.Result{settingsSum: execx.Result{ExitCode: 1}},
			says:     "digest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := guestWith(tc.override)
			if err := reconcileManagedSettings(context.Background(), r); err == nil {
				t.Fatalf("%s passed the managed settings check", tc.name)
			}
			if !strings.Contains(r.failed, tc.says) {
				t.Errorf("failure does not name the problem, want it to mention %q: %q", tc.says, r.failed)
			}
		})
	}
}

// TestWaitingMarkerHelperRefusesEveryStateItCannotProve is the same walk over
// the hook helper. It is a separate root-owned file with its own drift, which
// is exactly the fact the release notes for the settings change missed: an
// operator who repaired only the settings met this one next.
func TestWaitingMarkerHelperRefusesEveryStateItCannotProve(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override map[string]execx.Result
		says     string
	}{
		{
			name: "absence that cannot be proven",
			override: map[string]execx.Result{
				helperKind:                         exit(1),
				"test ! -e " + WaitingMarkerHelper: exit(2),
			},
			says: "absent",
		},
		{
			name:     "a path that is not a regular file",
			override: map[string]execx.Result{helperKind: out("symbolic link\n")},
			says:     "regular file",
		},
		{
			name:     "ownership that could not be read",
			override: map[string]execx.Result{helperOG: execx.Result{ExitCode: 2, Stdout: []byte("root:root 755\n")}},
			says:     "ownership",
		},
		{
			name:     "a helper the agent owns",
			override: map[string]execx.Result{helperOG: out(User + ":" + User + " 755\n")},
			says:     "want root:root",
		},
		{
			name:     "a helper the agent's group can rewrite",
			override: map[string]execx.Result{helperOG: out("root:root 775\n")},
			says:     "writable",
		},
		{
			name:     "content that is not what Torio ships",
			override: map[string]execx.Result{helperSum: digestLine([]byte("#!/bin/sh\nexit 0\n"), WaitingMarkerHelper)},
			says:     "drifted",
		},
		{
			// The helper is only as good as the parser it invokes: without it
			// every hook fails before writing the marker, and the box would
			// report a quiet agent rather than an unknown one.
			name:     "a guest missing the parser the helper runs",
			override: map[string]execx.Result{helperJQ: exit(127)},
			says:     "jq",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := guestWith(tc.override)
			if err := reconcileWaitingMarkerHelper(context.Background(), r); err == nil {
				t.Fatalf("%s passed the hook helper check", tc.name)
			}
			if !strings.Contains(r.failed, tc.says) {
				t.Errorf("failure does not name the problem, want it to mention %q: %q", tc.says, r.failed)
			}
		})
	}
}

// TestVerifyGuardrailsReportsEachManagedFileInTurn pins the migration an
// operator upgrading an older box actually performs.
//
// Such a box carries two files this release changed, and VerifyGuardrails stops
// at the first: repairing the settings does not finish the migration, because
// the hook helper is still the previous one and fails next. The sequence is
// pinned here because a release note derived from a single run of this check
// describes one file and leaves the operator to discover the second from a
// second failed bootstrap, which is how this was found.
func TestVerifyGuardrailsReportsEachManagedFileInTurn(t *testing.T) {
	previousRelease := map[string]execx.Result{
		settingsSum: digestLine([]byte(`{"permissions":{}}`), managedSettingsPath),
		helperSum:   digestLine([]byte("#!/bin/sh\n# the previous helper\n"), WaitingMarkerHelper),
	}

	r := guestWith(previousRelease)
	if err := New().VerifyGuardrails(context.Background(), r); err == nil {
		t.Fatal("a box from before this release passed the guardrail checks")
	}
	if !strings.HasPrefix(r.failed, "claude_managed_settings:") {
		t.Fatalf("first refusal is not about the managed settings: %q", r.failed)
	}

	// The operator removes the drifted settings and re-runs bootstrap, which
	// reinstalls them. The helper is untouched by that repair.
	repaired := guestWith(map[string]execx.Result{helperSum: previousRelease[helperSum]})
	if err := New().VerifyGuardrails(context.Background(), repaired); err == nil {
		t.Fatal("repairing only the settings finished the migration")
	}
	if !strings.HasPrefix(repaired.failed, "claude_waiting_marker_helper:") {
		t.Fatalf("second refusal is not about the hook helper: %q", repaired.failed)
	}
	if !strings.Contains(repaired.failed, "drifted") {
		t.Errorf("the second failure does not tell the operator what is wrong: %q", repaired.failed)
	}
}
