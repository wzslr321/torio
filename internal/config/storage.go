package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// dirPerm is the owner-only permission for trusted config/state directories.
	dirPerm os.FileMode = 0o700
	// filePerm is the owner-only permission for config/state files.
	filePerm os.FileMode = 0o600
)

// writeFilePrivate writes data to path crash-safely and with owner-only
// permissions (AGENTS §6): the parent directory is created 0700, the bytes are
// written to a temp file in the same directory, fsync'd, and atomically renamed
// over path. A partially written file can never be observed at path, and no
// temp file is left behind on success. On the failure paths the temp file is
// removed.
func writeFilePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// From here on, ensure the temp file never lingers on any error path.
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(filePerm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("atomic rename: %w", err)
	}
	return nil
}

// statPrivate stats path and enforces that it carries owner-only permissions on
// hosts where that is meaningful (see enforcePrivateMode). It fails closed: a
// group/world-accessible trusted file is rejected rather than trusted.
func statPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return enforcePrivateMode(path, fi.Mode())
}
