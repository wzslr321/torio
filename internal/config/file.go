package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ConfigSchemaVersion is the only supported config document version: the
// non-secret project registry schema (ADR-0003). A document declaring any other
// version is rejected rather than migrated.
//
// The settings-only predecessor ("1") is no longer read. Torio never shipped a
// release that wrote one, so no such document exists (ADR-0001). A binary that
// predates the registry still refuses this document — by its own version gate
// and by DisallowUnknownFields on "projects" — so it can never misread a
// registry as settings-only.
const ConfigSchemaVersion = "3"

// readableSchemaVersions are the document versions this binary understands. "2"
// is read but never written: it predates an instance declaring which agent
// backend it runs, and a document that names none is an instance running the
// default one. Reading it as anything else would re-point an existing box at a
// different agent on upgrade, so the absence is given the only meaning it can
// safely have and the document is rewritten as "3" the next time it changes.
//
// The converse is deliberate too. A binary that predates "3" refuses a "3"
// document, by its own version gate and by DisallowUnknownFields on `backend`.
// That is the desired failure: an older binary cannot know its Hermes-shaped
// commands are pointed at a box running a different agent, so it must stop
// rather than guess.
var readableSchemaVersions = []string{"2", "3"}

// readableSchemaVersion reports whether v is a document version this binary
// understands.
func readableSchemaVersion(v string) bool { return slices.Contains(readableSchemaVersions, v) }

// File is the validated, typed content of the on-disk config document. It holds
// only non-secret operator intent: runtime settings plus the active project
// registry. A project's workspace path is deliberately absent — it is derived
// from the project ID, never stored (ADR-0003).
type File struct {
	// SchemaVersion is the document schema version; ConfigSchemaVersion once
	// validated.
	SchemaVersion string
	// Timeout is the parsed default operation timeout, or 0 when the document
	// omits default_timeout. When set it is bounded by policy (see Validate).
	Timeout time.Duration
	// Backend is the agent backend this instance runs, empty for a document
	// that predates the field. Empty means the default backend; which names are
	// valid is resolved by the backend registry, not here — this package
	// validates the shape of the name, never its meaning.
	Backend string
	// Projects is the attached project registry. It is empty for a document
	// without projects.
	Projects []Project
}

// backendNamePattern is the shape a backend name may take. It is an identifier
// in a document Torio reads and echoes back in errors, so it is constrained
// here for the same reason a project id is; whether a well-shaped name is one
// this binary has an implementation for is the caller's question.
var backendNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// fileJSON is the wire form of File. Unknown fields are rejected by the
// decoder (DisallowUnknownFields) at every level, so the schema fails closed —
// including a project object that tries to smuggle in a workspace path.
type fileJSON struct {
	SchemaVersion  string        `json:"schema_version"`
	DefaultTimeout string        `json:"default_timeout,omitempty"`
	Backend        string        `json:"backend,omitempty"`
	Projects       []projectJSON `json:"projects"`
}

// fileJSONV2 is the wire form of the version that predates the backend field.
// It exists as its own type so a document declaring "2" while carrying
// `backend` is rejected by the strict decoder rather than quietly accepted: a
// document must mean what its declared version says it means.
type fileJSONV2 struct {
	SchemaVersion  string        `json:"schema_version"`
	DefaultTimeout string        `json:"default_timeout,omitempty"`
	Projects       []projectJSON `json:"projects"`
}

// projectJSON is the wire form of Project.
type projectJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Remote      string `json:"remote"`
}

// Runtime is the resolved configuration for one invocation: the canonical
// paths, the (possibly defaulted) config document, and whether a document was
// actually read from disk.
type Runtime struct {
	// Paths are the resolved canonical config/state locations.
	Paths Paths
	// File is the validated config; zero-valued defaults when ConfigLoaded is
	// false.
	File File
	// ConfigLoaded reports whether an on-disk config document was read.
	ConfigLoaded bool
}

// Load resolves paths from opts and loads the config document, returning the
// resolved Runtime. Behavior fails closed:
//
//   - An explicit --config path that does not exist is an error.
//   - An absent default config is a valid first run (defaults, ConfigLoaded=false).
//   - Malformed JSON, an unknown schema version, unknown fields, semantically
//     invalid values, or secret-shaped material are all rejected.
//   - On darwin/linux, a symlinked, non-mode-private, non-EUID-owned or
//     non-regular config file, or such an existing config/state directory, is
//     rejected (see ADR-0001); outside darwin/linux this is a documented no-op.
//
// Load performs no writes and does not create directories.
//
// Every returned error is redacted at the package boundary: trust/open/read
// diagnostics interpolate the (possibly caller-controlled) config path, and an
// explicit --config path may itself carry secret-shaped material. redactErr
// leaves a non-secret error untouched — preserving its wrapping chain and
// useful diagnostics — and only flattens a message that would leak a secret
// shape (see redactErr).
func Load(opts Options) (rt Runtime, err error) {
	defer func() { err = redactErr(err) }()

	paths, err := ResolvePaths(opts)
	if err != nil {
		return Runtime{}, err
	}

	rt = Runtime{Paths: paths}

	// For the default (non-explicit) config, ConfigDir is the trusted app
	// directory holding the config document; validate it if it exists. An
	// explicit --config is an operator-provided path whose parent mode is not
	// enforced (ADR-0001 decision 1) — only the file itself is checked.
	if !paths.explicitConfig {
		if err := statTrustedDirIfExists(paths.ConfigDir); err != nil {
			return Runtime{}, err
		}
	}

	// Open the config file without following a final-component symlink and
	// validate type/mode/ownership from the same descriptor before reading, so no
	// substituted object can be read (no TOCTOU on the final component).
	cf, err := openTrustedFile(paths.ConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !paths.explicitConfig {
			// Absent default config: valid first-run default state.
			return rt, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return Runtime{}, fmt.Errorf("config file %s does not exist", paths.ConfigFile)
		}
		return Runtime{}, fmt.Errorf("read config: %w", err)
	}
	defer cf.Close()

	data, err := io.ReadAll(cf)
	if err != nil {
		return Runtime{}, fmt.Errorf("read config: %w", err)
	}

	f, err := parseFile(data)
	if err != nil {
		// The deferred boundary redact scrubs any secret shape this path or the
		// parse error could carry; parseFile also redacts internally.
		return Runtime{}, fmt.Errorf("config file %s: %w", paths.ConfigFile, err)
	}
	rt.File = f
	rt.ConfigLoaded = true
	return rt, nil
}

// WriteFile validates f and persists it crash-safely with owner-only
// permissions, then reads it back and validates it again.
//
// A File that does not declare the current schema version is rejected rather
// than silently upgraded; the mutation helpers (WithProject, WithoutProject)
// set it.
//
// The registry is sorted by ID before marshalling, so the same set of projects
// always produces the same bytes regardless of the order they were added in.
//
// The document is validated before any file is created, and the trusted
// directory before the write: an atomic rename must not be what turns a
// symlinked, mode-permissive or foreign-owned directory into config authority.
// After the rename the file is re-read through the same trusted path the loader
// uses and compared against the intended document, so a write that landed as
// something else is reported instead of trusted.
//
// Every returned error is redacted at the package boundary (redactErr leaves a
// non-secret error untouched).
func WriteFile(path string, f File) (err error) {
	defer func() { err = redactErr(err) }()

	if f.SchemaVersion != ConfigSchemaVersion {
		return fmt.Errorf("config: writes always use schema_version %q, got %q", ConfigSchemaVersion, f.SchemaVersion)
	}
	out := f
	out.Projects = slices.Clone(f.Projects)
	slices.SortFunc(out.Projects, func(a, b Project) int { return strings.Compare(a.ID, b.ID) })
	if err := out.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	wire := fileJSON{SchemaVersion: out.SchemaVersion, Backend: out.Backend, Projects: []projectJSON{}}
	if out.Timeout != 0 {
		wire.DefaultTimeout = out.Timeout.String()
	}
	for _, p := range out.Projects {
		wire.Projects = append(wire.Projects, projectJSON{ID: p.ID, DisplayName: p.DisplayName, Remote: p.Remote})
	}
	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	if err := statTrustedDirIfExists(filepath.Dir(path)); err != nil {
		return fmt.Errorf("config: trusted directory: %w", err)
	}
	if err := writeFilePrivate(path, data); err != nil {
		return err
	}
	return verifyPersisted(path, out)
}

// verifyPersisted re-reads the document at path through the trusted read path
// and checks it parses, validates, and is the document want describes. It is
// the post-write half of WriteFile: validating in memory proves what we meant
// to write, not what a concurrent writer or a failing filesystem left behind.
//
// A mismatch is reported, not repaired: the rename already happened, so the
// operator — not a silent retry — decides what the file should contain.
func verifyPersisted(path string, want File) error {
	f, err := openTrustedFile(path)
	if err != nil {
		return fmt.Errorf("config: read back written config: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("config: read back written config: %w", err)
	}
	got, err := parseFile(data)
	if err != nil {
		return fmt.Errorf("config: written config did not validate on read back: %w", err)
	}
	if got.SchemaVersion != want.SchemaVersion || got.Timeout != want.Timeout ||
		got.Backend != want.Backend || !slices.Equal(got.Projects, want.Projects) {
		// Neither document is echoed: both may carry operator-controlled text.
		return errors.New("config: written config does not match the document that was persisted")
	}
	return nil
}

// Validate enforces the semantic rules of an in-memory document, independently
// of how it was obtained: parsed from disk, or built by a caller that is about
// to persist it. It fails closed — an unsupported schema version, an
// out-of-policy timeout, or an invalid registry is rejected, never coerced.
//
// The error is redacted at the boundary because it interpolates document values
// (redactErr leaves a non-secret error untouched).
func (f File) Validate() (err error) {
	defer func() { err = redactErr(err) }()

	if !readableSchemaVersion(f.SchemaVersion) {
		return fmt.Errorf("schema_version %q is not supported (want one of %v)",
			f.SchemaVersion, readableSchemaVersions)
	}
	if f.Backend != "" && !backendNamePattern.MatchString(f.Backend) {
		return fmt.Errorf("backend %q is not a valid backend name", f.Backend)
	}

	if f.Timeout != 0 {
		if err := (Settings{Timeout: f.Timeout}).Validate(); err != nil {
			return fmt.Errorf("default_timeout invalid: %w", err)
		}
	}
	return validateProjects(f.Projects)
}

// parseFile decodes and strictly validates a config document. The declared
// version is checked before the decode, so an unsupported document is rejected
// by version rather than by whichever field happens to be unknown to the one
// wire form we have.
//
// The returned error never contains the raw document bytes or secret-shaped
// material: the raw pre-scan rejects unescaped secrets early, and redactErr on
// every return path scrubs any secret a JSON-escaped value or decoder message
// could reveal.
func parseFile(data []byte) (f File, err error) {
	defer func() { err = redactErr(err) }()
	// Reject secret-shaped material anywhere in the document before echoing any
	// part of it, so neither the parse error nor later handling can leak it.
	if containsSecretShape(string(data)) {
		return File{}, errors.New("contains secret-shaped material; config must be non-secret")
	}

	// Probe the declared version first. The probe is deliberately non-strict
	// about other fields — the decode below is the one that enforces the schema
	// — but it still rejects malformed JSON and trailing data (json.Unmarshal
	// accepts exactly one document).
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return File{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if !readableSchemaVersion(probe.SchemaVersion) {
		return File{}, fmt.Errorf("schema_version %q is not supported (want one of %v)",
			probe.SchemaVersion, readableSchemaVersions)
	}

	var raw fileJSON
	if probe.SchemaVersion == "2" {
		var legacy fileJSONV2
		if err := decodeStrict(data, &legacy); err != nil {
			return File{}, err
		}
		raw = fileJSON{SchemaVersion: legacy.SchemaVersion, DefaultTimeout: legacy.DefaultTimeout, Projects: legacy.Projects}
	} else if err := decodeStrict(data, &raw); err != nil {
		return File{}, err
	}
	// The document is normalized to the current version on the way in, so a
	// document read as "2" is written back as "3" the next time anything
	// changes. Nothing downstream has a reason to know which version was on
	// disk; what matters is that reading an older one is lossless.
	f = File{SchemaVersion: ConfigSchemaVersion, Backend: raw.Backend}
	if err := f.setTimeout(raw.DefaultTimeout); err != nil {
		return File{}, err
	}
	for _, rp := range raw.Projects {
		f.Projects = append(f.Projects, Project{ID: rp.ID, DisplayName: rp.DisplayName, Remote: rp.Remote})
	}

	if err := f.Validate(); err != nil {
		return File{}, err
	}
	return f, nil
}

// setTimeout parses and policy-checks the wire default_timeout. An empty value
// leaves Timeout unset (0).
func (f *File) setTimeout(raw string) error {
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		// The rejected value is not echoed: it is caller-controlled text.
		return errors.New("default_timeout is not a valid duration")
	}
	// An explicitly written value is policy-checked here rather than in
	// Validate: in memory a zero Timeout means "unset", so "0s" on the wire
	// would otherwise be silently downgraded to the default instead of rejected.
	if err := (Settings{Timeout: d}).Validate(); err != nil {
		return fmt.Errorf("default_timeout invalid: %w", err)
	}
	f.Timeout = d
	return nil
}

// decodeStrict decodes exactly one JSON document from data into v, rejecting
// unknown fields at every level.
//
// Decoder.More() only tests for a next element within the current array/object,
// so a trailing closing delimiter can slip past it; a second Decode that must
// return io.EOF is the reliable end-of-input check (trailing whitespace is
// skipped and still yields EOF).
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON or unknown field: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("unexpected trailing data after JSON document")
	}
	return nil
}
