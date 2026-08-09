package claudecode

import (
	"testing"
)

// The declared process name must be the base name of the path a session is
// actually launched through: the kernel names a process after the path given to
// exec, so a session started via the command-path symlink appears under the
// symlink's own name and not the pinned binary's.
func TestSessionProcessIsTheNameASessionIsLaunchedUnder(t *testing.T) {
	spec := (claudeBackend{}).Status()
	if spec == nil {
		t.Fatal("Status() = nil, want a declared probe")
	}
	if spec.SessionProcess != "claude" {
		t.Fatalf("session process = %q, want the base name of %q", spec.SessionProcess, commandPath)
	}
	if got := loginArgv()[len(loginArgv())-1]; got != commandPath {
		t.Fatalf("login argv ends with %q, want the same command path the name is derived from", got)
	}
}

// The kernel keeps fifteen characters of a process name. A longer declaration
// would match nothing on every box, silently and forever.
func TestSessionProcessFitsWhatTheKernelKeeps(t *testing.T) {
	const kernelCommLimit = 15
	if got := len((claudeBackend{}).Status().SessionProcess); got > kernelCommLimit {
		t.Fatalf("session process is %d characters, want at most %d", got, kernelCommLimit)
	}
}

// The marker is declared because this backend's hooks write it; the progress
// reading is not declared because the only fixed-path file it has moves when a
// prompt is submitted rather than while one is worked on.
func TestStatusDeclaresTheMarkerAndNoProgressReading(t *testing.T) {
	spec := (claudeBackend{}).Status()
	if !spec.WaitingMarker {
		t.Error("WaitingMarker = false, want the marker this backend's hooks write")
	}
	if len(spec.ProgressPaths) != 0 {
		t.Errorf("progress paths = %v, want none declared", spec.ProgressPaths)
	}
}
