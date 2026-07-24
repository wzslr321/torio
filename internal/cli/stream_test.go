package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestVerboseDiagnosticsGoToStderrNotStdout ensures that even with --verbose,
// stdout in JSON mode is exactly one JSON document and diagnostics land on
// stderr only. This is the core stdout/stderr separation invariant.
func TestVerboseDiagnosticsGoToStderrNotStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--json", "--verbose"}, &stdout, &stderr, testBuild())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}

	// stdout must decode as exactly one JSON document with nothing after it.
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v; got %q", err, stdout.String())
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document (io.EOF on 2nd decode), got %v; stdout=%q", err, stdout.String())
	}
	// Diagnostics must not leak into stdout.
	if strings.Contains(stdout.String(), "level=") || strings.Contains(stdout.String(), "DEBUG") {
		t.Errorf("stdout contains diagnostic noise: %q", stdout.String())
	}
	// Verbose diagnostics must appear on stderr.
	if stderr.Len() == 0 {
		t.Errorf("expected verbose diagnostics on stderr, got none")
	}
}

func TestNonVerboseIsQuietOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--json"}, &stdout, &stderr, testBuild())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected quiet stderr without --verbose, got %q", stderr.String())
	}
}

func TestHumanVersionKeepsStdoutClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--verbose"}, &stdout, &stderr, testBuild())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	// Human stdout carries the version summary; diagnostics stay on stderr.
	if !strings.Contains(stdout.String(), "1.2.3") {
		t.Errorf("stdout %q missing version", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Errorf("expected verbose diagnostics on stderr")
	}
}
