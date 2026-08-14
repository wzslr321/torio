package codex

import (
	"path"
	"strings"
	"testing"
)

// TestStatusDeclaresOnlyWhatCodexCanProve pins the three separate declarations a
// poll reads. Each one it declares, it must be able to prove; each one it does
// not, the poll reports as unknown rather than as a quiet no.
func TestStatusDeclaresOnlyWhatCodexCanProve(t *testing.T) {
	spec := New().Status()
	if spec == nil {
		t.Fatal("codex declares no status at all, so a live box reads as unknowable")
	}

	// The kernel truncates a process name to fifteen characters, so a longer
	// declaration would silently match nothing in the process table.
	if spec.SessionProcess != path.Base(commandPath) {
		t.Errorf("session process is %q, want the name a session is launched under", spec.SessionProcess)
	}
	if len(spec.SessionProcess) > 15 {
		t.Errorf("session process %q is longer than the process table records", spec.SessionProcess)
	}

	if !spec.WaitingMarker {
		t.Error("codex declares no waiting marker, though its managed hooks write one")
	}

	// Codex writes per-session rollout files under a dated directory and moves
	// history only when a prompt is submitted. Neither is a fixed path whose
	// modification time means "the agent progressed", so declaring one would make
	// a long tool call read as a dead box.
	if len(spec.ProgressPaths) != 0 {
		t.Errorf("codex declares progress paths %v; neither of its records is a fixed progress signal", spec.ProgressPaths)
	}
}

// TestTheMarkerPathTheHelperWritesIsTheOneStatusReads pins the two halves
// together. The poller derives the marker from the identity's home, so a helper
// writing anywhere else would leave status reading an empty document forever.
func TestTheMarkerPathTheHelperWritesIsTheOneStatusReads(t *testing.T) {
	if !strings.HasPrefix(waitingMarkerPath, Home+"/") {
		t.Errorf("the marker at %q is not under the home the poller derives from", waitingMarkerPath)
	}
	if path.Base(waitingMarkerPath) != ".torio-waiting.json" {
		t.Errorf("the marker is called %q, which is not the name the poller looks for", path.Base(waitingMarkerPath))
	}
}
