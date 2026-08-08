package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

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
	if err := json.Unmarshal(ManagedSettings(), &doc); err != nil {
		t.Fatalf("managed settings are not valid JSON: %v", err)
	}

	// Two events set the marker and two clear it. Setting without clearing
	// leaves a plea nobody withdrew; clearing without setting is a surface that
	// never says anything.
	want := map[string]string{
		"Notification":     "notification",
		"Stop":             "notification",
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
// same file, the same schema version and the same kinds, and neither is free to
// move without the other.
func TestWaitingMarkerHelperWritesTheConventionTheReaderEnforces(t *testing.T) {
	script := string(WaitingMarkerScript())
	for _, want := range []string{
		`marker="$HOME/.` + strings.TrimPrefix(status.MarkerFileName, ".") + `"`,
		`"schema_version":"` + status.MarkerSchemaVersion + `"`,
		`permission|notification)`,
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
	// Reading it here is how agent-written prose would reach a rendered line.
	if strings.Contains(script, "read ") || strings.Contains(script, "cat -") || strings.Contains(script, "$(cat)") {
		t.Error("the helper reads its standard input; the hook payload carries agent-written text")
	}
}

// The kinds the helper accepts are exactly the ones the reader recognizes. A
// kind on one side only is a marker written and then refused, which renders as
// unknown for as long as its author keeps writing it.
func TestWaitingMarkerKindsAgreeWithTheReader(t *testing.T) {
	script := string(WaitingMarkerScript())
	for _, kind := range []string{status.KindPermission, status.KindNotification} {
		if !strings.Contains(script, kind) {
			t.Errorf("waiting-marker helper does not accept the kind %q the reader recognizes", kind)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
