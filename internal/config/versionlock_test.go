package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// privDir returns a freshly created, mode-private (0700) directory owned by the
// test's effective user — a genuinely trusted directory in which a manifest may
// live. It exists because t.TempDir() is 0755 (the testing package creates the
// numbered leaf with 0777&^umask), which the trusted-directory policy correctly
// rejects; tests that exercise manifest parsing must supply a private parent.
func privDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "hermes-box")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return d
}

func TestVersionLockWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes-box", versionLockFileName)
	in := VersionLock{
		SchemaVersion: VersionLockSchemaVersion,
		Lima:          "1.0.1",
		Docker:        "27.3.1",
		Hermes:        "0.4.2",
	}
	if err := WriteVersionLock(path, in); err != nil {
		t.Fatalf("WriteVersionLock: %v", err)
	}
	got, err := LoadVersionLock(path)
	if err != nil {
		t.Fatalf("LoadVersionLock: %v", err)
	}
	if got != in {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, in)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("version-lock perm = %o, want owner-only", fi.Mode().Perm())
		}
	}
}

func TestWriteVersionLockRejectsInvalidBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hermes-box", versionLockFileName)
	bad := VersionLock{SchemaVersion: "9", Lima: "1.0.0"}
	if err := WriteVersionLock(path, bad); err == nil {
		t.Fatalf("WriteVersionLock must reject invalid manifest")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("no file must be created for an invalid manifest, stat err = %v", err)
	}
}

func TestLoadVersionLockRejectsUnknownField(t *testing.T) {
	path := filepath.Join(privDir(t), versionLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","extra":"x"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadVersionLock(path); err == nil {
		t.Fatalf("unknown field must be rejected")
	}
}

func TestLoadVersionLockRejectsWrongSchemaVersion(t *testing.T) {
	path := filepath.Join(privDir(t), versionLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"2"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadVersionLock(path); err == nil {
		t.Fatalf("wrong schema_version must be rejected")
	}
}

func TestLoadVersionLockRejectsMalformedVersionValue(t *testing.T) {
	path := filepath.Join(privDir(t), versionLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","lima":"has space"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadVersionLock(path); err == nil {
		t.Fatalf("malformed version value must be rejected")
	}
}

func TestLoadVersionLockRejectsSecretShapedWithoutLeaking(t *testing.T) {
	// secretCanary (defined in file_test.go) is matcher-valid; its recognition
	// by the production redactor is asserted in
	// TestSecretCanaryIsRecognizedByProductionMatcher.
	path := filepath.Join(privDir(t), versionLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","hermes":"`+secretCanary+`"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadVersionLock(path)
	if err == nil {
		t.Fatalf("secret-shaped version must be rejected")
	}
	if !strings.Contains(err.Error(), "secret-shaped") {
		t.Errorf("rejection reason is not the secret detector: %q", err.Error())
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("error leaked secret-shaped value: %q", err.Error())
	}
}

// TestLoadVersionLockDoesNotLeakJSONEscapedSecretInAnyField mirrors the config
// test: a matcher-valid secret written in JSON-escaped form (escapedSecretRaw,
// defined in file_test.go) bypasses the raw pre-scan but decodes back into a
// secret. It must never appear in the manifest API's own error text, whether via
// schema_version %q interpolation, a decoded tool-pin value, or an escaped
// unknown field name surfaced by the strict decoder.
func TestLoadVersionLockDoesNotLeakJSONEscapedSecretInAnyField(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"schema_version", `{"schema_version":"` + escapedSecretRaw + `"}`},
		{"lima", `{"schema_version":"1","lima":"` + escapedSecretRaw + `"}`},
		{"docker", `{"schema_version":"1","docker":"` + escapedSecretRaw + `"}`},
		{"hermes", `{"schema_version":"1","hermes":"` + escapedSecretRaw + `"}`},
		{"unknown_field_name", `{"schema_version":"1","` + escapedSecretRaw + `":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(privDir(t), versionLockFileName)
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := LoadVersionLock(path)
			if err == nil {
				t.Fatalf("JSON-escaped secret in %s must be rejected", tc.name)
			}
			if strings.Contains(err.Error(), secretCanary) {
				t.Errorf("version-lock API error leaked JSON-escaped secret via %s: %q", tc.name, err.Error())
			}
		})
	}
}

func TestLoadVersionLockRejectsInsecurePermissions(t *testing.T) {
	requireTrustPolicy(t)
	path := filepath.Join(privDir(t), versionLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadVersionLock(path); err == nil {
		t.Fatalf("group/world-readable version-lock must be rejected on Unix")
	}
}

// TestLoadVersionLockRejectsTrailingBytesAndSecondDocument locks strict
// single-document parsing for the manifest, including the trailing closing
// delimiter case a bare Decoder.More() check misses.
func TestLoadVersionLockRejectsTrailingBytesAndSecondDocument(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":"1"}}`,
		`{"schema_version":"1"}]`,
		`{"schema_version":"1"} trailing`,
		`{"schema_version":"1"}{"schema_version":"1"}`,
	} {
		path := filepath.Join(privDir(t), versionLockFileName)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadVersionLock(path); err == nil {
			t.Errorf("body %q must be rejected (exactly one JSON document)", body)
		}
	}
}

func TestLoadVersionLockAllowsAbsentToolPins(t *testing.T) {
	path := filepath.Join(privDir(t), versionLockFileName)
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadVersionLock(path)
	if err != nil {
		t.Fatalf("LoadVersionLock: %v", err)
	}
	if got.Lima != "" || got.Docker != "" || got.Hermes != "" {
		t.Errorf("absent tool pins should stay empty, got %+v", got)
	}
}
