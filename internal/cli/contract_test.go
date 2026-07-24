package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestD2PendingGlobalsAreNotUsableInD1 keeps the D1 CLI contract truthful:
// --config and --state-dir are documented globals but are introduced in D2.
// In D1 they must be rejected as unknown flags, not silently accepted.
func TestD2PendingGlobalsAreNotUsableInD1(t *testing.T) {
	for _, flag := range []string{"--config", "--state-dir"} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"version", flag, "/tmp/x"}, &stdout, &stderr, testBuild())
		if code != int(ExitUsage) {
			t.Errorf("%s: exit = %d, want %d (D2-pending flag must be rejected in D1)", flag, code, int(ExitUsage))
		}
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
