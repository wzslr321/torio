package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"syscall"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// Neither the socket address rule nor the service-name rule is defined here.
// Both live in internal/mcpbroker (SocketPath / ValidateServiceName), because
// this binary and the broker own opposite halves of one path: the broker
// decides what it binds, this decides what may be asked for. Two copies of the
// rule would agree until one was widened, and the symptom would not be a
// rejected name — it would be a socket one side creates and the other cannot
// address.

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
	// The path is safe to print: it is mcpbroker.SocketDir joined with a validated slug,
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
		// The socket is 0660 torio-mcp:torio-mcp-clients (ADR-0004 §3), so this
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
				"by that group — `torio mcp status` distinguishes the two", path, mcpbroker.SocketDir),
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
