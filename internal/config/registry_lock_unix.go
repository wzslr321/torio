//go:build darwin || linux

package config

import (
	"fmt"
	"os"
	"syscall"
)

func withAdvisoryLock(dir string, update func() error) error {
	lock, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("config: open registry directory for lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("config: lock project registry: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return update()
}
