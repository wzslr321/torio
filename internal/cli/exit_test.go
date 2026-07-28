package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestExitCodeMappingMatchesContract locks the numeric exit codes to the table
// in docs/contracts/cli.md. Changing these is a contract change.
func TestExitCodeMappingMatchesContract(t *testing.T) {
	cases := map[string]struct {
		got  ExitCode
		want int
	}{
		"success":            {ExitOK, 0},
		"usage/schema":       {ExitUsage, 2},
		"precondition":       {ExitPrecondition, 3},
		"stale/conflict":     {ExitConflict, 5},
		"verification":       {ExitVerification, 6},
		"permission/cap":     {ExitPermission, 7},
		"external dep":       {ExitExternal, 8},
		"reconcile":          {ExitReconcile, 9},
		"internal (not 2-9)": {ExitInternal, 1},
	}
	for name, c := range cases {
		if int(c.got) != c.want {
			t.Errorf("%s: exit code = %d, want %d", name, int(c.got), c.want)
		}
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"frobnicate"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d", code, int(ExitUsage))
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty in human error mode, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "frobnicate") {
		t.Errorf("stderr %q should name the unknown command", stderr.String())
	}
}

func TestNoCommandIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d", code, int(ExitUsage))
	}
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--nope"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d; stderr=%q", code, int(ExitUsage), stderr.String())
	}
}

func TestVersionRejectsExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "extra"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d", code, int(ExitUsage))
	}
}

func TestUsageErrorJSONEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"frobnicate", "--json"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit code = %d, want %d", code, int(ExitUsage))
	}

	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v; got %q", err, stdout.String())
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document (io.EOF on 2nd decode), got %v; stdout=%q", err, stdout.String())
	}
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	if env["data"] != nil {
		t.Errorf("data = %v, want null", env["data"])
	}
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error is not an object: %T (%v)", env["error"], env["error"])
	}
	if errObj["code"] != "USAGE" {
		t.Errorf("error.code = %v, want USAGE", errObj["code"])
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		t.Errorf("error.message should be non-empty")
	}
}
