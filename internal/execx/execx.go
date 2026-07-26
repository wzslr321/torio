// Package execx is the typed, testable boundary for running external commands.
// Every command is expressed as an executable name plus an argument array and
// run with os/exec.CommandContext — never through a shell (no `sh -c`) — with
// an explicit context, an optional timeout, captured exit code, retained output
// that is bounded per stream and redacted, and diagnostics that are redacted
// before they reach a caller (AGENTS §6).
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"hermes-box.local/hb/internal/redact"
)

// DefaultMaxOutputPerStream bounds retained stdout/stderr per command when
// ExecRunner.MaxOutputPerStream is not set, so a runaway child cannot exhaust
// memory. Output beyond the bound is discarded and flagged as truncated.
const DefaultMaxOutputPerStream = 1 << 20 // 1 MiB

// killGraceDelay is how long Wait tolerates leftover I/O after the context is
// cancelled before the process (tree) is force-killed and pipes are closed.
const killGraceDelay = 2 * time.Second

// Command describes a single external command to run.
type Command struct {
	// Name is the executable, resolved via PATH. It is never a shell string.
	Name string
	// Args is the argument array passed verbatim to the executable.
	Args []string
	// Dir, if set, is the working directory.
	Dir string
	// Env, if non-nil, replaces the process environment (nil inherits).
	Env []string
	// Stdin, if non-nil, is fed verbatim to the child's standard input and then
	// closed (the child sees EOF). It is the no-shell primitive for writing a
	// generated file onto a host via a filter like `tee FILE`. Nil leaves stdin
	// empty (immediate EOF), never wired to the parent's stdin.
	Stdin []byte
	// Timeout, if > 0, bounds this command independently of the caller's ctx.
	Timeout time.Duration
}

// Result is the captured outcome of a completed command. Stdout and Stderr are
// bounded per stream and redacted.
type Result struct {
	// ExitCode is the process exit code, or -1 if the process did not exit
	// normally (for example it was killed on timeout).
	ExitCode int
	// Stdout/Stderr are the retained (bounded, redacted) child output.
	Stdout []byte
	Stderr []byte
	// StdoutTruncated/StderrTruncated report that the child produced more than
	// the per-stream bound and the excess was discarded.
	StdoutTruncated bool
	StderrTruncated bool
}

// Runner runs external commands. It is an interface so adapters can be tested
// against a fake without touching the host.
type Runner interface {
	Run(ctx context.Context, cmd Command) (Result, error)
}

// ExecRunner is the real Runner backed by os/exec.
type ExecRunner struct {
	// Redactor, if set, redacts retained output and diagnostics in addition to
	// the default well-known secret shapes.
	Redactor *redact.Redactor
	// MaxOutputPerStream bounds retained stdout/stderr per command. If <= 0,
	// DefaultMaxOutputPerStream is used.
	MaxOutputPerStream int
}

// Run executes cmd. A process that runs to completion — even with a non-zero
// exit — returns a populated Result and a nil error; the exit code is the
// caller's to interpret. A start failure, a timeout, or a cancellation returns
// a redacted, wrapped error. Context/timeout errors are reported as such so
// callers can distinguish them with errors.Is. On timeout/cancellation the
// spawned process tree is cleaned up where the host platform supports it (see
// processGroupSupported).
func (r *ExecRunner) Run(ctx context.Context, cmd Command) (Result, error) {
	if strings.TrimSpace(cmd.Name) == "" {
		return Result{ExitCode: -1}, errors.New("execx: empty command name")
	}

	runCtx := ctx
	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	c := exec.CommandContext(runCtx, cmd.Name, cmd.Args...)
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}
	if cmd.Env != nil {
		c.Env = cmd.Env
	}
	if cmd.Stdin != nil {
		// A bytes.Reader delivers the payload and then EOF, so a filter like
		// `tee` writes exactly these bytes and exits. We never wire the parent's
		// os.Stdin to the child.
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}

	// Put the child in its own process group and, on cancellation, kill the
	// whole group so descendants do not leak. WaitDelay is a backstop that
	// force-closes pipes if I/O outlives the kill.
	setProcessGroup(c)
	c.Cancel = func() error { return killProcessGroup(c) }
	c.WaitDelay = killGraceDelay

	limit := r.MaxOutputPerStream
	if limit <= 0 {
		limit = DefaultMaxOutputPerStream
	}
	stdout := &capWriter{limit: limit}
	stderr := &capWriter{limit: limit}
	c.Stdout = stdout
	c.Stderr = stderr

	runErr := c.Run()

	outBytes, outTrunc := stdout.snapshot()
	errBytes, errTrunc := stderr.snapshot()
	res := Result{
		ExitCode:        -1,
		Stdout:          []byte(r.redact(string(outBytes))),
		Stderr:          []byte(r.redact(string(errBytes))),
		StdoutTruncated: outTrunc,
		StderrTruncated: errTrunc,
	}
	if c.ProcessState != nil {
		res.ExitCode = c.ProcessState.ExitCode()
	}

	if runErr == nil {
		return res, nil
	}

	// A context deadline/cancellation takes precedence over the raw run error.
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return res, fmt.Errorf("run %s: %w", r.describe(cmd), ctxErr)
	}

	// A clean non-zero exit is not a runner error; the caller reads ExitCode.
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return res, nil
	}

	// Any other failure (e.g. executable not found) is reported, redacted.
	return res, fmt.Errorf("run %s: %s", r.describe(cmd), r.redact(runErr.Error()))
}

// capWriter retains up to limit bytes and discards the rest, recording whether
// truncation occurred. It never errors, so os/exec keeps draining the pipe to
// EOF and the child does not block. It is safe for concurrent writes (os/exec
// copies on its own goroutine) and for a snapshot read after Wait returns.
type capWriter struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
	total int
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total += len(p)
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(p) <= room {
			w.buf.Write(p)
		} else {
			w.buf.Write(p[:room])
		}
	}
	return len(p), nil
}

// snapshot returns a copy of the retained bytes and whether truncation occurred.
func (w *capWriter) snapshot() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, w.buf.Len())
	copy(out, w.buf.Bytes())
	return out, w.total > w.limit
}

// describe renders a redacted "name arg1 arg2 …" description for diagnostics.
func (r *ExecRunner) describe(cmd Command) string {
	parts := make([]string, 0, len(cmd.Args)+1)
	parts = append(parts, cmd.Name)
	parts = append(parts, cmd.Args...)
	if r.Redactor != nil {
		parts = r.Redactor.Slice(parts)
	} else {
		parts = redact.Slice(parts)
	}
	return strings.Join(parts, " ")
}

// redact applies the configured redactor (or the default) to s.
func (r *ExecRunner) redact(s string) string {
	if r.Redactor != nil {
		return r.Redactor.String(s)
	}
	return redact.String(s)
}
