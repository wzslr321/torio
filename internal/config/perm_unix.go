//go:build unix

package config

import (
	"fmt"
	"os"
)

// enforcePrivateMode rejects any group- or world-accessible bits on Unix hosts.
// Config and state are owner-private; a looser mode is a fail-closed error
// rather than a silently accepted state. The path is included for diagnostics
// (it is a local filesystem path, never a secret).
func enforcePrivateMode(path string, mode os.FileMode) error {
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("config: %s has insecure permissions %#o; want owner-only (%#o)", path, mode.Perm(), filePerm)
	}
	return nil
}
