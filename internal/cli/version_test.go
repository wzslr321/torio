package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func testBuild() BuildInfo {
	return BuildInfo{
		Version:   "1.2.3",
		Commit:    "abcdef0",
		BuildDate: "2026-07-24T00:00:00Z",
		GoVersion: "go1.26.5",
		OS:        "darwin",
		Arch:      "arm64",
	}
}

func TestVersionHumanReadable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version"}, &stdout, &stderr, testBuild())

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"1.2.3", "abcdef0", "darwin", "arm64"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output %q missing %q", out, want)
		}
	}
	// Human output must not be JSON.
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("human output unexpectedly looks like JSON: %q", out)
	}
}

func TestVersionJSONEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"version", "--json"}, &stdout, &stderr, testBuild())

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}

	// Exactly one JSON document on stdout: decode one, then require io.EOF.
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v; got %q", err, stdout.String())
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document (io.EOF on 2nd decode), got %v; stdout=%q", err, stdout.String())
	}

	if env["schema_version"] != "1" {
		t.Errorf("schema_version = %v, want \"1\"", env["schema_version"])
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	if env["command"] != "version" {
		t.Errorf("command = %v, want \"version\"", env["command"])
	}
	if env["error"] != nil {
		t.Errorf("error = %v, want null", env["error"])
	}
	// warnings must be an empty array, not null.
	warnings, ok := env["warnings"].([]any)
	if !ok {
		t.Fatalf("warnings is not an array: %T (%v)", env["warnings"], env["warnings"])
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want empty", warnings)
	}

	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %T (%v)", env["data"], env["data"])
	}
	for k, want := range map[string]any{
		"version":    "1.2.3",
		"commit":     "abcdef0",
		"build_date": "2026-07-24T00:00:00Z",
		"go_version": "go1.26.5",
		"os":         "darwin",
		"arch":       "arm64",
	} {
		if data[k] != want {
			t.Errorf("data[%q] = %v, want %v", k, data[k], want)
		}
	}
}
