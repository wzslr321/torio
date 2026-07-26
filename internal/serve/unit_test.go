package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
