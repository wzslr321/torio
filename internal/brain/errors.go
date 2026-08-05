package brain

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzslr321/torio/internal/lima"
)

// ErrorKind is the stable classification consumed by internal/cli.
type ErrorKind string

const (
	KindPrecondition ErrorKind = "precondition"
	KindConflict     ErrorKind = "conflict"
	KindVerification ErrorKind = "verification"
	KindGit          ErrorKind = "git_failed"
	KindRegistration ErrorKind = "registration_failed"
	KindGuestCommand ErrorKind = "guest_command_failed"
	KindTransport    ErrorKind = "transport"
	KindTimeout      ErrorKind = "timeout"
	KindCancelled    ErrorKind = "cancelled"
)

// Error contains only bounded, payload-free diagnostics.
type Error struct {
	Op   string
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("brain %s: %s: %v", e.Op, e.Kind, e.Err)
	}
	return fmt.Sprintf("brain %s: %s", e.Op, e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

func fromGuestErr(op string, err error) *Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Op: op, Kind: KindTimeout, Err: err}
	case errors.Is(err, context.Canceled):
		return &Error{Op: op, Kind: KindCancelled, Err: err}
	}
	var lerr *lima.Error
	if errors.As(err, &lerr) {
		return &Error{Op: op, Kind: KindTransport, Err: err}
	}
	return &Error{Op: op, Kind: KindTransport, Err: err}
}

func commandError(op string, kind ErrorKind, action string, exitCode int) *Error {
	return &Error{
		Op:   op,
		Kind: kind,
		Err:  fmt.Errorf("%s exited %d", action, exitCode),
	}
}
