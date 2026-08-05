package config

import (
	"errors"
	"fmt"
	"io/fs"
)

// objType is the platform-neutral kind of a filesystem object as the trust
// policy needs it. Symlinks are not represented: trusted paths are opened
// no-follow, so a final-component symlink fails at open time on supported hosts
// and never reaches the policy check.
type objType int

const (
	objOther objType = iota
	objRegular
	objDir
)

func (t objType) String() string {
	switch t {
	case objRegular:
		return "regular file"
	case objDir:
		return "directory"
	default:
		return "non-regular object"
	}
}

// verifyTrusted enforces the platform-neutral trust invariants on an already-
// opened object's stat result: it has the wanted type, carries mode-private
// permissions (no group/other access), and is owned by the effective user. It
// is pure — no filesystem, no globals — so every rule is unit-tested by
// constructing inputs directly. Production supplies typ/perm/uid from a single
// Fstat on the validated descriptor and euid from os.Geteuid(); there is no
// runtime-configurable ownership override (see ADR-0001 constraint 2). path is a
// local filesystem path used only for diagnostics and is never a secret.
//
// Terminology (ADR-0001): "mode-private" is the permission property (no 0o077
// bits); "owned-by-EUID" is the ownership property (uid == euid). Trust requires
// both, plus the type check.
func verifyTrusted(path string, want, typ objType, perm fs.FileMode, uid, euid uint32) error {
	if typ != want {
		return fmt.Errorf("config: %s is a %s, want a %s", path, typ, want)
	}
	if perm&0o077 != 0 {
		return fmt.Errorf("config: %s has insecure permissions %#o; want mode-private (no group/other access)", path, perm)
	}
	if uid != euid {
		return fmt.Errorf("config: %s is not owned by the effective user (owner uid %d, euid %d)", path, uid, euid)
	}
	return nil
}

// statTrustedDirIfExists validates a trusted directory when present and accepts
// its absence (a not-yet-created default directory is a valid first-run
// precondition). A present directory must satisfy the trusted-directory policy
// (non-symlink directory, mode-private, owned by the effective user); any
// violation fails closed. See statTrustedDir for the platform primitive and its
// documented non-darwin/linux boundary.
func statTrustedDirIfExists(dir string) error {
	err := statTrustedDir(dir)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
