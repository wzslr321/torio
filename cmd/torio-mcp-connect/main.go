// Command torio-mcp-connect is the stdio adapter Hermes spawns to reach the
// MCP broker (ADR-0004). It connects to the broker's unix socket for one
// service and copies bytes both ways, unchanged.
//
// # This binary is not a security control
//
// The agent runs under the same uid and can open the same socket itself, so
// nothing here can be relied upon to deny anything — and that is the designed
// arrangement, not a gap. The controls live elsewhere: the socket's
// ownership and mode decide who may connect at all, the kernel (not a presented
// secret) tells the broker who the peer is, and the broker's own policy decides
// what that peer may call. This binary exists for one reason: Hermes' MCP
// client speaks stdio and the broker listens on a unix socket.
//
// It follows that the relay must not authenticate, filter or inspect the
// traffic. Enforcement here would be theatre — bypassed by a direct connect —
// and reading MCP frames would put upstream Jira and Confluence content within
// reach of a log line, which ADR-0004 §5 forbids outright.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Exit codes follow the table in docs/contracts/cli.md so an operator debugging
// a guest reads one mapping, not two. Only the classes this binary can actually
// reach are named here.
const (
	// exitOK is a completed session: one side closed and the other drained.
	exitOK = 0
	// exitInternal is a failure that is none of the classified ones below. The
	// contract table leaves 1 undefined, which is what makes it the honest code
	// for "not a failure this binary claims to understand".
	exitInternal = 1
	// exitUsage is a bad invocation: wrong arity or a rejected service name.
	exitUsage = 2
	// exitNoBroker is an unmet precondition: there is no socket to connect to.
	exitNoBroker = 3
	// exitDenied is a capability denial: the socket exists and this identity may
	// not open it.
	exitDenied = 7
	// exitBrokerDown is an external dependency failure: the socket is there and
	// the broker behind it is not.
	exitBrokerDown = 8
)

func main() {
	// SIGINT/SIGTERM cancel the session context, which closes the connection and
	// ends both directions promptly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], socketDir, os.Stdin, os.Stdout, os.Stderr))
}

// run is the whole command with its I/O and socket directory injected, so tests
// drive it against a temp dir. socketBase is a parameter only for that reason;
// production always passes the fixed socketDir.
//
// stdout carries the MCP protocol stream and nothing else. Every diagnostic in
// this command goes to stderr, including usage.
func run(ctx context.Context, args []string, socketBase string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usage(stderr, fmt.Sprintf("expects exactly one argument, the MCP service name (got %d)", len(args)))
	}
	path, err := socketPath(socketBase, args[0])
	if err != nil {
		return usage(stderr, err.Error())
	}

	conn, err := dial(path)
	if err != nil {
		return fail(stderr, err)
	}
	defer conn.Close()

	if err := relay(ctx, conn, stdin, stdout); err != nil {
		return fail(stderr, fmt.Errorf("relaying %s failed: %w", args[0], err))
	}
	return exitOK
}

// fail reports err on stderr and returns its exit code. A classified dial
// failure carries its own code; anything else is unclassified by definition.
func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "torio-mcp-connect: %v\n", err)
	var de *dialError
	if errors.As(err, &de) {
		return de.exit
	}
	return exitInternal
}

// usage reports a bad invocation on stderr and returns the usage exit code.
func usage(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "torio-mcp-connect: %s\n", msg)
	fmt.Fprintf(stderr, "usage: torio-mcp-connect <service>\n")
	return exitUsage
}
