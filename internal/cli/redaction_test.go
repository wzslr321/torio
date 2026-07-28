package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/redact"
)

// knownShapeCanary is a fake token matching a well-known secret shape (OpenAI).
// It is not a real credential.
const knownShapeCanary = "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"

// TestKnownShapeCanaryMatchesProductionMatcher proves the fixture used as a
// known-shape canary is actually recognized by the production redactor before
// the end-to-end tests below rely on it. An abbreviated, non-matching value
// would never be redacted, so those tests could only "pass" by never having a
// secret to leak; this assertion fails loudly in that case.
func TestKnownShapeCanaryMatchesProductionMatcher(t *testing.T) {
	out := redact.String(knownShapeCanary)
	if strings.Contains(out, knownShapeCanary) {
		t.Fatalf("known-shape canary is not recognized by the production redactor")
	}
	if !strings.Contains(out, redact.Placeholder) {
		t.Fatalf("known-shape canary did not redact to placeholder: %q", out)
	}
}

// TestFailRedactsKnownShape_Human exercises the final renderer directly on the
// human (stderr) path.
func TestFailRedactsKnownShape_Human(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := fail(&stdout, &stderr, "version", false,
		&CLIError{Exit: ExitInternal, Code: "INTERNAL", Message: "boom " + knownShapeCanary})

	if code != int(ExitInternal) {
		t.Fatalf("exit = %d, want %d", code, int(ExitInternal))
	}
	if strings.Contains(stderr.String(), knownShapeCanary) {
		t.Errorf("stderr leaked the canary")
	}
	if !strings.Contains(stderr.String(), redact.Placeholder) {
		t.Errorf("stderr not redacted: %q", stderr.String())
	}
}

// TestFailRedactsKnownShape_JSON exercises the renderer on the --json path.
// Details are covered here and nowhere else: they are the one error surface
// that carries structured, adapter-supplied values (bounded guest output,
// bootstrap checks), so a leak there would not show up in the message tests.
func TestFailRedactsKnownShape_JSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fail(&stdout, &stderr, "version", true,
		&CLIError{
			Exit:    ExitInternal,
			Code:    "INTERNAL",
			Message: "boom " + knownShapeCanary,
			Details: map[string]any{
				"context": "value " + knownShapeCanary,
				"nested":  map[string]any{"inner": "value " + knownShapeCanary},
			},
		})

	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout not a JSON document: %v", err)
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document (io.EOF on 2nd decode), got %v", err)
	}
	if strings.Contains(stdout.String(), knownShapeCanary) {
		t.Errorf("json envelope leaked the canary: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), redact.Placeholder) {
		t.Errorf("json envelope not redacted")
	}
}

// TestRunRedactsKnownShapeInError_Human proves the end-to-end error path scrubs
// a known secret shape that appears in an error message (here, an unknown
// command whose name is a canary token).
func TestRunRedactsKnownShapeInError_Human(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{knownShapeCanary}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, int(ExitUsage))
	}
	if strings.Contains(stderr.String(), knownShapeCanary) {
		t.Errorf("stderr leaked known-shape canary: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), redact.Placeholder) {
		t.Errorf("stderr not redacted: %q", stderr.String())
	}
}

func TestRunRedactsKnownShapeInError_JSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{knownShapeCanary, "--json"}, &stdout, &stderr, testBuild())
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, int(ExitUsage))
	}

	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout not a JSON document: %v", err)
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document (io.EOF on 2nd decode), got %v", err)
	}
	if strings.Contains(stdout.String(), knownShapeCanary) {
		t.Errorf("json envelope leaked known-shape canary: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), redact.Placeholder) {
		t.Errorf("json envelope not redacted")
	}
}
