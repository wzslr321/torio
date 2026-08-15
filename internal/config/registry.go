package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// RegistrySchemaVersion is the version this build writes. Like the config
// document, an unknown version is rejected rather than migrated.
//
// Version 2 admits a project with no remote (ADR-0027). The bump exists for
// the older binary's benefit, not this one's: a V1 validator refuses an empty
// remote outright, so without a version it would report a registry holding one
// local project as an invalid document rather than as one written by a Torio
// it does not know.
const RegistrySchemaVersion = "2"

// readableRegistryVersions are the versions this build can read. A registry
// written before local projects existed is still exactly right, so it is read
// as it is; the next write stamps the current version.
var readableRegistryVersions = []string{"1", "2"}

// registryJSON is the wire form of the registry document. Unknown fields are
// rejected at every level, so the schema fails closed — including a project
// object that tries to smuggle in a workspace path, which is derived from the
// backend and must never come off disk (ADR-0003).
type registryJSON struct {
	SchemaVersion string        `json:"schema_version"`
	Projects      []projectJSON `json:"projects"`
}

func loadRegistry(path string, checkParent bool) (_ []Project, _ bool, err error) {
	defer func() { err = redactErr(err) }()

	if checkParent {
		if err := statTrustedDirIfExists(filepath.Dir(path)); err != nil {
			return nil, false, err
		}
	}

	f, err := openTrustedFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read project registry: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, fmt.Errorf("read project registry: %w", err)
	}
	projects, err := parseRegistry(data)
	if err != nil {
		return nil, false, fmt.Errorf("project registry %s: %w", path, err)
	}
	return projects, true, nil
}

// ResolveRegistry returns the projects this invocation sees, from whichever
// document currently holds them.
//
// The shared registry wins whenever it exists. Until it does, the projects come
// from the *default* instance's document — not the selected instance's — because
// that is where the one registry lived before there was a shared one. Reading
// the selected instance's array instead would empty the registry the moment an
// operator first passed --backend, which is exactly the invocation this whole
// change exists to make ordinary.
//
// A non-default instance's own legacy array is therefore not carried over. Those
// belonged to a separate registry per instance, which is the thing being
// abolished; merging two registries is not a decision this function can make
// safely, and the file is left untouched either way.
func ResolveRegistry(paths Paths) ([]Project, error) {
	shared, present, err := loadRegistry(paths.RegistryFile, !paths.explicitConfig)
	if err != nil {
		return nil, err
	}
	if present {
		return shared, nil
	}
	return legacyProjects(paths.RootConfigFile)
}

// UpdateRegistry applies update while holding the advisory lock for the shared
// registry directory. The lock spans reading, changing, writing and verifying
// the document, so concurrent project mutations cannot overwrite one another.
func UpdateRegistry(paths Paths, update func([]Project) ([]Project, error)) error {
	return withRegistryLock(paths.RegistryFile, func() error {
		projects, err := ResolveRegistry(paths)
		if err != nil {
			return err
		}
		next, err := update(slices.Clone(projects))
		if err != nil {
			return err
		}
		if slices.Equal(next, projects) {
			return nil
		}
		return WriteRegistry(paths.RegistryFile, next)
	})
}

// legacyProjects reads the projects out of a config document at an explicit
// path, treating an absent document as no projects. It is the pre-shared-
// registry read path and has no other caller.
func legacyProjects(path string) (_ []Project, err error) {
	defer func() { err = redactErr(err) }()

	f, err := openTrustedFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	doc, err := parseFile(data)
	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	return doc.Projects, nil
}

// WriteRegistry validates the registry and persists it through the same
// crash-safe path the config document uses: private temp, fsync, atomic
// rename, read back and verify.
//
// Entries are sorted by ID before marshalling, so the same set of projects
// always produces the same bytes regardless of the order they were attached in.
func WriteRegistry(path string, projects []Project) (err error) {
	return writeRegistry(path, projects, true)
}

// WriteRegistryForPaths persists the registry at paths.RegistryFile using the
// same parent-directory trust rule as ResolveRegistry.
func WriteRegistryForPaths(paths Paths, projects []Project) error {
	return writeRegistry(paths.RegistryFile, projects, !paths.explicitConfig)
}

func writeRegistry(path string, projects []Project, checkParent bool) (err error) {
	defer func() { err = redactErr(err) }()

	out := slices.Clone(projects)
	slices.SortFunc(out, func(a, b Project) int { return strings.Compare(a.ID, b.ID) })
	if err := validateProjects(out); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	wire := registryJSON{SchemaVersion: RegistrySchemaVersion, Projects: []projectJSON{}}
	for _, p := range out {
		wire.Projects = append(wire.Projects, projectJSON(p))
	}
	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal project registry: %w", err)
	}
	data = append(data, '\n')

	if checkParent {
		if err := statTrustedDirIfExists(filepath.Dir(path)); err != nil {
			return fmt.Errorf("config: trusted directory: %w", err)
		}
	}
	if err := writeFilePrivate(path, data); err != nil {
		return err
	}
	return verifyPersistedRegistry(path, out, checkParent)
}

// verifyPersistedRegistry re-reads the document through the trusted read path
// and checks it is the one want describes. Validating in memory proves what we
// meant to write, not what a concurrent writer or a failing filesystem left
// behind.
//
// A mismatch is reported, not repaired: the rename already happened, so the
// operator decides what the file should contain.
func verifyPersistedRegistry(path string, want []Project, checkParent bool) error {
	got, present, err := loadRegistry(path, checkParent)
	if err != nil {
		return fmt.Errorf("config: read back written project registry: %w", err)
	}
	if !present {
		return errors.New("config: written project registry is not there on read back")
	}
	if !slices.Equal(got, want) {
		// Neither document is echoed: both carry operator-controlled text.
		return errors.New("config: written project registry does not match the document that was persisted")
	}
	return nil
}

// parseRegistry decodes and strictly validates a registry document. The
// declared version is checked before the decode, so an unsupported document is
// rejected by version rather than by whichever field happens to be unknown to
// the one wire form we have.
func parseRegistry(data []byte) (_ []Project, err error) {
	defer func() { err = redactErr(err) }()

	if containsSecretShape(string(data)) {
		return nil, errors.New("contains secret-shaped material; config must be non-secret")
	}

	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if !slices.Contains(readableRegistryVersions, probe.SchemaVersion) {
		return nil, fmt.Errorf("schema_version %q is not supported (want one of %s)",
			probe.SchemaVersion, strings.Join(readableRegistryVersions, ", "))
	}

	var raw registryJSON
	if err := decodeStrict(data, &raw); err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(raw.Projects))
	for _, rp := range raw.Projects {
		projects = append(projects, Project(rp))
	}
	if err := validateProjects(projects); err != nil {
		return nil, err
	}
	return projects, nil
}
