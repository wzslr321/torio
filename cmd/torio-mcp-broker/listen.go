package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// socketDir is where the broker publishes one socket per service (ADR-0022 §3).
// It is fixed in the binary for the same reason it is fixed in the relay: the
// guest layout is Torio's, not the caller's, and an overridable base would let
// anything that can set argv or the environment move the boundary.
const socketDir = "/run/torio-mcp"

// socketSuffix keeps the service name and the file name distinct, so a name is
// never mistaken for a whole path.
const socketSuffix = ".sock"

// socketMode is the mode ADR-0022 §3 requires: the owner (torio-mcp) and the
// client group (torio-mcp-clients), nobody else. Group membership is the entire
// privilege the agent identity holds, so the bits that express it are not left
// to whatever umask the process inherited.
const socketMode fs.FileMode = 0o660

// The service-name rule is NOT defined here. It lives in
// internal/mcpbroker.ValidateServiceName, which is also what the relay resolves
// its path with: the two binaries own opposite halves of one address, and a rule
// written twice would drift into a socket one side binds and the other cannot
// reach. That rule's length bound is also what keeps the resolved path inside the
// kernel's ~104-byte sun_path limit.

// listenError is a socket failure classified by what an operator has to do about
// it. The exit classes come from docs/contracts/cli.md; the remedies behind them
// have nothing in common, so collapsing them into one code would mean the
// difference between "stop the other broker", "the unit's runtime directory is
// missing" and "this identity may not hand the socket to that group" reaching the
// operator as a single "it did not start".
type listenError struct {
	exit int
	msg  string
}

func (e *listenError) Error() string { return e.msg }

func classify(exit int, format string, args ...any) *listenError {
	return &listenError{exit: exit, msg: fmt.Sprintf(format, args...)}
}

// listenService binds the socket for one service and hands it to the client
// group.
//
// base and gid are parameters so tests can bind under a temp directory and prove
// the chown against a group they really belong to; production passes socketDir
// and the gid of torio-mcp-clients. gid < 0 leaves group ownership alone, which
// is only for tests — on the guest, a socket that did not reach the client group
// is a boundary that was not built.
//
// Every failure removes the socket it created. A half-built boundary left on disk
// is worse than none: it answers a stat with the right owner and the right mode
// while refusing, or accepting, the wrong callers.
func listenService(base, service string, gid int) (*net.UnixListener, error) {
	if err := mcpbroker.ValidateServiceName(service); err != nil {
		return nil, classify(exitUsage, "%v", err)
	}
	path := filepath.Join(base, service+socketSuffix)

	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, bindError(path, err)
	}

	if err := secureSocket(path, gid); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// clearStaleSocket removes a socket left behind by a broker that is gone, and
// refuses everything else.
//
// The distinction is made by connecting, because it cannot be made by looking:
// the file a crashed broker leaves has the same owner, group and mode as the one
// a running broker is serving. ECONNREFUSED is the kernel saying nothing is
// listening on that address — a fact no stat can produce.
func clearStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return classify(exitVerificationFailed, "inspect %s: %v", path, err)
	}
	if info.Mode()&fs.ModeSocket == 0 {
		// Removal is deliberately narrow. A rule that cleared whatever was in the
		// way would be an unlink primitive pointed at a path in /run, and the
		// broker's job does not include deciding that somebody else's file is
		// rubbish.
		return classify(exitConflict, "%s exists and is not a socket; the broker will not remove it — "+
			"something other than a broker owns that path", path)
	}

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err == nil {
		conn.Close()
		return classify(exitConflict, "%s is already served by a running broker; "+
			"stop that broker before starting another for the same service", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		// Anything else — a permission error, an address the kernel will not
		// resolve — leaves the question open, and an open question about whether
		// somebody else is serving this address is not grounds for unlinking it.
		return classify(exitVerificationFailed, "cannot tell whether %s is still served (%v); refusing to replace it", path, err)
	}
	if err := os.Remove(path); err != nil {
		return classify(exitPermissionDenied, "remove the stale socket at %s: %v", path, err)
	}
	return nil
}

// bindError classifies a failed bind by what stopped it. A missing directory is
// the unit's RuntimeDirectory not being there, which is a precondition; a
// permission error is this identity not being allowed to publish in it.
func bindError(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return classify(exitPrecondition, "cannot bind %s: %v — the broker's socket directory does not exist "+
			"(it is the unit's runtime directory)", path, err)
	case errors.Is(err, fs.ErrPermission):
		return classify(exitPermissionDenied, "cannot bind %s: %v — the broker's identity may not create a socket there", path, err)
	}
	return classify(exitInternal, "cannot bind %s: %v", path, err)
}

// secureSocket sets the mode and group of a freshly bound socket, then proves
// both rather than trusting the calls that made them.
//
// There is a window between bind and chmod in which the socket carries whatever
// mode the umask produced. It is closed as immediately as a process can close it,
// and the enclosing directory is the real gate: /run/torio-mcp is created by the
// broker's unit, and a socket cannot be reached through a directory that cannot
// be traversed.
func secureSocket(path string, gid int) error {
	if gid >= 0 {
		if err := os.Chown(path, -1, gid); err != nil {
			return classify(exitPermissionDenied, "hand %s to client group %d: %v — the broker's identity must be able to give "+
				"the socket to that group (either it is a member, or the unit creates the socket with the group already set)",
				path, gid, err)
		}
	}
	if err := os.Chmod(path, socketMode); err != nil {
		return classify(exitPermissionDenied, "set the mode of %s: %v", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return classify(exitVerificationFailed, "verify %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != socketMode {
		return classify(exitVerificationFailed, "%s is mode %04o after being set to %04o; the boundary is not what it was asked to be",
			path, perm, socketMode)
	}
	if gid >= 0 {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return classify(exitVerificationFailed, "verify group ownership of %s: no unix ownership on this platform", path)
		}
		if int(stat.Gid) != gid {
			return classify(exitVerificationFailed, "%s belongs to group %d, want the client group %d", path, stat.Gid, gid)
		}
	}
	return nil
}
