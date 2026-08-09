package lima

import "testing"

// Hermes proves work and nothing else, and each omission is a declaration.
//
// A session is not a process here: the service holds its sessions as rows, and
// its own liveness is what `torio serve status` answers. Waiting is unknown
// because the predicate that would answer it lives in the memory of the running
// process, not anywhere a poll can read.
func TestHermesStatusDeclaresProgressOnly(t *testing.T) {
	spec := (hermesBackend{}).Status()
	if spec == nil {
		t.Fatal("Status() = nil, want a declared probe")
	}
	if spec.SessionProcess != "" {
		t.Errorf("session process = %q, want none: a Hermes session is not a process", spec.SessionProcess)
	}
	if spec.WaitingMarker {
		t.Error("WaitingMarker = true, want false until Hermes exports the predicate")
	}
	want := []string{HermesProfilePath + "/state.db", HermesProfilePath + "/state.db-wal"}
	if len(spec.ProgressPaths) != len(want) {
		t.Fatalf("progress paths = %v, want %v", spec.ProgressPaths, want)
	}
	for i := range want {
		if spec.ProgressPaths[i] != want[i] {
			t.Fatalf("progress paths = %v, want %v", spec.ProgressPaths, want)
		}
	}
}

// The progress paths sit under the identity's own profile directory, which is
// the only place a poll running as that identity can read.
func TestHermesProgressPathsAreUnderItsProfile(t *testing.T) {
	for _, p := range (hermesBackend{}).Status().ProgressPaths {
		if len(p) <= len(HermesProfilePath) || p[:len(HermesProfilePath)+1] != HermesProfilePath+"/" {
			t.Errorf("progress path %q is not under %q", p, HermesProfilePath)
		}
	}
}
