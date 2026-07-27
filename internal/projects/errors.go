package projects

import (
	"context"
	"errors"
	"fmt"
)

// ErrorKind is the stable classification consumed by internal/cli. It is a
// closed set: a caller maps a kind to an exit code and a remediation, never a
// message substring.
type ErrorKind string

const (
	// KindInvalidConfig is operator input the config layer rejects: a bad slug,
	// display name, or a remote that could carry a credential.
	KindInvalidConfig ErrorKind = "invalid_config"
	// KindConfigWrite is the persistence half of the same boundary: the document
	// was valid but could not be written. It is separate because the remedy is
	// different — a rerun finishes, rather than a value needing correction.
	KindConfigWrite ErrorKind = "config_write_failed"
	// KindPrecondition is a guest that is not ready: no Running VM, no verified
	// bootstrap, no passwordless sudo.
	KindPrecondition ErrorKind = "precondition"
	// KindAuth is a remote the guest cannot read noninteractively. Torio never
	// stores or configures credentials, so this is always a human action.
	KindAuth ErrorKind = "auth"
	// KindConflict is existing state Torio refuses to adopt or overwrite.
	KindConflict ErrorKind = "conflict"
	// KindGuestCommand is a guest command that failed for its own reasons.
	KindGuestCommand ErrorKind = "guest_command_failed"
	// KindGit is a Git operation that failed.
	KindGit ErrorKind = "git_failed"
	// KindRegistration is the Hermes project CLI failing or reporting a state we
	// cannot verify.
	KindRegistration ErrorKind = "registration_failed"
	// KindVerification is a postcondition that did not hold, or state we could
	// not determine. It is the fail-closed default.
	KindVerification ErrorKind = "verification"
	// KindTransport is a failure of the guest transport itself.
	KindTransport ErrorKind = "transport"
	// KindTimeout / KindCancelled carry the context outcome.
	KindTimeout   ErrorKind = "timeout"
	KindCancelled ErrorKind = "cancelled"
)

// Error contains only bounded, payload-free diagnostics: an operation, a kind,
// and a message built from fixed text plus already-validated identifiers. Guest
// stdout/stderr is never interpolated, so no secret can travel out this way.
type Error struct {
	Op   string
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("project %s: %s: %v", e.Op, e.Kind, e.Err)
	}
	return fmt.Sprintf("project %s: %s", e.Op, e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

// errInvalidID is the containment failure derivePath reports. It never echoes
// the offending value: an ID that fails containment is not a value to print
// back at a terminal.
var errInvalidID = errors.New("project id does not derive a contained workspace path")

func fromGuestErr(op string, err error) *Error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Op: op, Kind: KindTimeout, Err: err}
	case errors.Is(err, context.Canceled):
		return &Error{Op: op, Kind: KindCancelled, Err: err}
	}
	return &Error{Op: op, Kind: KindTransport, Err: err}
}

// commandError reports a failed guest command by action and exit code only.
func commandError(op string, kind ErrorKind, action string, exitCode int) *Error {
	return &Error{Op: op, Kind: kind, Err: fmt.Errorf("%s exited %d", action, exitCode)}
}
