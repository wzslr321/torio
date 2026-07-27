package serve

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// ErrorKind classifies a serve-lifecycle failure so internal/cli can map it onto
// the exit-code contract (docs/contracts/cli.md) without string matching.
type ErrorKind string

const (
	// KindTransport means the guest transport (limactl shell) could not run at
	// all — binary missing / spawn failure. External dependency (exit 8).
	KindTransport ErrorKind = "transport"
	// KindTimeout means the operation's context deadline was exceeded. Exit 8.
	KindTimeout ErrorKind = "timeout"
	// KindCancelled means the operation's context was cancelled. Exit 8.
	KindCancelled ErrorKind = "cancelled"
	// KindGuestCommandFailed means a required guest command (systemctl, mv,
	// mkdir, loginctl, id) exited non-zero unexpectedly. External (exit 8).
	KindGuestCommandFailed ErrorKind = "guest_command_failed"
	// KindNotInstalled means the unit is not installed when an operation requires
	// it. Unmet precondition (exit 3).
	KindNotInstalled ErrorKind = "not_installed"
	// KindInactive means the service is not active when the operation requires a
	// running backend (a bare `status` reports this rather than erroring). Unmet
	// precondition (exit 3).
	KindInactive ErrorKind = "inactive"
	// KindPostconditionFailed means a mutating command (start/stop/restart)
	// exited zero but the re-queried systemd state did not confirm the expected
	// result. A clean exit is never sufficient proof. Unmet precondition (exit 3).
	KindPostconditionFailed ErrorKind = "postcondition_failed"
	// KindEndpointUnready means systemd reports the service active but the
	// loopback readiness endpoint did not answer 200 — an active process with a
	// dead endpoint is a failure (docs/contracts/service-lifecycle.md).
	// Verification failed (exit 6).
	KindEndpointUnready ErrorKind = "endpoint_unready"
	// KindValidationFailed means the generated unit failed `systemd-analyze
	// verify` before activation. Verification failed (exit 6).
	KindValidationFailed ErrorKind = "validation_failed"
)

// Error is a categorized serve-lifecycle failure. Its message and wrapped Err
// are already redacted: execx redacts retained output and diagnostics before
// they reach this package (AGENTS §6), so Error never needs to redact again.
type Error struct {
	// Op names the operation ("install", "start", "stop", "restart", "status",
	// "logs").
	Op string
	// Kind classifies the failure for CLI exit-code mapping.
	Kind ErrorKind
	// Err is the underlying, already-redacted error, if any.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("serve %s: %s: %v", e.Op, e.Kind, e.Err)
	}
	return fmt.Sprintf("serve %s: %s", e.Op, e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

// fromGuestErr maps a transport failure returned by the Guest into a serve
// Error. The Guest is *lima.Adapter in production, which already classifies
// timeout/cancel/binary; we preserve timeout/cancel (so the CLI can distinguish
// them) and fold everything else into KindTransport. All map to exit 8.
func fromGuestErr(op string, err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Op: op, Kind: KindTimeout, Err: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Op: op, Kind: KindCancelled, Err: err}
	}
	// A classified *lima.Error carries its own kind; still fold to transport for
	// the serve surface, keeping the underlying (redacted) message.
	var lerr *lima.Error
	if errors.As(err, &lerr) {
		return &Error{Op: op, Kind: KindTransport, Err: err}
	}
	return &Error{Op: op, Kind: KindTransport, Err: err}
}

// cmdErr builds a bounded, already-redacted underlying error from a non-zero
// guest command. Output is capped so an unexpected blob cannot enter the error.
func cmdErr(what string, res execx.Result) error {
	return fmt.Errorf("%s exited %d: %s", what, res.ExitCode, bound(string(res.Stderr)))
}

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// bound caps a derived string so an error/detail can never carry an unbounded
// blob into the JSON envelope.
func bound(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
