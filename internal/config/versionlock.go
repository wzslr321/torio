package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// VersionLockSchemaVersion is the only accepted version-lock schema version.
const VersionLockSchemaVersion = "1"

// versionValue bounds an accepted tool version pin: it must start
// alphanumerically and contain only version-safe characters. This keeps the
// manifest a plain, non-secret metadata pin and rejects free-form or injected
// content.
var versionValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// VersionLock is the typed, schema-versioned, non-secret version-lock manifest.
// It pins the external tool versions that later slices consume — D3 (the Lima
// adapter's feature/version probe) and D4 (deterministic bootstrap's pinned
// dependencies). D2 only owns its parse/validate/write lifecycle; it performs
// no runtime probing and installs nothing.
type VersionLock struct {
	// SchemaVersion is the manifest schema version; always
	// VersionLockSchemaVersion once validated.
	SchemaVersion string
	// Lima/Docker/Hermes are optional pinned tool versions. Empty means unpinned.
	Lima   string
	Docker string
	Hermes string
}

// versionLockJSON is the wire form. Unknown fields are rejected on decode so
// the manifest fails closed.
type versionLockJSON struct {
	SchemaVersion string `json:"schema_version"`
	Lima          string `json:"lima,omitempty"`
	Docker        string `json:"docker,omitempty"`
	Hermes        string `json:"hermes,omitempty"`
}

// LoadVersionLock reads and strictly validates the manifest at path. On Unix it
// requires owner-only permissions. Errors never contain secret-shaped material.
func LoadVersionLock(path string) (VersionLock, error) {
	if err := statPrivate(path); err != nil {
		return VersionLock{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return VersionLock{}, fmt.Errorf("read version-lock: %w", err)
	}
	m, err := parseVersionLock(data)
	if err != nil {
		return VersionLock{}, redactErr(fmt.Errorf("version-lock %s: %w", path, err))
	}
	return m, nil
}

// WriteVersionLock validates m and writes it crash-safely with owner-only
// permissions. An invalid manifest is rejected before any file is created.
func WriteVersionLock(path string, m VersionLock) error {
	if err := m.validate(); err != nil {
		// validate interpolates the caller-supplied schema_version with %q; a
		// secret-shaped value would otherwise leak through the write path too.
		return redactErr(fmt.Errorf("version-lock: %w", err))
	}
	data, err := json.MarshalIndent(versionLockJSON{
		SchemaVersion: m.SchemaVersion,
		Lima:          m.Lima,
		Docker:        m.Docker,
		Hermes:        m.Hermes,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal version-lock: %w", err)
	}
	data = append(data, '\n')
	return writeFilePrivate(path, data)
}

// parseVersionLock decodes and strictly validates a manifest. As in parseFile,
// redactErr on every return path guarantees no secret-shaped material leaves the
// parser, including one hidden from the raw pre-scan by JSON escaping.
func parseVersionLock(data []byte) (m VersionLock, err error) {
	defer func() { err = redactErr(err) }()
	if containsSecretShape(string(data)) {
		return VersionLock{}, errors.New("contains secret-shaped material; version-lock must be non-secret")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw versionLockJSON
	if err := dec.Decode(&raw); err != nil {
		return VersionLock{}, fmt.Errorf("invalid JSON or unknown field: %w", err)
	}
	// Exactly one JSON document; see parseFile for why a second Decode requiring
	// io.EOF is used instead of Decoder.More().
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return VersionLock{}, errors.New("unexpected trailing data after JSON document")
	}
	m = VersionLock{
		SchemaVersion: raw.SchemaVersion,
		Lima:          raw.Lima,
		Docker:        raw.Docker,
		Hermes:        raw.Hermes,
	}
	if verr := m.validate(); verr != nil {
		return VersionLock{}, verr
	}
	return m, nil
}

// validate enforces the schema version and the shape of any set tool pin. It
// never echoes a rejected value that matches a secret shape.
func (m VersionLock) validate() error {
	if m.SchemaVersion != VersionLockSchemaVersion {
		return fmt.Errorf("schema_version %q is not supported (want %q)", m.SchemaVersion, VersionLockSchemaVersion)
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"lima", m.Lima},
		{"docker", m.Docker},
		{"hermes", m.Hermes},
	} {
		if f.value == "" {
			continue
		}
		if containsSecretShape(f.value) {
			return fmt.Errorf("%s contains secret-shaped material; version-lock must be non-secret", f.name)
		}
		if !versionValue.MatchString(f.value) {
			return fmt.Errorf("%s is not a valid version pin", f.name)
		}
	}
	return nil
}
