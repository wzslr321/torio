// Command torio-mcp-connect is the credential-free stdio adapter between an
// agent backend and the Torio MCP broker's Unix socket (ADR-0012). It parses,
// filters and logs no MCP content; the broker is the control.
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

const (
	exitOK         = 0
	exitInternal   = 1
	exitUsage      = 2
	exitNoBroker   = 3
	exitDenied     = 7
	exitBrokerDown = 8
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], socketDir, os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, base string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usage(stderr, "expects exactly one service name")
	}
	path, err := socketPath(base, args[0])
	if err != nil {
		return usage(stderr, err.Error())
	}
	conn, err := dial(path)
	if err != nil {
		return fail(stderr, err)
	}
	defer conn.Close()
	if err := relay(ctx, conn, stdin, stdout); err != nil {
		return fail(stderr, errors.New("relay session failed"))
	}
	return exitOK
}

func usage(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "torio-mcp-connect: %s\nusage: torio-mcp-connect <service>\n", message)
	return exitUsage
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "torio-mcp-connect: %v\n", err)
	var classified *dialError
	if errors.As(err, &classified) {
		return classified.exit
	}
	return exitInternal
}
