package main

import (
	"context"
	"io"
)

type halfCloser interface {
	io.ReadWriteCloser
	CloseWrite() error
}

func relay(ctx context.Context, conn halfCloser, stdin io.Reader, stdout io.Writer) error {
	released := make(chan struct{})
	defer close(released)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-released:
		}
	}()
	go func() {
		_, _ = io.Copy(conn, stdin)
		_ = conn.CloseWrite()
	}()
	_, err := io.Copy(stdout, conn)
	_ = conn.Close()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
