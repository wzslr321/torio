package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateRegistrySerializesReadModifyWrite(t *testing.T) {
	paths, err := ResolvePaths(Options{Getenv: envOnly("XDG_CONFIG_HOME", t.TempDir())})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	release := make(chan struct{})
	errs := make(chan error, 2)

	go func() {
		errs <- UpdateRegistry(paths, func(projects []Project) ([]Project, error) {
			close(firstEntered)
			<-release
			return append(projects, Project{ID: "first", DisplayName: "First", Remote: "git@github.com:owner/first.git"}), nil
		})
	}()
	<-firstEntered
	go func() {
		errs <- UpdateRegistry(paths, func(projects []Project) ([]Project, error) {
			close(secondEntered)
			return append(projects, Project{ID: "second", DisplayName: "Second", Remote: "git@github.com:owner/second.git"}), nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second update entered before the first read-modify-write finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("UpdateRegistry: %v", err)
		}
	}
	projects, err := ResolveRegistry(paths)
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %#v, want both updates", projects)
	}
}

// registryPath returns a path for a registry document inside a trusted config
// directory. The directory is mode-private because that is a precondition the
// loader enforces, not an incidental detail of the fixture.
func registryPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), appDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return filepath.Join(dir, registryFileName)
}

func writeRegistryDoc(t *testing.T, body string) string {
	t.Helper()
	path := registryPath(t)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return path
}

// TestLoadRegistryTellsAbsentFromEmpty is the distinction the whole migration
// rests on. An installation that predates the shared registry has no document,
// and reading that as "no projects" rather than "not migrated" would make an
// upgrade look like someone had detached every project.
func TestLoadRegistryTellsAbsentFromEmpty(t *testing.T) {
	absent := registryPath(t)
	got, present, err := LoadRegistry(absent)
	if err != nil {
		t.Fatalf("LoadRegistry on an absent document: %v", err)
	}
	if present {
		t.Error("an absent registry reported itself present")
	}
	if len(got) != 0 {
		t.Errorf("an absent registry returned %d projects", len(got))
	}

	empty := writeRegistryDoc(t, `{"schema_version":"1","projects":[]}`)
	got, present, err = LoadRegistry(empty)
	if err != nil {
		t.Fatalf("LoadRegistry on an empty document: %v", err)
	}
	if !present {
		t.Error("a written registry with no projects reported itself absent")
	}
	if len(got) != 0 {
		t.Errorf("an empty registry returned %d projects", len(got))
	}
}

// TestWriteRegistryRoundTripsSorted pins that the same set of projects always
// produces the same bytes, so attaching in a different order does not show up
// as a diff in a file the operator may keep under version control.
func TestWriteRegistryRoundTrips(t *testing.T) {
	path := registryPath(t)
	in := []Project{
		{ID: "zeta", DisplayName: "Zeta", Remote: "git@github.com:wzslr321/zeta.git"},
		{ID: "alpha", DisplayName: "Alpha", Remote: "https://github.com/wzslr321/alpha"},
	}
	if err := WriteRegistry(path, in); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	got, present, err := LoadRegistry(path)
	if err != nil || !present {
		t.Fatalf("LoadRegistry: %v (present=%v)", err, present)
	}
	if len(got) != 2 || got[0].ID != "alpha" || got[1].ID != "zeta" {
		t.Fatalf("registry did not round-trip sorted: %+v", got)
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []Project{in[1], in[0]}
	if err := WriteRegistry(path, reversed); err != nil {
		t.Fatalf("WriteRegistry (reversed): %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("the same projects in a different order produced different bytes")
	}
}

// TestWriteRegistryWritesModePrivate pins that the registry is created under the
// same permission policy as the config document. It holds remotes, which name
// every repository the operator has attached.
func TestWriteRegistryWritesModePrivate(t *testing.T) {
	path := registryPath(t)
	if err := WriteRegistry(path, []Project{{ID: "demo", DisplayName: "Demo", Remote: "git@github.com:wzslr321/demo.git"}}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("registry permissions %#o allow group/other access", perm)
	}
}

// TestLoadRegistryRejectsMalformedDocuments pins that the registry fails closed
// on everything the config document does. It is read on every invocation and is
// operator-authored, so it gets the same strict decoder, the same version gate
// and the same secret-shape refusal — not a looser one because it is "just a
// list".
func TestLoadRegistryRejectsMalformedDocuments(t *testing.T) {
	for name, body := range map[string]string{
		"unknown schema":       `{"schema_version":"2","projects":[]}`,
		"missing schema":       `{"projects":[]}`,
		"unknown field":        `{"schema_version":"1","projects":[],"backend":"hermes"}`,
		"smuggled path":        `{"schema_version":"1","projects":[{"id":"a","display_name":"A","remote":"git@h:a.git","path":"/etc"}]}`,
		"invalid id":           `{"schema_version":"1","projects":[{"id":"../etc","display_name":"A","remote":"git@h:a.git"}]}`,
		"duplicate id":         `{"schema_version":"1","projects":[{"id":"a","display_name":"A","remote":"git@h:a.git"},{"id":"a","display_name":"B","remote":"git@h:b.git"}]}`,
		"credential in remote": `{"schema_version":"1","projects":[{"id":"a","display_name":"A","remote":"https://x-access-token:ghp_00000000000000000000000000000000000000@github.com/o/r"}]}`,
		"trailing data":        `{"schema_version":"1","projects":[]}{}`,
		"not json":             `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeRegistryDoc(t, body)
			if _, _, err := LoadRegistry(path); err == nil {
				t.Fatal("malformed registry document was accepted")
			}
		})
	}
}

// TestRegistryErrorsCarryNoSecret pins the package boundary: a document that
// smuggled a token in must not have it handed back in the diagnostic that
// rejected it.
func TestRegistryErrorsCarryNoSecret(t *testing.T) {
	const token = "ghp_00000000000000000000000000000000000000"
	path := writeRegistryDoc(t,
		`{"schema_version":"1","projects":[{"id":"a","display_name":"A","remote":"https://x-access-token:`+token+`@github.com/o/r"}]}`)
	_, _, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("a registry carrying a token was accepted")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error echoed the token: %v", err)
	}
}

// TestWriteRegistryRefusesAnInvalidRegistry pins that validation happens before
// anything is created, so a rejected write leaves no file behind at all.
func TestWriteRegistryRefusesAnInvalidRegistry(t *testing.T) {
	path := registryPath(t)
	err := WriteRegistry(path, []Project{{ID: "Bad Id", DisplayName: "A", Remote: "git@github.com:o/r.git"}})
	if err == nil {
		t.Fatal("an invalid project was persisted")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("a rejected write left a document behind")
	}
}
