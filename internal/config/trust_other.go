//go:build !darwin && !linux

package config

import "os"

// On hosts other than Darwin and Linux the trusted-authority policy (no-follow
// open, ownership and mode enforcement) is NOT claimed. The supported host
// matrix is `darwin/arm64` and `linux/amd64` (ADR-0002 §4), and `lima.ProfileFor`
// refuses anything else, so this file exists to keep the package compilable —
// not to serve a host Torio runs on. These stubs preserve functional behavior —
// files open, directories read as present or absent — without asserting the
// trust invariants.
//
// That makes this the one place in the package where an unavailable guarantee
// degrades instead of failing: `transfer/open_other.go`, guarded by the same
// build tag, returns an error rather than an unchecked handle. Widening the
// matrix, or deciding that a host without the guarantee should not compile at
// all, has to settle that difference; it is not settled here.

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
