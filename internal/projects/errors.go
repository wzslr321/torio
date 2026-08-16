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
	// KindAuth is a remote the guest cannot read noninteractively. Torio may
	// provision a guest deploy key, but forge authorization remains a human act.
	KindAuth ErrorKind = "auth"
	// KindConflict is existing state Torio refuses to adopt or overwrite.
	KindConflict ErrorKind = "conflict"
	// KindGuestCommand is a guest command that failed for its own reasons.
	KindGuestCommand ErrorKind = "guest_command_failed"
	// KindGit is a Git operation that failed.
	KindGit ErrorKind = "git_failed"
	// KindRegistration is a backend registration failing or reporting a state we
	// cannot verify.
	KindRegistration ErrorKind = "registration_failed"
	// KindNoRegistry is asking Torio to drive a project registry the backend
	// declares it has not got. It mirrors serve's no_service: managing an
	// undeclared capability is an operator error naming the backend, not a guest
	// that failed, and it must not be reported as one.
	KindNoRegistry ErrorKind = "no_registry"
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
	// Issues names the stable markers a verification failure found, in the
	// order CheckoutStatus reports them. It is carried as data rather than left
	// in the message because a caller has to be able to tell one drift from
	// another without reading prose: an absent checkout is answerable and every
	// other drift is a tree only a human may touch (ADR-0024).
	Issues []string
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

// IsCheckoutAbsentOnly reports whether err is a session drift whose single
// cause is that no checkout is there.
//
// It is deliberately exact. A checkout that exists and disagrees with the
// record, or one whose permissions drifted, is a tree Torio will not touch, and
// treating either as "absent" would clone over an operator's work. Only the
// state that nothing exists yet can be answered by making it exist.
func IsCheckoutAbsentOnly(err error) bool {
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindVerification {
		return false
	}
	return len(perr.Issues) == 1 && perr.Issues[0] == issueCheckoutAbsent
}
