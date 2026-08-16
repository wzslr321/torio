package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadV2DocumentDeclaresNoBackend pins the compatibility rule that decides
// what happens to every box that already exists. A document written before
// instances declared a backend names none, and none must mean the default —
// anything else re-points a working box at a different agent on upgrade.
func TestLoadV2DocumentDeclaresNoBackend(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","projects":[]}`)

	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rt.File.Backend != "" {
		t.Errorf("Backend = %q, want empty for a document that predates the field", rt.File.Backend)
	}
	if rt.File.SchemaVersion != ConfigSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q: reading an old document normalizes it",
			rt.File.SchemaVersion, ConfigSchemaVersion)
	}
}

// TestLoadV2DocumentCarryingBackendIsRejected pins that a document must mean
// what its declared version says it means. A "2" document with a backend field
// was written by something that did not understand either version, and reading
// it either way would be a guess.
func TestLoadV2DocumentCarryingBackendIsRejected(t *testing.T) {
	cfgHome := t.TempDir()
	writeConfig(t, cfgHome, `{"schema_version":"2","backend":"claude-code","projects":[]}`)

	if _, err := loadWith(t, Options{}, cfgHome); err == nil {
		t.Fatal("a v2 document carrying a backend field must be rejected")
	}
}

// TestBackendSurvivesAWriteReadRound proves the declaration persists, since an
// instance's backend is fixed at creation and every later command depends on
// reading back the one that was chosen.
func TestBackendSurvivesAWriteReadRound(t *testing.T) {
	cfgHome := t.TempDir()
	path := filepath.Join(cfgHome, appDir, configFileName)

	want := File{SchemaVersion: ConfigSchemaVersion, Backend: "claude-code"}
	if err := WriteFile(path, want); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rt, err := loadWith(t, Options{}, cfgHome)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rt.File.Backend != "claude-code" {
		t.Fatalf("Backend = %q, want %q", rt.File.Backend, "claude-code")
	}
}

// TestValidateRejectsAMalformedBackendName pins that the name is constrained
// where it is read. It is an identifier from a document Torio echoes back in
// errors, so it gets the same treatment as a project id — whether a well-shaped
// name is one this binary implements is a question for the backend registry,
// not for the config layer.
func TestValidateRejectsAMalformedBackendName(t *testing.T) {
	for _, name := range []string{
		"-leading-dash",
		"trailing-dash-",
		"Upper",
		"has space",
		"has/slash",
		strings.Repeat("a", 33),
	} {
		err := File{SchemaVersion: ConfigSchemaVersion, Backend: name}.Validate()
		if err == nil {
			t.Errorf("Validate accepted malformed backend name %q", name)
		}
	}
	for _, name := range []string{"codex", "claude-code", "a"} {
		if err := (File{SchemaVersion: ConfigSchemaVersion, Backend: name}).Validate(); err != nil {
			t.Errorf("Validate rejected valid backend name %q: %v", name, err)
		}
	}
}
