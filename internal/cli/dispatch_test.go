package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// TestGlobalFlagAcceptedBeforeAndAfterCommand ensures --json is a real global
// (persistent) flag: it works whether placed before or after the subcommand.
func TestGlobalFlagAcceptedBeforeAndAfterCommand(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "version"},
		{"version", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, &stdout, &stderr, testBuild())
		if code != int(ExitOK) {
			t.Fatalf("args %v: exit = %d, want 0; stderr=%q", args, code, stderr.String())
		}
		var env map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("args %v: stdout not JSON: %v; got %q", args, err, stdout.String())
		}
		if env["ok"] != true || env["command"] != "version" {
			t.Errorf("args %v: unexpected envelope: %v", args, env)
		}
	}
}
