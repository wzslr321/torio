package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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

// LoadVersionLock reads and strictly validates the manifest at path. The
// manifest is authority for D3/D4, so its trusted directory is validated (within
// the accepted direct-parent boundary) and the file is opened without following
// a final-component symlink, then checked for regular type, mode-private
// permissions and effective-user ownership from the same descriptor before it is
// read. Errors never contain secret-shaped material — every returned error is
// redacted at the package boundary, since path is caller-controlled and may
// itself carry a secret shape (redactErr leaves non-secret errors untouched).
// The policy is enforced on darwin and linux (see ADR-0013 / trust_darwinlinux.go).
func LoadVersionLock(path string) (_ VersionLock, err error) {
	defer func() { err = redactErr(err) }()

	if err := statTrustedDirIfExists(filepath.Dir(path)); err != nil {
		return VersionLock{}, err
	}
	f, err := openTrustedFile(path)
	if err != nil {
		return VersionLock{}, fmt.Errorf("read version-lock: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return VersionLock{}, fmt.Errorf("read version-lock: %w", err)
	}
	m, err := parseVersionLock(data)
	if err != nil {
		return VersionLock{}, fmt.Errorf("version-lock %s: %w", path, err)
	}
	return m, nil
}

// WriteVersionLock validates m and writes it crash-safely with owner-only
// permissions. An invalid manifest is rejected before any file is created.
//
// Within the accepted direct-parent boundary (ADR-0013 constraint 4), the
// trusted directory is validated before any file is created in it: an existing
// directory that is a symlink, mode-permissive, or owned by another user must
// not become authority merely because the final rename is atomic. A
// not-yet-existing directory is created privately by writeFilePrivate.
//
// As with the read paths, every returned error is redacted at the package
// boundary: validate interpolates the caller-supplied schema_version with %q,
// and the trusted-directory check interpolates the caller-controlled path — both
// of which may carry secret-shaped material (redactErr leaves non-secret errors
// untouched).
func WriteVersionLock(path string, m VersionLock) (err error) {
	defer func() { err = redactErr(err) }()

	if err := m.validate(); err != nil {
		return fmt.Errorf("version-lock: %w", err)
	}
	if err := statTrustedDirIfExists(filepath.Dir(path)); err != nil {
		return fmt.Errorf("version-lock: trusted directory: %w", err)
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
	var raw versionLockJSON
	if err := decodeStrict(data, &raw); err != nil {
		return VersionLock{}, err
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
