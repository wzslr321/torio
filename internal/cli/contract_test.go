package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestUnknownGlobalFlagStillRejected keeps the fail-closed flag contract: a
// genuinely unknown persistent flag remains a usage error even after D2 adds
// --config/--state-dir. (Those two are now real globals; see config_test.go.)
func TestUnknownGlobalFlagStillRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--not-a-flag", "/tmp/x"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Errorf("unknown flag: exit = %d, want %d", code, int(ExitUsage))
	}
}

// TestHelpIsTheNarrowExceptionToJSON documents and locks the one exception to
// the --json single-envelope invariant: --help is a human-only affordance that
// prints usage text to stdout and exits 0, even when --json is present. It must
// NOT emit an envelope, and it must not be treated as a JSON document.
func TestHelpIsTheNarrowExceptionToJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--help"},
		{"version", "--json", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, &stdout, &stderr, testBuild())
		if code != int(ExitOK) {
			t.Fatalf("%v: exit = %d, want 0; stderr=%q", args, code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Usage:") {
			t.Errorf("%v: expected human help text with 'Usage:', got %q", args, out)
		}
		// Must not be our JSON envelope.
		var env map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); err == nil {
			if _, isEnvelope := env["schema_version"]; isEnvelope {
				t.Errorf("%v: --help produced a JSON envelope; help must be the human-only exception", args)
			}
		}
	}
}
