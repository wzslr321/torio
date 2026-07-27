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
// a staging directory below HermesHome using the exact `limactl copy` shape
// promoted by the Brain transfer Gate. The trailing slashes are intentional:
// the verified contract copies directory contents while preserving their
// relative tree.
func (a *Adapter) CopyToGuest(ctx context.Context, hostSourceDir, guestDestinationDir string) error {
	const op = "copy_to_guest"
	host, guest, err := transferPaths(hostSourceDir, guestDestinationDir)
	if err != nil {
		return &Error{Op: op, Kind: KindVerificationFailed, Err: err}
	}
	return a.copy(ctx, op, host, InstanceName+":"+guest)
}

// CopyFromGuest transfers one private guest staging directory below HermesHome
// into a private host staging directory. It never returns command output:
// rsync/scp backends may name payload files in diagnostics, and Brain filenames
// are outside Torio's output and logging contract.
func (a *Adapter) CopyFromGuest(ctx context.Context, guestSourceDir, hostDestinationDir string) error {
	const op = "copy_from_guest"
	host, guest, err := transferPaths(hostDestinationDir, guestSourceDir)
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

// transferPaths validates both sides before either is rendered into Lima's
// colon-based remote syntax. Callers may choose the host directory, but it must
// be an absolute non-root path with no remote-syntax/control bytes. Guest
// staging is deliberately narrower: exactly a contained descendant of the
// fixed Hermes home, never /tmp or another guest authority boundary.
func transferPaths(hostDir, guestDir string) (host, guest string, err error) {
	if !filepath.IsAbs(hostDir) ||
		filepath.Clean(hostDir) == string(filepath.Separator) ||
		strings.ContainsAny(hostDir, ":\x00\n\r") {
		return "", "", fmt.Errorf("host transfer path is outside the typed staging boundary")
	}
	host = filepath.Clean(hostDir) + string(filepath.Separator)

	if !strings.HasPrefix(guestDir, "/") ||
		strings.ContainsAny(guestDir, ":\x00\n\r\\") {
		return "", "", fmt.Errorf("guest transfer path is outside the typed staging boundary")
	}
	cleanGuest := path.Clean(guestDir)
	if cleanGuest != guestDir ||
		cleanGuest == HermesHome ||
		!strings.HasPrefix(cleanGuest, HermesHome+"/") {
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
