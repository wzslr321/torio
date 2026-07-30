package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"syscall"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// socketDir is where the broker publishes one socket per service (ADR-0022 §3).
// It is fixed in the binary: the guest layout is Torio's, not the caller's, and
// an overridable base would let anything that can set argv or the environment
// point the relay at a socket of its own.
const socketDir = "/run/torio-mcp"

// socketSuffix keeps the service name and the file name distinct, so a name is
// never mistaken for a whole path.
const socketSuffix = ".sock"

// The service-name rule is NOT defined here. It lives in
// internal/mcpbroker.ValidateServiceName, because this binary and the policy
// loader own opposite halves of one path: the loader decides what the broker
// binds, this decides what may be asked for. Two copies of the rule would agree
// until one was widened, and the symptom would not be a rejected name — it
// would be a socket one side creates and the other cannot address. The bound in
// that rule also keeps the resolved path inside the kernel's ~104-byte sun_path
// limit: socketDir plus the longest accepted name plus socketSuffix is 52
// bytes, so an over-long address is unreachable by construction rather than an
// EINVAL at connect time.

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
	if err := mcpbroker.ValidateServiceName(service); err != nil {
		return "", err
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
				"or it has never started (run `torio mcp install` on the host, then check the broker unit)", path),
		}
	case errors.Is(err, fs.ErrPermission):
		// The socket is 0660 torio-mcp:torio-mcp-clients (ADR-0022 §3), so this
		// is the boundary doing its job, not a malfunction. The group name is
		// repeated from internal/lima.TorioMCPClientsGroup, which stays the
		// source of truth; a guest binary must not pull in the host adapter.
		//
		// Two different faults produce this errno and the relay cannot tell them
		// apart from here: this identity being outside the group, or the
		// directory above the socket being handed to some other group, which
		// denies a caller who is a member. Naming only the first would send an
		// operator to check something that is already correct — so both are
		// named, and neither is asserted as the cause.
		return nil, &dialError{
			exit: exitDenied,
			msg: fmt.Sprintf("permission denied opening %s: either this identity is not in the broker's client group "+
				"(torio-mcp-clients), which is the whole privilege the agent identity is granted, or %s is not traversable "+
				"by that group — `torio mcp status` distinguishes the two", path, socketDir),
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
