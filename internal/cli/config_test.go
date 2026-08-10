package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/redact"
)

// runVersionWithXDG runs `torio <args>` with isolated XDG dirs so no real user
// config is touched, and returns exit code + captured streams.
func runVersionWithXDG(t *testing.T, args []string, cfgHome string) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr, testBuild())
	return code, stdout.String(), stderr.String()
}

func writeCLIConfig(t *testing.T, cfgHome, body string) {
	t.Helper()
	dir := filepath.Join(cfgHome, "torio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestConfigGlobalAcceptedBeforeAndAfterCommand replaces the D1-era rejection:
// --config is a real persistent global in D2, usable both before and after the
// subcommand.
func TestConfigGlobalAcceptedBeforeAndAfterCommand(t *testing.T) {
	cfgHome := t.TempDir()
	cfg := filepath.Join(cfgHome, "explicit.json")
	if err := os.WriteFile(cfg, []byte(`{"schema_version":"2"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, args := range [][]string{
		{"--config", cfg, "version", "--json"},
		{"version", "--config", cfg, "--json"},
	} {
		t.Setenv("XDG_CONFIG_HOME", cfgHome)
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

// TestConfigDefaultTimeoutIsConsumed proves command dispatch replaces the
// built-in timeout with the configured value.
func TestConfigDefaultTimeoutIsConsumed(t *testing.T) {
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"137ms"}`)
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	var stdout, stderr bytes.Buffer
	a := &app{stdout: &stdout, stderr: &stderr, build: testBuild()}
	if code := runWithApp(context.Background(), a, []string{"version"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if want := 137 * time.Millisecond; a.timeout != want {
		t.Fatalf("operation timeout = %s, want configured %s", a.timeout, want)
	}
}

// TestExplicitTimeoutOverridesConfig proves --timeout wins over the config's
// default_timeout: the tiny config timeout is overridden by a valid flag, so
// the command succeeds.
func TestExplicitTimeoutOverridesConfig(t *testing.T) {
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"1ns"}`)
	code, _, stderr := runVersionWithXDG(t, []string{"version", "--timeout", "5s"}, cfgHome)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0 (explicit --timeout must override config); stderr=%q", code, stderr)
	}
}

// TestOverMaxConfigTimeoutIsUsageError proves the CLI surfaces a semantically
// invalid config value (default_timeout above policy max) as a usage/schema
// error (exit 2), not a silent coercion.
func TestOverMaxConfigTimeoutIsUsageError(t *testing.T) {
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"999h"}`)
	code, _, _ := runVersionWithXDG(t, []string{"version"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (over-max config default_timeout)", code, int(ExitUsage))
	}
}

func TestAbsentDefaultConfigStillRunsVersion(t *testing.T) {
	code, out, stderr := runVersionWithXDG(t, []string{"version"}, t.TempDir())
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0 (absent config is valid first-run); stderr=%q", code, stderr)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("stdout %q missing version", out)
	}
}

func TestExplicitMissingConfigIsUsageError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	code, _, _ := runVersionWithXDG(t, []string{"version", "--config", missing}, t.TempDir())
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (explicit missing --config)", code, int(ExitUsage))
	}
}

func TestMalformedConfigIsUsageError(t *testing.T) {
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, `{not json`)
	code, _, _ := runVersionWithXDG(t, []string{"version", "--json"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (malformed config)", code, int(ExitUsage))
	}
}

// TestConfigSecretShapedValueDoesNotLeak proves neither the human nor JSON
// error path echoes secret-shaped config material. The canary is synthetic but
// matcher-valid: it is first asserted to be recognized by the production
// redactor, so the no-leak assertions below are meaningful rather than vacuous.
func TestConfigSecretShapedValueDoesNotLeak(t *testing.T) {
	const canary = "ghp_ABCDEFGHIJKLMNOPQRSTUVWX"
	if redact.String(canary) == canary || !strings.Contains(redact.String(canary), redact.Placeholder) {
		t.Fatalf("canary %q is not recognized by the production redactor; fixture is invalid", canary)
	}
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, `{"schema_version":"2","default_timeout":"`+canary+`"}`)

	code, _, stderr := runVersionWithXDG(t, []string{"version"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("human: exit = %d, want %d", code, int(ExitUsage))
	}
	if strings.Contains(stderr, canary) {
		t.Errorf("human stderr leaked secret-shaped value: %q", stderr)
	}

	// JSON path: exactly one envelope, no leak.
	code, stdout, _ := runVersionWithXDG(t, []string{"version", "--json"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("json: exit = %d, want %d", code, int(ExitUsage))
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout not a JSON document: %v; got %q", err, stdout)
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document, got %v", err)
	}
	if strings.Contains(stdout, canary) {
		t.Errorf("json envelope leaked secret-shaped value: %q", stdout)
	}
}

// TestConfigJSONEscapedSecretDoesNotLeak is the end-to-end defence-in-depth
// counterpart to the config-package escaped-secret tests: a matcher-valid secret
// written JSON-escaped in schema_version (so the raw pre-scan cannot see it, yet
// it decodes back to a secret) must not surface on either the human or --json
// error path.
func TestConfigJSONEscapedSecretDoesNotLeak(t *testing.T) {
	// Decoded form of the escaped wire value below; matcher-valid canary.
	const canary = "ghp_ABCDEFGHIJKLMNOPQRSTUVWX"
	if redact.String(canary) == canary || !strings.Contains(redact.String(canary), redact.Placeholder) {
		t.Fatalf("canary %q is not recognized by the production redactor; fixture is invalid", canary)
	}
	// The `h` is JSON-escaped so the on-disk bytes carry no literal "ghp_".
	const escaped = "g\\u0068p_ABCDEFGHIJKLMNOPQRSTUVWX"
	body := `{"schema_version":"` + escaped + `"}`
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, body)

	code, _, stderr := runVersionWithXDG(t, []string{"version"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("human: exit = %d, want %d", code, int(ExitUsage))
	}
	if strings.Contains(stderr, canary) {
		t.Errorf("human stderr leaked JSON-escaped secret: %q", stderr)
	}

	// JSON path: exactly one envelope, no leak.
	code, stdout, _ := runVersionWithXDG(t, []string{"version", "--json"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("json: exit = %d, want %d", code, int(ExitUsage))
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout not a JSON document: %v; got %q", err, stdout)
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document, got %v", err)
	}
	if strings.Contains(stdout, canary) {
		t.Errorf("json envelope leaked JSON-escaped secret: %q", stdout)
	}
}

func TestInsecureConfigPermissionsIsUsageError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission enforcement is Unix-only")
	}
	cfgHome := t.TempDir()
	writeCLIConfig(t, cfgHome, `{"schema_version":"2"}`)
	if err := os.Chmod(filepath.Join(cfgHome, "torio", "config.json"), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	code, _, _ := runVersionWithXDG(t, []string{"version"}, cfgHome)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (insecure config perms)", code, int(ExitUsage))
	}
}

// TestVersionJSONEnvelopeUnchangedWithConfig locks the D1 envelope discipline:
// even with config resolution active, `torio version --json` emits exactly one
// envelope (second decode is io.EOF).
func TestVersionJSONEnvelopeUnchangedWithConfig(t *testing.T) {
	code, stdout, _ := runVersionWithXDG(t, []string{"version", "--json"}, t.TempDir())
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout not a JSON document: %v", err)
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document (io.EOF), got %v", err)
	}
}
