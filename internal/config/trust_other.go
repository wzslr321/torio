//go:build !darwin && !linux

package config

import "os"

// On hosts other than Darwin and Linux the trusted-authority policy (no-follow
// open, ownership and mode enforcement) is NOT claimed: the security-relevant
// hosts for Demo A are macOS and Linux arm64 (see ADR-0001). These stubs
// preserve functional behavior — files open, directories read as present or
// absent — without asserting the trust invariants. This is a documented no-op
// boundary, consistent with the historical perm_other.go stance it replaces.

// openTrustedFile opens path without the no-follow / type / ownership guarantees
// available on darwin and linux. The security policy is not claimed here.
func openTrustedFile(path string) (*os.File, error) {
	return os.Open(path)
}

// statTrustedDir reports whether dir exists and is a directory, without the
// no-symlink / mode-private / ownership guarantees claimed on darwin and linux.
func statTrustedDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return &os.PathError{Op: "stat", Path: dir, Err: os.ErrInvalid}
	}
	return nil
}
