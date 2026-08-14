package codex

import (
	"context"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

func markerHelperProbes() map[string]execx.Result {
	return map[string]execx.Result{
		"sudo -n -u " + User + " -H -- /usr/bin/jq --version": out("jq-1.7\n"),
		"stat -c %F " + WaitingMarkerHelper:                   out("regular file\n"),
		"stat -c %U:%G %a " + WaitingMarkerHelper:             out("root:root 755\n"),
		"sha256sum -- " + WaitingMarkerHelper:                 out(digestOf(embeddedWaitingMarker) + "  " + WaitingMarkerHelper + "\n"),
	}
}

// TestTheHelperTheAgentsHooksRunIsTheOneTorioWrote pins the digest check. The
// managed configuration names this path, so a helper the agent could rewrite
// would be an agent choosing what its own hooks do.
func TestTheHelperTheAgentsHooksRunIsTheOneTorioWrote(t *testing.T) {
	t.Run("the installed helper passes", func(t *testing.T) {
		r := newFakeRunner(markerHelperProbes())
		if err := reconcileWaitingMarkerHelper(context.Background(), r); err != nil {
			t.Fatalf("reconcileWaitingMarkerHelper: %v", err)
		}
	})

	t.Run("drift is reported and not rewritten", func(t *testing.T) {
		probes := markerHelperProbes()
		probes["sha256sum -- "+WaitingMarkerHelper] = out(strings.Repeat("b", 64) + "  " + WaitingMarkerHelper + "\n")
		r := newFakeRunner(probes)
		if err := reconcileWaitingMarkerHelper(context.Background(), r); err == nil {
			t.Fatal("a drifted helper passed")
		}
		if r.saw("/bin/bash -ceu") {
			t.Error("drift was repaired in place instead of being reported")
		}
	})

	t.Run("an agent-owned helper fails", func(t *testing.T) {
		probes := markerHelperProbes()
		probes["stat -c %U:%G %a "+WaitingMarkerHelper] = out("codex:codex 755\n")
		r := newFakeRunner(probes)
		if err := reconcileWaitingMarkerHelper(context.Background(), r); err == nil {
			t.Fatal("a helper the agent owns passed")
		}
	})

	// The parser is checked as the agent identity, because that is who runs the
	// hook. A missing parser would make every hook fail before writing anything,
	// which would look exactly like an agent that is never waiting.
	t.Run("a missing parser fails before anything else is proven", func(t *testing.T) {
		probes := markerHelperProbes()
		probes["sudo -n -u "+User+" -H -- /usr/bin/jq --version"] = exit(127)
		r := newFakeRunner(probes)
		if err := reconcileWaitingMarkerHelper(context.Background(), r); err == nil {
			t.Fatal("a guest without the parser passed")
		}
	})
}

// TestWaitingMarkerStateIsInitializedAgentOwned pins that bootstrap leaves an
// empty document behind. Without one, an absent marker is ambiguous: it could
// mean nothing is waiting or it could mean the integration was never installed.
func TestWaitingMarkerStateIsInitializedAgentOwned(t *testing.T) {
	t.Run("an existing document must be the agent's own and private", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{
			"stat -c %F " + waitingMarkerPath:       out("regular file\n"),
			"stat -c %U:%G %a " + waitingMarkerPath: out(User + ":" + User + " 600\n"),
		})
		if err := reconcileWaitingMarkerState(context.Background(), r); err != nil {
			t.Fatalf("reconcileWaitingMarkerState: %v", err)
		}
		if got := r.records["codex_waiting_marker_state"]; !strings.Contains(got, "drift detector") {
			t.Errorf("recorded %q, want it to say what this file is and is not", got)
		}
	})

	t.Run("a world-readable marker fails", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{
			"stat -c %F " + waitingMarkerPath:       out("regular file\n"),
			"stat -c %U:%G %a " + waitingMarkerPath: out(User + ":" + User + " 644\n"),
		})
		if err := reconcileWaitingMarkerState(context.Background(), r); err == nil {
			t.Fatal("a marker anyone can read passed")
		}
	})
}

// TestTheMarkerScriptSpeaksThisBackendsOwnNames pins the two substitutions that
// make the shared script this backend's. Getting either wrong produces a helper
// that runs, exits cleanly, and never marks anything.
func TestTheMarkerScriptSpeaksThisBackendsOwnNames(t *testing.T) {
	script := string(embeddedWaitingMarker)

	if !strings.Contains(script, "session_process='"+User+"'") {
		t.Error("the helper looks for another backend's process name when it walks the process tree")
	}
	if !strings.Contains(script, waitingMarkerPath[strings.LastIndex(waitingMarkerPath, "/"):]) {
		t.Error("the helper writes a marker the poller does not read")
	}
	// The helper runs as the agent, inside a session that may hold a forwarded
	// agent socket. Nothing here should be able to reach it.
	if strings.Contains(script, "SSH_AUTH_SOCK") {
		t.Error("the waiting-marker helper mentions the forwarded agent socket")
	}
	// The session id is the only thing taken from a document the agent writes,
	// and it is taken through a filter that bounds it.
	if !strings.Contains(script, ".session_id") {
		t.Error("the helper does not read the session id the hook hands it")
	}
}
