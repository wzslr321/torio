package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFilePrivateCreatesOwnerOnlyFileAndDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "hermes-box")
	path := filepath.Join(dir, "config.json")
	data := []byte(`{"schema_version":"1"}`)

	if err := writeFilePrivate(path, data); err != nil {
		t.Fatalf("writeFilePrivate: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content = %q, want %q", got, data)
	}

	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("file perm = %o, want owner-only (no group/other bits)", perm)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("dir perm = %o, want owner-only (no group/other bits)", perm)
	}
}

func TestWriteFilePrivateOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := writeFilePrivate(path, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFilePrivate(path, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
	// No stray temp files should remain in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want exactly 1 (no temp leftovers): %v", len(entries), entries)
	}
}

// Note: the trusted-file mode/type/ownership rules that statPrivate used to
// approximate are now enforced by openTrustedFile (no-follow open + fstat) and
// unit-tested purely in trust_unit_test.go (verifyTrusted). Integration coverage
// through the public API lives in trust_test.go.
