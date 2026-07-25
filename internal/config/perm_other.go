//go:build !unix

package config

import "os"

// enforcePrivateMode is a no-op on non-Unix hosts, where the POSIX permission
// bits do not carry the same owner-only guarantee. Platform behavior is thus
// explicit: the owner-only enforcement is Unix-only and is not claimed
// elsewhere. Supported Demo A hosts (macOS/Linux arm64) are all Unix.
func enforcePrivateMode(_ string, _ os.FileMode) error {
	return nil
}
