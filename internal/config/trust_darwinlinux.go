//go:build darwin || linux

package config

import (
	"io/fs"
	"os"
	"syscall"
)

// This file holds the trusted-open primitive for the supported security hosts
// (Darwin and Linux arm64; see ADR-0013). It is deliberately NOT tagged with the
// generic `unix` constraint: the trusted-authority policy is only claimed on
// darwin and linux. syscall.Open + Fstat cover both without a third-party
// dependency; full openat-relative resolution (which would need
// golang.org/x/sys/unix, absent on darwin as syscall.Openat) is not required
// within the accepted direct-parent trust boundary.

// openTrustedFile opens path for reading without following a final-component
// symlink, then validates from the SAME descriptor that the object is a
// mode-private, effective-user-owned regular file. The caller reads from the
// returned *os.File; because validation and the subsequent read share one
// descriptor, no path re-resolution can substitute a different object between
// check and use (no TOCTOU on the final component). A final-component symlink
// fails at open (ELOOP); an absent file yields fs.ErrNotExist so callers can
// honor first-run semantics.
func openTrustedFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	typ, perm, uid, err := fstatObj(fd)
	if err != nil {
		_ = f.Close()
		return nil, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	if err := verifyTrusted(path, objRegular, typ, perm, uid, uint32(os.Geteuid())); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// statTrustedDir opens dir without following a final-component symlink and
// requiring a directory, then validates from the same descriptor that it is a
// mode-private, effective-user-owned directory. It returns no handle: callers
// within the accepted direct-parent boundary need only the validation. A symlink
// at the final component fails at open (ELOOP on Linux, ENOTDIR on Darwin — both
// detected as "open failed", not a specific errno); an absent directory yields
// fs.ErrNotExist.
func statTrustedDir(dir string) error {
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: dir, Err: err}
	}
	defer syscall.Close(fd)
	typ, perm, uid, err := fstatObj(fd)
	if err != nil {
		return &os.PathError{Op: "fstat", Path: dir, Err: err}
	}
	return verifyTrusted(dir, objDir, typ, perm, uid, uint32(os.Geteuid()))
}

// fstatObj reads type, permission bits and owner uid from an open descriptor.
// The raw syscall mode is translated into the platform-neutral objType so the
// pure policy check stays free of syscall constants.
func fstatObj(fd int) (objType, fs.FileMode, uint32, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return objOther, 0, 0, err
	}
	perm := fs.FileMode(uint32(st.Mode) & 0o777)
	var typ objType
	switch uint32(st.Mode) & syscall.S_IFMT {
	case syscall.S_IFREG:
		typ = objRegular
	case syscall.S_IFDIR:
		typ = objDir
	default:
		typ = objOther
	}
	return typ, perm, st.Uid, nil
}
