package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/config"
)

// configHome builds a mode-private XDG config home and returns an env stub for
// it. Nothing here touches the process environment, so these tests say nothing
// about the machine they run on.
func configHome(t *testing.T) (string, func(string) string) {
	t.Helper()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "torio"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return base, func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return base
		}
		return ""
	}
}

func writeInstanceConfig(t *testing.T, base, instance, body string) {
	t.Helper()
	dir := filepath.Join(base, "torio")
	if instance != "" {
		dir = filepath.Join(dir, "instances", instance)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

const legacyDocument = `{"schema_version":"3","backend":"codex","projects":[` +
	`{"id":"legacy","display_name":"Legacy","remote":"git@github.com:wzslr321/legacy.git"}]}`

// TestRegistryReadsTheLegacyArrayUntilItIsMigrated pins the upgrade path. An
// installation that predates the shared registry has projects in its instance
// document and no registry document at all; reading that as an empty registry
// would make an upgrade look like someone had detached everything.
func TestRegistryReadsTheLegacyArrayUntilItIsMigrated(t *testing.T) {
	base, env := configHome(t)
	writeInstanceConfig(t, base, "", legacyDocument)

	r := FileRegistry{Options: config.Options{Getenv: env}}
	got, err := r.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "legacy" {
		t.Fatalf("legacy projects were not read: %+v", got.Projects)
	}
}

// TestTheLegacyArrayIsReadFromTheDefaultInstance is the regression that the
// unit tests missed and a real binary found in one command. The projects of an
// unmigrated installation live in the *default* instance's document; reading
// the selected instance's instead empties the registry the moment an operator
// first passes --backend — which is the invocation this whole change exists to
// make ordinary.
func TestTheLegacyArrayIsReadFromTheDefaultInstance(t *testing.T) {
	base, env := configHome(t)
	writeInstanceConfig(t, base, "", legacyDocument)

	r := FileRegistry{Options: config.Options{Getenv: env, Instance: "torio-claude-code"}}
	got, err := r.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "legacy" {
		t.Fatalf("a routed instance lost the unmigrated registry: %+v", got.Projects)
	}
}

// TestTheFirstWriteMigratesWithoutDeleting pins that migration is a write of
// the new document, not a rewrite of the old one. The legacy array is left
// where it is: downgrading Torio has to find its projects exactly where it left
// them, and reversing the migration must be deleting one file rather than
// recovering from a backup nobody made.
func TestTheFirstWriteMigratesWithoutDeleting(t *testing.T) {
	base, env := configHome(t)
	writeInstanceConfig(t, base, "", legacyDocument)
	r := FileRegistry{Options: config.Options{Getenv: env}}

	current, err := r.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	next, err := current.WithProject(
		config.Project{ID: "added", DisplayName: "Added", Remote: "git@github.com:wzslr321/added.git"},
		config.AddOptions{})
	if err != nil {
		t.Fatalf("WithProject: %v", err)
	}
	if err := r.Save(next); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after, err := r.Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if len(after.Projects) != 2 {
		t.Fatalf("registry holds %d projects after the migration, want 2: %+v", len(after.Projects), after.Projects)
	}

	raw, err := os.ReadFile(filepath.Join(base, "torio", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"legacy"`) {
		t.Error("the migration deleted the legacy array instead of leaving it")
	}
	if strings.Contains(string(raw), `"added"`) {
		t.Error("a registry write reached the instance document, which it does not own")
	}
}

// TestTheRegistryDocumentWinsOverTheLegacyArray pins which one is authoritative
// once both exist. They will both exist for as long as the operator keeps the
// legacy bytes, so "whichever we happen to read last" is not an answer.
func TestTheRegistryDocumentWinsOverTheLegacyArray(t *testing.T) {
	base, env := configHome(t)
	writeInstanceConfig(t, base, "", legacyDocument)
	if err := config.WriteRegistry(filepath.Join(base, "torio", "projects.json"),
		[]config.Project{{ID: "shared", DisplayName: "Shared", Remote: "git@github.com:wzslr321/shared.git"}}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	r := FileRegistry{Options: config.Options{Getenv: env}}
	got, err := r.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "shared" {
		t.Fatalf("the legacy array won over the registry document: %+v", got.Projects)
	}
}

// TestBothInstancesSeeOneRegistry is the point of the change: a project
// attached while talking to one backend's box is on record for the other, so
// materializing it there is `project add <id> --backend <other>` rather than
// retyping a remote into a second registry that can drift from the first.
func TestBothInstancesSeeOneRegistry(t *testing.T) {
	base, env := configHome(t)
	writeInstanceConfig(t, base, "", `{"schema_version":"3","backend":"codex","projects":[]}`)
	writeInstanceConfig(t, base, "torio-claude-code", `{"schema_version":"3","backend":"claude-code","projects":[]}`)

	legacy := FileRegistry{Options: config.Options{Getenv: env}}
	claude := FileRegistry{Options: config.Options{Getenv: env, Instance: "torio-claude-code"}}

	current, err := legacy.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	next, err := current.WithProject(
		config.Project{ID: "demo", DisplayName: "Demo", Remote: "git@github.com:wzslr321/demo.git"},
		config.AddOptions{})
	if err != nil {
		t.Fatalf("WithProject: %v", err)
	}
	if err := legacy.Save(next); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := claude.Load()
	if err != nil {
		t.Fatalf("Load from the other instance: %v", err)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "demo" {
		t.Fatalf("the other instance does not see the project: %+v", got.Projects)
	}
	if got.Backend != "claude-code" {
		t.Errorf("backend = %q; the shared registry leaked the other instance's declaration", got.Backend)
	}
}
