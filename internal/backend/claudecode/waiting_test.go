package claudecode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/status"
)

// The settings and the helper are one guardrail in two files, so what the
// settings run must be the file bootstrap installs and proves. A path that
// drifted apart here would install a helper nothing calls, and call a helper
// nothing installed — and the status surface would report every agent as not
// waiting, forever and silently.
func TestManagedSettingsRunTheInstalledHelper(t *testing.T) {
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(embeddedManagedSettings, &doc); err != nil {
		t.Fatalf("managed settings are not valid JSON: %v", err)
	}

	// Two events set the marker and two clear it. Setting without clearing
	// leaves a plea nobody withdrew; clearing without setting is a surface that
	// never says anything.
	want := map[string]string{
		"Notification":     "set",
		"Stop":             "set",
		"UserPromptSubmit": "clear",
		"SessionEnd":       "clear",
	}
	if len(doc.Hooks) != len(want) {
		t.Fatalf("hook events = %v, want exactly %v", keysOf(doc.Hooks), want)
	}
	for event, arg := range want {
		matchers, ok := doc.Hooks[event]
		if !ok || len(matchers) != 1 || len(matchers[0].Hooks) != 1 {
			t.Fatalf("%s hook = %+v, want exactly one command", event, matchers)
		}
		h := matchers[0].Hooks[0]
		if h.Type != "command" {
			t.Errorf("%s hook type = %q, want %q", event, h.Type, "command")
		}
		if h.Command != WaitingMarkerHelper+" "+arg {
			t.Errorf("%s hook command = %q, want %q", event, h.Command, WaitingMarkerHelper+" "+arg)
		}
	}
}

// The helper writes the convention the reader enforces. Both sides name the
// same file and the same schema version, and neither is free to
// move without the other.
func TestWaitingMarkerHelperWritesTheConventionTheReaderEnforces(t *testing.T) {
	script := string(embeddedWaitingMarker)
	for _, want := range []string{
		`marker="$HOME/.` + strings.TrimPrefix(status.MarkerFileName, ".") + `"`,
		`"schema_version":"` + status.MarkerSchemaVersion + `"`,
		`set)`,
		`chmod 0600 "$tmp"`,
		`mv -T -- "$tmp" "$marker"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("waiting-marker helper is missing %q", want)
		}
	}
	// The reader refuses a marker anyone but its owner could write, so the file
	// must never exist at a wider mode — not even for the moment between being
	// created and being tightened.
	if strings.Contains(script, "chmod 0600 \"$marker\"") {
		t.Error("the helper tightens the marker after publishing it; it must be created private")
	}
	// Claude Code feeds a hook a JSON document carrying the session's own text.
	// The only permitted read is jq selecting the validated session_id; a raw
	// shell read or cat would let prose enter the marker path unchecked.
	if strings.Contains(script, "read ") || strings.Contains(script, "cat -") || strings.Contains(script, "$(cat)") {
		t.Error("the helper reads its standard input without the bounded jq selector")
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The helper finds the waiting session by walking up to the nearest ancestor
// that is the agent, so the name it walks for must be the one the probe
// declares. Two spellings here would write a marker that names nothing, on
// every box, silently.
func TestWaitingMarkerHelperWalksForTheDeclaredSessionProcess(t *testing.T) {
	script := string(embeddedWaitingMarker)
	spec := (claudeBackend{}).Status()
	if !strings.Contains(script, "session_process='"+spec.SessionProcess+"'") {
		t.Errorf("helper does not walk for %q, the process name the probe declares", spec.SessionProcess)
	}
	// A per-session entry without a process cannot be ranked below liveness. It
	// must fail closed instead of becoming a box-wide flag that another session
	// could accidentally keep alive.
	if !strings.Contains(script, "die 'could not identify the waiting session process'") {
		t.Error("helper does not fail closed when the hook has no agent ancestor")
	}
}

// Claude's hook contract carries a stable session_id on stdin. The helper must
// key updates and clears by that identifier so one session cannot erase a wait
// another live session still owns.
func TestWaitingMarkerHelperKeepsIndependentSessionEntries(t *testing.T) {
	script := string(embeddedWaitingMarker)
	for _, want := range []string{
		`/usr/bin/jq -er`,
		`.session_id`,
		`"schema_version":"2"`,
		`"waits"`,
		`select(.session_id != $session_id)`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("waiting-marker helper is missing %q", want)
		}
	}
	if strings.Contains(script, `clear)
    rm -f -- "$marker"`) {
		t.Error("clear removes the box-wide marker instead of only its session entry")
	}
	if strings.Contains(script, `/usr/bin/rm -f -- "$marker"`) {
		t.Error("the helper removes readiness state instead of publishing an empty marker")
	}
	if !strings.Contains(script, `/usr/bin/sync -f "$HOME"`) {
		t.Error("the helper does not fsync the marker directory after publishing")
	}
	for _, want := range []string{
		`.since_unix >= ($now - 3600)`,
		`.since_unix <= $now`,
		`.waits | length <= 64`,
		`.waits[-64:]`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("waiting-marker helper does not prune and cap state: missing %q", want)
		}
	}
}

// Bootstrap must prove the parser the root-owned helper invokes. Otherwise a
// box can pass verification while every hook fails before writing a marker and
// status confidently reports not-waiting.
func TestWaitingMarkerParserIsAVerifiedDependency(t *testing.T) {
	const probe = "sudo -n -u " + User + " -H -- /usr/bin/jq --version"

	t.Run("the pinned guest answer passes", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: out("jq-1.7\n")})
		if err := verifyWaitingMarkerDependencies(context.Background(), r); err != nil {
			t.Fatalf("verifyWaitingMarkerDependencies: %v", err)
		}
		if r.records["claude_waiting_marker_dependencies"] == "" {
			t.Error("passing dependency check recorded nothing")
		}
	})

	t.Run("a missing parser fails closed", func(t *testing.T) {
		r := newFakeRunner(map[string]execx.Result{probe: exit(127)})
		if err := verifyWaitingMarkerDependencies(context.Background(), r); err == nil {
			t.Fatal("missing jq passed the hook dependency check")
		}
	})
}

func TestBootstrapInitializesPersistentWaitingStateCrashSafely(t *testing.T) {
	kindProbe := "stat -c %F " + waitingMarkerPath
	absentProbe := "test ! -e " + waitingMarkerPath
	installProbe := strings.Join(waitingMarkerStateInstallArgv(), " ")
	r := newFakeRunner(map[string]execx.Result{
		kindProbe:    exit(1),
		absentProbe:  exit(0),
		installProbe: exit(0),
	})

	if err := reconcileWaitingMarkerState(context.Background(), r); err != nil {
		t.Fatalf("reconcileWaitingMarkerState: %v", err)
	}
	if r.records["claude_waiting_marker_state_initialized"] == "" {
		t.Error("initializing the marker recorded no evidence")
	}
	if r.records["claude_waiting_marker_state"] == "" {
		t.Error("the successful initialization left the readiness check absent")
	}

	script := installProbe
	for _, want := range []string{
		"mktemp " + Home + "/.torio-waiting.",
		"chown " + User + ":" + User,
		"chmod 0600",
		`sync -f "$tmp"`,
		"mv -T --",
		"sync -f " + Home,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("marker initializer is missing %q", want)
		}
	}
	if got := string(initialWaitingMarker); got != `{"schema_version":"2","waits":[]}`+"\n" {
		t.Errorf("initial marker = %q, want the empty current schema", got)
	}
}
