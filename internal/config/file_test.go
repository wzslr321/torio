package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// loadWith resolves+loads config with a temp XDG_CONFIG_HOME so no real
// user config is touched. It returns the Runtime and any error.
func loadWith(t *testing.T, opts Options, cfgHome, stateHome string) (Runtime, error) {
	t.Helper()
	env := map[string]string{}
	if cfgHome != "" {
		env["XDG_CONFIG_HOME"] = cfgHome
	}
	if stateHome != "" {
		env["XDG_STATE_HOME"] = stateHome
	}
	opts.Getenv = envFunc(env)
	if opts.HomeDir == nil {
		opts.HomeDir = homeFunc(t.TempDir())
	}
	return Load(opts)
}

func writeConfig(t *testing.T, cfgHome, body string) string {
	t.Helper()
	dir := filepath.Join(cfgHome, appDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadAbsentDefaultConfigIsValidFirstRun(t *testing.T) {
	rt, err := loadWith(t, Options{}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("absent default config must be valid, got %v", err)
	}
	if rt.ConfigLoaded {
		t.Errorf("ConfigLoaded = true, want false for absent default config")
	}
	if rt.File.Timeout != 0 {
		t.Errorf("absent config Timeout = %v, want 0 (unset)", rt.File.Timeout)
	}
}

func TestLoadExplicitMissingConfigIsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	_, err := loadWith(t, Options{ConfigPath: missing}, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatalf("explicit --config to a missing file must error")
	}
}

func TestLoadValidConfigParsesFields(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"1","default_timeout":"45s"}`)
	rt, err := loadWith(t, Options{}, cfgHome, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !rt.ConfigLoaded {
		t.Errorf("ConfigLoaded = false, want true")
	}
	if rt.File.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s", rt.File.Timeout)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{not json`)
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("malformed JSON must be rejected")
	}
}

func TestLoadRejectsWrongSchemaVersion(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2"}`)
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("unknown schema_version must be rejected")
	}
}

func TestLoadRejectsMissingSchemaVersion(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"default_timeout":"10s"}`)
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("missing schema_version must be rejected")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"1","surprise":true}`)
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("unknown field must be rejected (fail closed)")
	}
}

func TestLoadRejectsSemanticallyInvalidTimeout(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"1","default_timeout":"-5s"}`,
		`{"schema_version":"1","default_timeout":"999h"}`,
		`{"schema_version":"1","default_timeout":"not-a-duration"}`,
		`{"schema_version":"1","default_timeout":"0s"}`,
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, body)
		if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
			t.Errorf("semantically invalid config %q must be rejected", body)
		}
	}
}

// TestLoadRejectsTrailingBytesAndSecondDocument locks strict single-document
// parsing: exactly one top-level JSON value, no trailing bytes. This covers the
// cases a bare Decoder.More() check misses — a trailing closing delimiter can
// make More() report false despite invalid remaining bytes.
func TestLoadRejectsTrailingBytesAndSecondDocument(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"1"}}`,                      // trailing }
		`{"schema_version":"1"}]`,                      // trailing ]
		`{"schema_version":"1"} trailing`,              // trailing garbage
		`{"schema_version":"1"}{"schema_version":"1"}`, // second document
	} {
		cfgHome := t.TempDir()
		writeConfig(t, cfgHome, body)
		if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
			t.Errorf("body %q must be rejected (exactly one JSON document)", body)
		}
	}
}

// secretCanary is a synthetic, matcher-valid fake GitHub token: it is not a
// real credential but DOES match the production redactor's gh[pousr]_ shape
// (24 alphanumerics after the prefix, so it satisfies the {20,} quantifier).
// Using a matcher-valid fixture is what makes the secret-rejection tests
// meaningful — see TestSecretCanaryIsRecognizedByProductionMatcher.
const secretCanary = "ghp_ABCDEFGHIJKLMNOPQRSTUVWX"

// TestSecretCanaryIsRecognizedByProductionMatcher guards the fixture: if the
// canary did not actually match the production redactor, the rejection tests
// below could pass for the wrong reason (e.g. duration validation) and the
// secret-rejection evidence would be false. This asserts the canary is real.
func TestSecretCanaryIsRecognizedByProductionMatcher(t *testing.T) {
	if !containsSecretShape(secretCanary) {
		t.Fatalf("canary %q is not recognized by the production redactor; fixture is invalid", secretCanary)
	}
}

// TestLoadRejectsSecretShapedValueWithoutLeaking proves config refuses
// secret-shaped material specifically via the secret detector (not incidental
// duration validation), and that the error text never echoes the secret.
func TestLoadRejectsSecretShapedValueWithoutLeaking(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"1","default_timeout":"`+secretCanary+`"}`)
	_, err := loadWith(t, Options{}, cfgHome, t.TempDir())
	if err == nil {
		t.Fatalf("secret-shaped config value must be rejected")
	}
	// The rejection must be attributed to the secret detector, proving it is the
	// secret shape — not some other field validation — that fails closed.
	if !strings.Contains(err.Error(), "secret-shaped") {
		t.Errorf("rejection reason is not the secret detector: %q", err.Error())
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("error leaked the secret-shaped value: %q", err.Error())
	}
}

// escapedSecretRaw is the JSON-escaped wire form of secretCanary: the `h` in
// the gh[pousr]_ prefix is written as the h escape. On disk the raw bytes
// therefore contain no literal "ghp_", so a raw-byte pre-scan cannot see it —
// but encoding/json decodes it back into the exact matcher-valid secret. This
// is the escaping bypass the JSON-escaped no-leak tests exercise.
const escapedSecretRaw = "g\\u0068p_ABCDEFGHIJKLMNOPQRSTUVWX"

// TestEscapedSecretFixtureIsAGenuineBypass guards the escaped fixture: it must
// (a) NOT be caught by the raw-byte pre-scan in its on-disk form, yet (b) decode
// to the exact matcher-valid secret. If either invariant breaks, the escaped
// no-leak tests below could pass for the wrong reason.
func TestEscapedSecretFixtureIsAGenuineBypass(t *testing.T) {
	if containsSecretShape(escapedSecretRaw) {
		t.Fatalf("escaped fixture %q must NOT match the raw pre-scan (else it is not an escaping bypass)", escapedSecretRaw)
	}
	var got string
	if err := json.Unmarshal([]byte(`"`+escapedSecretRaw+`"`), &got); err != nil {
		t.Fatalf("unmarshal escaped fixture: %v", err)
	}
	if got != secretCanary {
		t.Fatalf("escaped fixture decodes to %q, want the canary %q", got, secretCanary)
	}
}

// TestLoadDoesNotLeakJSONEscapedSecretInAnyField proves the config API's own
// no-leak contract holds even when a matcher-valid secret is JSON-escaped so the
// raw pre-scan cannot see it. The decoder turns it back into a secret that could
// otherwise reach an error via %q interpolation (schema_version) or the
// DisallowUnknownFields decoder text (an escaped unknown field name). Every
// textual decoded surface is covered; the assertion is on the error returned by
// the config package itself, not the final CLI renderer.
func TestLoadDoesNotLeakJSONEscapedSecretInAnyField(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"schema_version", `{"schema_version":"` + escapedSecretRaw + `"}`},
		{"default_timeout", `{"schema_version":"1","default_timeout":"` + escapedSecretRaw + `"}`},
		{"unknown_field_name", `{"schema_version":"1","` + escapedSecretRaw + `":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgHome := t.TempDir()
			writeConfig(t, cfgHome, tc.body)
			_, err := loadWith(t, Options{}, cfgHome, t.TempDir())
			if err == nil {
				t.Fatalf("JSON-escaped secret in %s must be rejected", tc.name)
			}
			if strings.Contains(err.Error(), secretCanary) {
				t.Errorf("config API error leaked JSON-escaped secret via %s: %q", tc.name, err.Error())
			}
		})
	}
}

func TestLoadRejectsInsecureConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission enforcement is Unix-only")
	}
	cfgHome := t.TempDir()
	path := writeConfig(t, cfgHome, `{"schema_version":"1"}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("group/world-readable config must be rejected on Unix")
	}
}

func TestLoadRejectsInsecureExistingStateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission enforcement is Unix-only")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := loadWith(t, Options{StateDir: stateDir}, t.TempDir(), "")
	if err == nil {
		t.Fatalf("insecure existing --state-dir must be rejected on Unix")
	}
}
