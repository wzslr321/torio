package lima

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// CopyToGuest transfers one already-filtered private host staging directory to
// a staging directory below guestHome, using the exact `limactl copy` shape
// promoted by the Brain transfer Gate. The trailing slashes are intentional:
// the verified contract copies directory contents while preserving their
// relative tree.
//
// guestHome is the home of the guest identity this transfer is for, and it is a
// parameter rather than a constant because the boundary belongs to that
// identity. It was a fixed home, which on a second backend meant private vault
// bytes were only ever accepted into the *other* identity's home — the one
// place they must not land.
func (a *Adapter) CopyToGuest(ctx context.Context, hostSourceDir, guestDestinationDir, guestHome string) error {
	const op = "copy_to_guest"
	host, guest, err := transferPaths(hostSourceDir, guestDestinationDir, guestHome)
	if err != nil {
		return &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}
	return a.copy(ctx, op, host, InstanceName+":"+guest)
}

// CopyFromGuest transfers one guest staging directory back to a private host
// staging directory. It is the mirror of CopyToGuest, validated by the same
// function with the sides swapped, and it exists because one Second Brain
// replicated into several guests needs bytes to travel both ways (ADR-0025).
//
// What travels is a Git bundle: one file, written by the guest, read on the
// host by `git fetch`. It configures no remote on either side, so the rule that
// a vault carrying a network remote is drift is untouched.
func (a *Adapter) CopyFromGuest(ctx context.Context, guestSourceDir, hostDestinationDir, guestHome string) error {
	const op = "copy_from_guest"
	host, guest, err := transferPaths(hostDestinationDir, guestSourceDir, guestHome)
	if err != nil {
		return &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}
	return a.copy(ctx, op, InstanceName+":"+guest, host)
}

func (a *Adapter) copy(ctx context.Context, op, source, destination string) error {
	res, err := a.runRaw(ctx, "copy", source, destination)
	if err != nil {
		return privateCopyRunError(op, err)
	}
	if res.ExitCode != 0 {
		return &Error{
			Op:   op,
			Kind: KindCommandFailed,
			Err:  fmt.Errorf("transport exited %d", res.ExitCode),
		}
	}
	return nil
}

// transferPaths validates every side before any of them is rendered into
// Lima's colon-based remote syntax. Callers may choose the host directory, but
// it must be an absolute non-root path with no remote-syntax/control bytes.
// Guest staging is deliberately narrower: exactly a contained descendant of the
// owning identity's home, never /tmp or another guest authority boundary.
//
// The home is validated to the same standard as the destination rather than
// trusted for coming from a backend. It reaches an argv the same way, and a
// boundary that accepts anything as its own root is not one.
func transferPaths(hostDir, guestDir, guestHome string) (host, guest string, err error) {
	if !filepath.IsAbs(hostDir) ||
		filepath.Clean(hostDir) == string(filepath.Separator) ||
		strings.ContainsAny(hostDir, ":\x00\n\r") {
		return "", "", fmt.Errorf("host transfer path is outside the typed staging boundary")
	}
	host = filepath.Clean(hostDir) + string(filepath.Separator)

	if !strings.HasPrefix(guestHome, "/") ||
		strings.ContainsAny(guestHome, ":\x00\n\r\\") ||
		path.Clean(guestHome) != guestHome ||
		guestHome == "/" {
		return "", "", fmt.Errorf("guest transfer boundary is not a usable guest home")
	}

	if !strings.HasPrefix(guestDir, "/") ||
		strings.ContainsAny(guestDir, ":\x00\n\r\\") {
		return "", "", fmt.Errorf("guest transfer path is outside the typed staging boundary")
	}
	cleanGuest := path.Clean(guestDir)
	if cleanGuest != guestDir ||
		cleanGuest == guestHome ||
		!strings.HasPrefix(cleanGuest, guestHome+"/") {
		return "", "", fmt.Errorf("guest transfer path is outside the typed staging boundary")
	}
	guest = cleanGuest + "/"
	return host, guest, nil
}

// privateCopyRunError keeps only the failure class. execx diagnostics include
// argv and backend stderr by design; both may contain a private host staging
// path or a Brain filename, so this boundary intentionally discards them.
func privateCopyRunError(op string, err error) *Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Op: op, Kind: KindTimeout}
	case errors.Is(err, context.Canceled):
		return &Error{Op: op, Kind: KindCancelled}
	default:
		return &Error{Op: op, Kind: KindBinaryUnavailable}
	}
}
