package cli

import (
	"fmt"
	"io"

	"github.com/wzslr321/torio/internal/redact"
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
	// ExitPrecondition is an unmet precondition (VM stopped, Brain absent).
	ExitPrecondition ExitCode = 3
	// 4 was "policy denied" in the pre-V0 worker platform (forbidden
	// mount/tool/skill). V1 has no policy engine to deny anything, so no
	// command produces it. The number stays unused rather than reassigned: the
	// table is a contract, and reusing a code would silently change what an
	// existing 4 meant.
	//
	// ExitConflict is a stale/conflict state (an id or remote already taken).
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
	// Command, when non-empty, is the concrete envelope command name for this
	// error (e.g. "vm.status"), so a failing command's error envelope matches
	// its success envelope. Empty falls back to the first-non-flag arg, which
	// is all an early parse/usage error (before dispatch) can know.
	Command string
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
// redaction (AGENTS §6, TM-12): the command name, the message and all detail
// values are scrubbed of known secret shapes, so no secret can escape through
// an error path even if an upstream layer missed it.
func fail(stdout, stderr io.Writer, command string, jsonOut bool, err *CLIError) int {
	command = redact.String(command)
	msg := redact.String(err.Message)
	details := redactDetails(err.Details)

	if jsonOut {
		env := errorEnvelope(command, &EnvelopeError{
			Code:    err.Code,
			Message: msg,
			Details: details,
		})
		// A write failure here cannot be reported through stdout without
		// corrupting the envelope; surface it on stderr and keep the exit code.
		if werr := writeJSON(stdout, env); werr != nil {
			fmt.Fprintf(stderr, "torio: failed to write error envelope: %v\n", werr)
		}
		return int(err.Exit)
	}
	fmt.Fprintf(stderr, "torio: %s\n", msg)
	return int(err.Exit)
}

// redactDetails returns a copy of details with string values redacted. Nested
// maps are redacted recursively; other value types are left as-is.
func redactDetails(details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	out := make(map[string]any, len(details))
	for k, v := range details {
		switch vv := v.(type) {
		case string:
			out[k] = redact.String(vv)
		case map[string]any:
			out[k] = redactDetails(vv)
		default:
			out[k] = v
		}
	}
	return out
}
