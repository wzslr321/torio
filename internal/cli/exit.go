package cli

import (
	"fmt"
	"io"

	"hermes-box.local/hb/internal/redact"
)

// ExitCode is the stable process exit-code mapping defined in
// docs/contracts/cli.md. The numeric values are a contract and must not drift.
type ExitCode int

const (
	// ExitOK is success, including idempotent success.
	ExitOK ExitCode = 0
	// ExitInternal is an uncategorized internal error. It is intentionally not
	// one of the contract's defined error classes (2-9); the contract table
	// does not define 1, so it is reserved for unexpected failures.
	ExitInternal ExitCode = 1
	// ExitUsage is a usage or schema-validation error (missing arg, invalid config).
	ExitUsage ExitCode = 2
	// ExitPrecondition is an unmet precondition (VM stopped, task not frozen).
	ExitPrecondition ExitCode = 3
	// ExitPolicy is a policy denial (forbidden mount/tool/skill).
	ExitPolicy ExitCode = 4
	// ExitConflict is a stale/conflict state (base/candidate/policy changed).
	ExitConflict ExitCode = 5
	// ExitVerification is a verification failure (trusted check exit != 0).
	ExitVerification ExitCode = 6
	// ExitPermission is a permission/capability denial (brain attempts admin action).
	ExitPermission ExitCode = 7
	// ExitExternal is an external dependency failure (Hermes/Docker/Git unavailable).
	ExitExternal ExitCode = 8
	// ExitReconcile signals reconciliation is required (state/resource disagreement).
	ExitReconcile ExitCode = 9
)

// CLIError is a categorized command error. Its Exit field maps directly onto
// the contract exit-code table and its Code/Message populate the JSON envelope.
// Message must never contain secrets.
type CLIError struct {
	Exit    ExitCode
	Code    string
	Message string
	Details map[string]any
}

func (e *CLIError) Error() string { return e.Message }

// usageError constructs a usage (exit 2) error.
func usageError(msg string) *CLIError {
	return &CLIError{Exit: ExitUsage, Code: "USAGE", Message: msg}
}

// internalError constructs an uncategorized internal (exit 1) error.
func internalError(msg string) *CLIError {
	return &CLIError{Exit: ExitInternal, Code: "INTERNAL", Message: msg}
}

// fail renders err to the correct stream and returns its exit code. In JSON
// mode a single error envelope is written to stdout; otherwise a diagnostic
// line is written to stderr. Either way stdout stays free of mixed content.
//
// This is the final renderer, so it is also the last line of defense for
// redaction (AGENTS §6, TM-12): the message and all detail values are scrubbed
// of known secret shapes and, if red is non-nil, its registered literals — so
// no secret can escape through an error path even if an upstream layer missed
// it. red may be nil, in which case only known shapes are redacted.
func fail(stdout, stderr io.Writer, command string, jsonOut bool, err *CLIError, red *redact.Redactor) int {
	command = redactString(red, command)
	msg := redactString(red, err.Message)
	details := redactDetails(red, err.Details)

	if jsonOut {
		env := errorEnvelope(command, &EnvelopeError{
			Code:    err.Code,
			Message: msg,
			Details: details,
		})
		// A write failure here cannot be reported through stdout without
		// corrupting the envelope; surface it on stderr and keep the exit code.
		if werr := writeJSON(stdout, env); werr != nil {
			fmt.Fprintf(stderr, "hb: failed to write error envelope: %v\n", werr)
		}
		return int(err.Exit)
	}
	fmt.Fprintf(stderr, "hb: %s\n", msg)
	return int(err.Exit)
}

// redactString applies red's literals (if any) plus known secret shapes.
func redactString(red *redact.Redactor, s string) string {
	if red != nil {
		return red.String(s)
	}
	return redact.String(s)
}

// redactDetails returns a copy of details with string values redacted. Nested
// maps are redacted recursively; other value types are left as-is.
func redactDetails(red *redact.Redactor, details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	out := make(map[string]any, len(details))
	for k, v := range details {
		switch vv := v.(type) {
		case string:
			out[k] = redactString(red, vv)
		case map[string]any:
			out[k] = redactDetails(red, vv)
		default:
			out[k] = v
		}
	}
	return out
}
