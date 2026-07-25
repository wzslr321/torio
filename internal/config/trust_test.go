//go:build darwin || linux

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trust_test.go — D3.0 trusted-config-authority enforcement (ADR-0013).
// These are integration tests through the public API (Load / LoadVersionLock /
// WriteVersionLock) exercising every matrix row that is reproducible without a
// second user. Ownership mismatch is covered here only when the process can
// chown to a foreign uid (root); otherwise it is skipped and the deterministic
// coverage lives in the pure verifyTrusted unit tests. The tag is darwin||linux
// because the trusted-authority policy is only claimed on those hosts.

// --- default config.json: symlink / dir trust ------------------------------

// A default config.json that is a symlink escaping ConfigDir must be rejected
// (out-of-dir content must never become authority).
func TestLoadRejectsDefaultConfigSymlink(t *testing.T) {
	cfgHome := t.TempDir()
	dir := filepath.Join(cfgHome, appDir)
	mustMkdir(t, dir, 0o700)
	outside := filepath.Join(t.TempDir(), "evil.json")
	mustWrite(t, outside, `{"schema_version":"1","default_timeout":"99s"}`, 0o600)
	mustSymlink(t, outside, filepath.Join(dir, configFileName))

	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("symlinked default config.json must be rejected")
	}
}

// A default ConfigDir that is itself a symlink must be rejected before any file
// inside it is trusted (ADR-0013 requirement 1).
func TestLoadRejectsSymlinkedConfigDir(t *testing.T) {
	cfgHome := t.TempDir()
	realDir := filepath.Join(t.TempDir(), "real")
	mustMkdir(t, realDir, 0o700)
	mustWrite(t, filepath.Join(realDir, configFileName), `{"schema_version":"1"}`, 0o600)
	// ConfigDir (<cfgHome>/hermes-box) is a symlink to realDir.
	mustSymlink(t, realDir, filepath.Join(cfgHome, appDir))

	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("symlinked ConfigDir must be rejected")
	}
}

// A world-writable existing ConfigDir must be rejected (the trusted-directory
// authority assumption is void when anyone can swap its contents).
func TestLoadRejectsWorldWritableConfigDir(t *testing.T) {
	cfgHome := t.TempDir()
	dir := filepath.Join(cfgHome, appDir)
	mustMkdir(t, dir, 0o700)
	mustWrite(t, filepath.Join(dir, configFileName), `{"schema_version":"1"}`, 0o600)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("world-writable ConfigDir must be rejected")
	}
}

// A default config.json owned by a different uid must be rejected. Requires the
// ability to chown to a foreign uid (root); skipped otherwise. Deterministic
// coverage of the ownership rule lives in the pure verifyTrusted unit tests.
func TestLoadRejectsConfigFileOwnedByOtherUID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown a file to a foreign uid")
	}
	cfgHome := t.TempDir()
	dir := filepath.Join(cfgHome, appDir)
	mustMkdir(t, dir, 0o700)
	path := filepath.Join(dir, configFileName)
	mustWrite(t, path, `{"schema_version":"1"}`, 0o600)
	if err := os.Chown(path, 12345, -1); err != nil {
		t.Fatalf("chown: %v", err)
	}
	if _, err := loadWith(t, Options{}, cfgHome, t.TempDir()); err == nil {
		t.Fatalf("config.json owned by a foreign uid must be rejected")
	}
}

// --- explicit --config: same final-file enforcement ------------------------

// An explicit --config pointing at a symlink must be rejected: "trusted operator
// input" does not license a symlinked authority file (ADR-0013 decision 1).
func TestLoadRejectsExplicitConfigSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target.json")
	mustWrite(t, target, `{"schema_version":"1","default_timeout":"99s"}`, 0o600)
	link := filepath.Join(base, "link.json")
	mustSymlink(t, target, link)

	if _, err := loadWith(t, Options{ConfigPath: link}, t.TempDir(), t.TempDir()); err == nil {
		t.Fatalf("symlinked explicit --config must be rejected")
	}
}

// An explicit --config with group/world-readable mode must be rejected.
func TestLoadRejectsExplicitConfigNonPrivate(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, cfg, `{"schema_version":"1"}`, 0o644)
	if _, err := loadWith(t, Options{ConfigPath: cfg}, t.TempDir(), t.TempDir()); err == nil {
		t.Fatalf("non-private explicit --config must be rejected")
	}
}

// An explicit --config pointing at a non-regular object (a directory) must be
// rejected by the type check.
func TestLoadRejectsExplicitConfigNonRegular(t *testing.T) {
	d := filepath.Join(t.TempDir(), "adir")
	mustMkdir(t, d, 0o700)
	if _, err := loadWith(t, Options{ConfigPath: d}, t.TempDir(), t.TempDir()); err == nil {
		t.Fatalf("explicit --config to a directory must be rejected")
	}
}

// Happy path: an explicit --config that is a private, effective-user-owned
// regular file still loads (guards against over-rejection).
func TestLoadExplicitConfigHappyPath(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	mustWrite(t, cfg, `{"schema_version":"1","default_timeout":"45s"}`, 0o600)
	rt, err := loadWith(t, Options{ConfigPath: cfg}, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("private regular explicit --config must load: %v", err)
	}
	if !rt.ConfigLoaded {
		t.Errorf("ConfigLoaded = false, want true")
	}
}

// --- version-lock: symlink / dir trust -------------------------------------

// A version-lock.json symlink escaping its directory must be rejected.
func TestLoadVersionLockRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "evil-lock.json")
	mustWrite(t, outside, `{"schema_version":"1","lima":"9.9.9"}`, 0o600)
	link := filepath.Join(dir, versionLockFileName)
	mustSymlink(t, outside, link)
	if _, err := LoadVersionLock(link); err == nil {
		t.Fatalf("symlinked version-lock must be rejected")
	}
}

// A version-lock whose trusted directory is a symlink must be rejected.
func TestLoadVersionLockRejectsSymlinkedParentDir(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "real")
	mustMkdir(t, realDir, 0o700)
	mustWrite(t, filepath.Join(realDir, versionLockFileName), `{"schema_version":"1"}`, 0o600)
	linkDir := filepath.Join(t.TempDir(), "link")
	mustSymlink(t, realDir, linkDir)
	if _, err := LoadVersionLock(filepath.Join(linkDir, versionLockFileName)); err == nil {
		t.Fatalf("version-lock under a symlinked directory must be rejected")
	}
}

// --- WriteVersionLock: validate trusted directory before writing (constraint 4)

// WriteVersionLock into a symlinked trusted directory must be rejected: an
// atomic final rename must not launder an untrusted directory into authority.
func TestWriteVersionLockRejectsSymlinkedTrustedDir(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "real")
	mustMkdir(t, realDir, 0o700)
	linkDir := filepath.Join(t.TempDir(), "link")
	mustSymlink(t, realDir, linkDir)
	m := VersionLock{SchemaVersion: VersionLockSchemaVersion, Lima: "1.0.0"}
	if err := WriteVersionLock(filepath.Join(linkDir, versionLockFileName), m); err == nil {
		t.Fatalf("WriteVersionLock into a symlinked trusted dir must be rejected")
	}
}

// WriteVersionLock into a world-writable existing trusted directory must be
// rejected.
func TestWriteVersionLockRejectsWorldWritableTrustedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hermes-box")
	mustMkdir(t, dir, 0o700)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	m := VersionLock{SchemaVersion: VersionLockSchemaVersion, Lima: "1.0.0"}
	if err := WriteVersionLock(filepath.Join(dir, versionLockFileName), m); err == nil {
		t.Fatalf("WriteVersionLock into a world-writable trusted dir must be rejected")
	}
}

// --- StateDir: symlink / type / ownership trust ----------------------------
//
// StateDir is a core protected input in the accepted policy: Load validates it
// via statTrustedDirIfExists before any later slice trusts it. These public-API
// tests lock that enforcement (symlinked directory, non-directory, and — when
// root — a foreign-owned directory) so it cannot silently regress.

// An existing StateDir that is itself a symlink must be rejected before it is
// trusted (out-of-tree state directory must never become authority).
func TestLoadRejectsSymlinkedStateDir(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "real-state")
	mustMkdir(t, realDir, 0o700)
	linkDir := filepath.Join(t.TempDir(), "state-link")
	mustSymlink(t, realDir, linkDir)
	if _, err := loadWith(t, Options{StateDir: linkDir}, t.TempDir(), ""); err == nil {
		t.Fatalf("symlinked StateDir must be rejected")
	}
}

// An existing StateDir path that is a non-directory (regular file) must be
// rejected by the directory-type check.
func TestLoadRejectsStateDirNotDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state-not-a-dir")
	mustWrite(t, file, "x", 0o600)
	if _, err := loadWith(t, Options{StateDir: file}, t.TempDir(), ""); err == nil {
		t.Fatalf("StateDir that is a regular file must be rejected")
	}
}

// An existing StateDir owned by a different uid must be rejected. Requires the
// ability to chown to a foreign uid (root); skipped otherwise. Deterministic
// coverage of the ownership rule lives in the pure verifyTrusted unit tests.
func TestLoadRejectsStateDirOwnedByOtherUID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown a directory to a foreign uid")
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	mustMkdir(t, stateDir, 0o700)
	if err := os.Chown(stateDir, 12345, -1); err != nil {
		t.Fatalf("chown: %v", err)
	}
	if _, err := loadWith(t, Options{StateDir: stateDir}, t.TempDir(), ""); err == nil {
		t.Fatalf("StateDir owned by a foreign uid must be rejected")
	}
}

// --- no-leak: caller-controlled secret-shaped path in a trust error ---------
//
// D3.0 trust diagnostics interpolate the filesystem path, and an explicit
// --config path (or a version-lock path/dir) is caller-controlled — it may
// itself contain secret-shaped material. The public internal/config API must
// redact its returned errors at the package boundary so a direct API caller
// cannot log a secret-shaped path raw. secretCanary / its recognition by the
// production matcher are defined and guarded in file_test.go.

// Load must not echo a secret-shaped explicit --config path component when the
// file fails the trust policy (here: non-private mode).
func TestLoadDoesNotLeakSecretShapedPathInTrustError(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, secretCanary+".json")
	mustWrite(t, cfg, `{"schema_version":"1"}`, 0o644) // group/world-readable → trust violation
	_, err := loadWith(t, Options{ConfigPath: cfg}, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatalf("non-private explicit --config must be rejected")
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("Load error leaked secret-shaped path component: %q", err.Error())
	}
}

// LoadVersionLock must not echo a secret-shaped manifest-file path component
// when the file fails the trust policy. The parent directory is trusted (0700)
// so the violation is on the file itself, exercising the read-path boundary.
func TestLoadVersionLockDoesNotLeakSecretShapedPathInTrustError(t *testing.T) {
	path := filepath.Join(privDir(t), secretCanary+".json")
	mustWrite(t, path, `{"schema_version":"1"}`, 0o644) // non-private → trust violation
	_, err := LoadVersionLock(path)
	if err == nil {
		t.Fatalf("non-private version-lock file must be rejected")
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("LoadVersionLock error leaked secret-shaped path component: %q", err.Error())
	}
}

// WriteVersionLock must not echo a secret-shaped trusted-directory path
// component when the directory fails the trust policy (here: world-writable),
// exercising the write-path boundary (constraint 4 + the redaction contract).
func TestWriteVersionLockDoesNotLeakSecretShapedPathInTrustError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), secretCanary) // dir name carries the secret shape
	mustMkdir(t, dir, 0o700)
	if err := os.Chmod(dir, 0o777); err != nil { // world-writable → trust violation
		t.Fatalf("chmod: %v", err)
	}
	m := VersionLock{SchemaVersion: VersionLockSchemaVersion, Lima: "1.0.0"}
	err := WriteVersionLock(filepath.Join(dir, versionLockFileName), m)
	if err == nil {
		t.Fatalf("world-writable trusted dir must be rejected")
	}
	if strings.Contains(err.Error(), secretCanary) {
		t.Errorf("WriteVersionLock error leaked secret-shaped path component: %q", err.Error())
	}
}

// --- helpers ---------------------------------------------------------------

func mustMkdir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(dir, mode); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// MkdirAll honors umask; force the exact mode so 0700 fixtures are private.
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}
