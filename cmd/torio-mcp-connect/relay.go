package main

import (
	"context"
	"io"
)

// halfCloser is the connection shape the relay needs: a duplex stream whose
// write side closes on its own. *net.UnixConn is one. Naming the requirement
// rather than the concrete type keeps the shutdown rules in one place and makes
// it obvious that half-close is not optional here.
type halfCloser interface {
	io.ReadWriteCloser
	CloseWrite() error
}

// relay copies bytes in both directions until the session ends.
//
// It parses nothing. The only thing it knows about MCP is which event ends
// which direction: stdin closing ends the request, the socket closing ends the
// answer. Anything more — framing, filtering, counting tool calls — would make
// this a place where upstream content could be read, and ADR-0004 §5 keeps
// content out of every Torio surface, not just the log.
func relay(ctx context.Context, conn halfCloser, stdin io.Reader, stdout io.Writer) error {
	// A signal cancels ctx, and closing the connection is what unblocks a copy
	// parked in a socket read or write. The watcher is released on every return
	// path, so it cannot outlive the call.
	released := make(chan struct{})
	defer close(released)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-released:
		}
	}()

	go func() {
		// A read error on stdin ends the request exactly as EOF does: either way
		// there is nothing further to send, and the broker has to be told before
		// it can answer.
		_, _ = io.Copy(conn, stdin)
		// Half-close, deliberately: the broker sees the end of the request while
		// its reply still has a way back. Closing outright here is the bug this
		// binary is most likely to have — it truncates in-flight responses, and
		// only under load, where they no longer fit in one buffer.
		_ = conn.CloseWrite()
	}()

	// The inbound direction defines the session. Waiting for it, rather than for
	// whichever direction finishes first, is what lets the broker finish
	// answering after Hermes has already stopped talking.
	_, err := io.Copy(stdout, conn)
	conn.Close()

	// The outbound copy is deliberately not joined. When the broker ends the
	// session first, that goroutine is parked in a read on stdin, and a blocking
	// read on the process's own stdin cannot be interrupted portably — waiting
	// for it would hang the relay exactly when the broker has gone away. Its
	// lifetime is the process, and there is one relay per process, so it is
	// bounded rather than leaked: nothing here runs a second session.

	if ctx.Err() != nil {
		// The session was cut short on purpose. The error the closed connection
		// produced describes the mechanism, not a failure worth reporting.
		return nil
	}
	return err
}
