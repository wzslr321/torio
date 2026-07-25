package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"
)

// ConfigSchemaVersion is the only accepted config document schema version. A
// document declaring any other value is rejected rather than migrated.
const ConfigSchemaVersion = "1"

// File is the validated, typed content of the on-disk config document. It holds
// only non-secret operator intent. Fields are deliberately minimal in D2; later
// slices extend the schema behind the same version gate.
type File struct {
	// SchemaVersion is the document schema version; always ConfigSchemaVersion
	// once validated.
	SchemaVersion string
	// Timeout is the parsed default operation timeout, or 0 when the document
	// omits default_timeout. When set it is bounded by policy (see Validate).
	Timeout time.Duration
}

// fileJSON is the wire form of File. Unknown fields are rejected by the decoder
// (DisallowUnknownFields), so the schema fails closed.
type fileJSON struct {
	SchemaVersion  string `json:"schema_version"`
	DefaultTimeout string `json:"default_timeout"`
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
//   - On Unix, a group/world-accessible config file or existing state dir is
//     rejected (owner-only is required).
//
// Load performs no writes and does not create directories.
func Load(opts Options) (Runtime, error) {
	paths, err := ResolvePaths(opts)
	if err != nil {
		return Runtime{}, err
	}

	rt := Runtime{Paths: paths}

	// Validate the trusted state dir's permissions if it already exists. A
	// not-yet-created state dir is fine (later slices create it privately).
	if err := statDirIfExists(paths.StateDir); err != nil {
		return Runtime{}, err
	}

	data, err := os.ReadFile(paths.ConfigFile)
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

	// The config file exists; enforce owner-only permissions before trusting it.
	if err := statPrivate(paths.ConfigFile); err != nil {
		return Runtime{}, err
	}

	f, err := parseFile(data)
	if err != nil {
		return Runtime{}, redactErr(fmt.Errorf("config file %s: %w", paths.ConfigFile, err))
	}
	rt.File = f
	rt.ConfigLoaded = true
	return rt, nil
}

// parseFile decodes and strictly validates a config document. The returned
// error never contains the raw document bytes or secret-shaped material: the
// raw pre-scan rejects unescaped secrets early, and redactErr on every return
// path scrubs any secret a JSON-escaped value or decoder message could reveal.
func parseFile(data []byte) (f File, err error) {
	defer func() { err = redactErr(err) }()
	// Reject secret-shaped material anywhere in the document before echoing any
	// part of it, so neither the parse error nor later handling can leak it.
	if containsSecretShape(string(data)) {
		return File{}, errors.New("contains secret-shaped material; config must be non-secret")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw fileJSON
	if err := dec.Decode(&raw); err != nil {
		return File{}, fmt.Errorf("invalid JSON or unknown field: %w", err)
	}
	// Exactly one JSON document is allowed. Decoder.More() only tests for a next
	// element within the current array/object, so a trailing closing delimiter
	// can slip past it; a second Decode that must return io.EOF is the reliable
	// end-of-input check (trailing whitespace is skipped and still yields EOF).
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return File{}, errors.New("unexpected trailing data after JSON document")
	}

	if raw.SchemaVersion != ConfigSchemaVersion {
		return File{}, fmt.Errorf("schema_version %q is not supported (want %q)", raw.SchemaVersion, ConfigSchemaVersion)
	}

	f = File{SchemaVersion: raw.SchemaVersion}
	if raw.DefaultTimeout != "" {
		d, derr := time.ParseDuration(raw.DefaultTimeout)
		if derr != nil {
			return File{}, fmt.Errorf("default_timeout is not a valid duration")
		}
		if verr := (Settings{Timeout: d}).Validate(); verr != nil {
			return File{}, fmt.Errorf("default_timeout invalid: %w", verr)
		}
		f.Timeout = d
	}
	return f, nil
}

// statDirIfExists enforces owner-only permissions on dir when it exists. A
// missing dir is not an error; a non-directory at the path is.
func statDirIfExists(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat state dir: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("state dir %s is not a directory", dir)
	}
	return enforcePrivateMode(dir, fi.Mode())
}
