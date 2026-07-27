package lima

import (
	"context"
	"errors"
	"fmt"
)

// ErrorKind classifies an adapter failure so callers (eventually internal/cli)
// can map it onto the exit-code contract without string matching.
type ErrorKind string

const (
	// KindBinaryUnavailable means limactl could not be started at all (not
	// found, permission denied, etc.) — distinct from a command that ran and
	// exited non-zero.
	KindBinaryUnavailable ErrorKind = "binary_unavailable"
	// KindCommandFailed means limactl ran and exited non-zero.
	KindCommandFailed ErrorKind = "command_failed"
	// KindMalformedOutput means limactl exited zero but its output could not
	// be parsed into the expected shape (version string, JSON instance
	// record, known status value).
	KindMalformedOutput ErrorKind = "malformed_output"
	// KindVersionMismatch means the probed version does not match a
	// non-empty VersionLock.Lima pin.
	KindVersionMismatch ErrorKind = "version_mismatch"
	// KindTimeout means the operation's context deadline was exceeded.
	KindTimeout ErrorKind = "timeout"
	// KindCancelled means the operation's context was cancelled.
	KindCancelled ErrorKind = "cancelled"
	// KindNotFound means the target VM instance does not exist yet.
	KindNotFound ErrorKind = "not_found"
	// KindNotRunning means the instance exists but is not Running when an
	// operation (bootstrap) requires a verified Running precondition. Distinct
	// from KindAmbiguousState (Broken/Unknown): a Stopped instance is a
	// well-understood, non-ambiguous state that simply fails the precondition.
	KindNotRunning ErrorKind = "not_running"
	// KindAmbiguousState means the instance exists but is in a state
	// (Broken/Unknown) that must not be silently mutated.
	KindAmbiguousState ErrorKind = "ambiguous_state"
	// KindVerificationFailed means the adapter reached the guest but a proven
	// postcondition was false — an architecture/version/mount/filesystem drift,
	// a required guest reconcile step that could not be established, or an
	// unverifiable state. It fails closed (docs/contracts/cli.md exit class 6,
	// "verification failed"), distinct from KindPostconditionFailed which is a
	// lifecycle mutation (start/stop) whose re-queried state did not confirm.
	KindVerificationFailed ErrorKind = "verification_failed"
	// KindPostconditionFailed means a mutating command (start) exited zero,
	// but re-querying the instance did not confirm the expected resulting
	// state. A clean exit code alone is never sufficient proof of a
	// state-changing operation (see docs/contracts/cli.md's Hermes-adapter
	// mutation-postcondition rule, applied here to limactl).
	KindPostconditionFailed ErrorKind = "postcondition_failed"
	// KindIncompatible means InstanceName already exists but its trusted pins
	// (image digest/URL, empty mounts, forwardAgent=false, vz/aarch64) do not
	// match the embedded V1 template. Init fails closed — no recreate/reset/delete.
	KindIncompatible ErrorKind = "incompatible"
)

// Error is a categorized adapter failure. Its message and wrapped Err are
// already redacted: execx redacts retained output and diagnostics before
// they reach this package (AGENTS §6), so Error never needs to redact again.
type Error struct {
	// Op names the adapter operation ("probe", "status", "start", "ssh").
	Op string
	// Kind classifies the failure for CLI exit-code mapping.
	Kind ErrorKind
	// Err is the underlying, already-redacted error, if any.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("lima %s: %s: %v", e.Op, e.Kind, e.Err)
	}
	return fmt.Sprintf("lima %s: %s", e.Op, e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

// classifyRunErr distinguishes a context timeout/cancellation from any other
// runner failure (binary missing, spawn failure). It relies on execx wrapping
// the context error with %w, so errors.Is finds it through the wrapping.
func classifyRunErr(op string, err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Op: op, Kind: KindTimeout, Err: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Op: op, Kind: KindCancelled, Err: err}
	}
	return &Error{Op: op, Kind: KindBinaryUnavailable, Err: err}
}

// commandFailed builds a KindCommandFailed error from a non-zero exit.
func commandFailed(op string, exitCode int, stderr []byte) *Error {
	return &Error{Op: op, Kind: KindCommandFailed, Err: fmt.Errorf("exit %d: %s", exitCode, string(stderr))}
}
