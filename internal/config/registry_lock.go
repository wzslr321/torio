package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func withRegistryLock(path string, update func() error) error {
	dir := filepath.Dir(path)
	if err := statTrustedDirIfExists(dir); err != nil {
		return fmt.Errorf("config: trusted directory: %w", err)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("config: create registry directory: %w", err)
	}
	if err := statTrustedDirIfExists(dir); err != nil {
		return fmt.Errorf("config: trusted directory: %w", err)
	}
	return withAdvisoryLock(dir, update)
}
