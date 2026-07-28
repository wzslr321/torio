package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"regexp"
	"syscall"
)

// socketDir is where the broker publishes one socket per service (ADR-0022 §3).
// It is fixed in the binary: the guest layout is Torio's, not the caller's, and
// an overridable base would let anything that can set argv or the environment
// point the relay at a socket of its own.
const socketDir = "/run/torio-mcp"

// socketSuffix keeps the service name and the file name distinct, so a name is
// never mistaken for a whole path.
const socketSuffix = ".sock"

// maxServiceNameLen bounds the service name. A service name is a short label an
// operator writes in a policy file and an MCP client config, not a free-form
// identifier, and the bound is what stops an argv-sized string reaching a
// syscall or a diagnostic line. It also keeps the resolved path inside
// sun_path, the kernel's ~104-byte limit on a unix socket address: socketDir
// plus the longest accepted name plus the suffix is 52 bytes, so an over-long
// address is unreachable by construction rather than caught at connect time.
const maxServiceNameLen = 32

// servicePattern is the accepted service name: a lowercase slug of ASCII
// letters, digits and inner hyphens, bounded to maxServiceNameLen. It is the
// same shape internal/config accepts for a project id, for the same reason —
// nothing in this charset can traverse, rename or escape a directory, which is
// what makes it safe to derive a socket path from.
//
// The bound and the pattern must stay identical to the ones in
// internal/mcpbroker, which validates the policy document that names the same
// service. The two sides own opposite halves of one path: the broker's policy
// file stem decides what the socket is called, this decides what may be asked
// for. A name accepted here and rejected there is a socket nothing can reach.
var servicePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// socketPath resolves the broker socket for one service under base. base is a
// parameter so tests can point at a temp dir; production always passes
// socketDir.
//
// The containment here is structural, not corrective: the name is checked
// against servicePattern and rejected if it does not match, so no traversal,
// separator or absolute path can survive to be joined. There is deliberately no
// cleanup step — a caller that meant "atlassian" and wrote "Atlassian" must be
// told, not guessed at.
func socketPath(base, service string) (string, error) {
	if service == "" {
		return "", errors.New("service name is required")
	}
	// Length is checked before the pattern so an oversized name is reported
	// without being echoed back into a diagnostic.
	if len(service) > maxServiceNameLen {
		return "", fmt.Errorf("service name is longer than %d bytes", maxServiceNameLen)
	}
	if !servicePattern.MatchString(service) {
		return "", fmt.Errorf("service name %q must be a lowercase slug of letters, digits and inner hyphens", service)
	}
	return filepath.Join(base, service+socketSuffix), nil
}

// dialError is a connect failure an operator can act on: the exit class from
// docs/contracts/cli.md plus the remedy for that specific class.
type dialError struct {
	exit int
	msg  string
}

func (e *dialError) Error() string { return e.msg }

// dial opens the broker socket.
//
// The three errno cases below are separated because their remedies have nothing
// in common — one installs software, one changes a group, one starts a service
// — and an operator staring at "connect: connection error" learns none of that.
// They are matched through errors.Is on the portable spellings: syscall.Errno
// maps ENOENT to fs.ErrNotExist and both EACCES and EPERM to fs.ErrPermission,
// so the permission case cannot be missed by matching only one of the two.
func dial(path string) (*net.UnixConn, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err == nil {
		return conn, nil
	}
	// The path is safe to print: it is socketDir joined with a validated slug,
	// so it carries no caller-supplied material beyond a name already shown to
	// be a bare lowercase label.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &dialError{
			exit: exitNoBroker,
			msg: fmt.Sprintf("no broker socket at %s: the MCP broker is not installed on this guest, "+
				"or it has never started (run `torio mcp install`, then check the broker unit)", path),
		}
	case errors.Is(err, fs.ErrPermission):
		// The socket is 0660 torio-mcp:torio-mcp-clients (ADR-0022 §3), so this
		// is the boundary doing its job, not a malfunction. The group name is
		// repeated from internal/lima.TorioMCPClientsGroup, which stays the
		// source of truth; a guest binary must not pull in the host adapter.
		return nil, &dialError{
			exit: exitDenied,
			msg: fmt.Sprintf("permission denied opening %s: this identity is not in the broker's client group "+
				"(torio-mcp-clients); membership is the whole privilege the agent identity is granted", path),
		}
	case errors.Is(err, syscall.ECONNREFUSED):
		return nil, &dialError{
			exit: exitBrokerDown,
			msg: fmt.Sprintf("connection refused at %s: the socket file is there but nothing is listening — "+
				"the broker is stopped or crashed and left its socket behind (check the broker unit)", path),
		}
	}
	return nil, &dialError{exit: exitInternal, msg: fmt.Sprintf("cannot connect to %s: %v", path, err)}
}
