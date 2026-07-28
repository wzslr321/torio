package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/brain"
)

// TestRenderUnitMatchesGolden locks the exact bytes of the generated user unit.
// A change to the rendered unit must be a deliberate, reviewed change to the
// golden file — the bind, profile pin, and restart policy are security-relevant.
func TestRenderUnitMatchesGolden(t *testing.T) {
	got := renderUnit()
	goldenPath := filepath.Join("testdata", "hermes-serve.service.golden")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("rendered unit does not match golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderUnitEnforcesInvariants proves the security-relevant directives are
// present regardless of golden formatting: loopback bind only, the HERMES_HOME
// profile pin, and Restart=always. These are the D5 invariants.
func TestRenderUnitEnforcesInvariants(t *testing.T) {
	u := string(renderUnit())
	invariants := []string{
		"Environment=HERMES_HOME=/home/hermes/.hermes",
		"--host 127.0.0.1",
		"--port 9119",
		"Restart=always",
		"WantedBy=default.target",
		"ExecStart=/usr/local/bin/hermes serve --skip-build",
	}
	for _, want := range invariants {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing required directive %q\n%s", want, u)
		}
	}
	// The bind must never be a public address in a rendered unit.
	for _, forbidden := range []string{"0.0.0.0", "--host 0", "::"} {
		if strings.Contains(u, forbidden) {
			t.Errorf("unit contains a non-loopback bind marker %q", forbidden)
		}
	}
}

// The hint is the one channel that reaches a session regardless of which skill
// the model loads, so what it says is a product decision, not formatting.
func TestUnitCarriesTheBrainEnvironmentHint(t *testing.T) {
	u := string(renderUnit())
	if !strings.Contains(u, "Environment=\"HERMES_ENVIRONMENT_HINT=") {
		t.Fatalf("unit does not set HERMES_ENVIRONMENT_HINT:\n%s", u)
	}
	for _, want := range []string{
		brain.Path,      // where the vault is
		brain.SkillName, // what to read it with
		"bulk",          // the rule the bundled competitor lacks
	} {
		if !strings.Contains(u, want) {
			t.Errorf("environment hint does not mention %q:\n%s", want, brain.EnvironmentHint)
		}
	}
}

// systemd would expand or terminate the value early on these, and a truncated
// hint is worse than none: it would deliver half a sentence to every session.
func TestEnvironmentHintSurvivesSystemdQuoting(t *testing.T) {
	for _, forbidden := range []string{"\n", "\"", "$", "%", "\\"} {
		if strings.Contains(brain.EnvironmentHint, forbidden) {
			t.Errorf("environment hint contains %q, which systemd does not carry verbatim in a quoted value", forbidden)
		}
	}
	// One Environment= line per directive: a stray newline would silently make
	// the remainder of the hint an unparsable unit directive.
	if got := strings.Count(string(renderUnit()), "HERMES_ENVIRONMENT_HINT"); got != 1 {
		t.Errorf("HERMES_ENVIRONMENT_HINT appears %d times, want exactly 1", got)
	}
}
