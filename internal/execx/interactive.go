package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/wzslr321/torio/internal/redact"
)

// InteractiveCommand describes a single interactive external command. It is
// deliberately narrower than Command: there is no Stdin payload (the parent's
// own standard input is the payload), no Dir, and no Timeout — an operator
// session ends when the operator ends it, never on a deadline.
type InteractiveCommand struct {
	// Name is the executable, resolved via PATH. It is never a shell string.
	Name string
	// Args is the argument array passed verbatim to the executable.
	Args []string
	// Env, if non-nil, replaces the process environment. Nil inherits the
	// parent's environment, which is what an interactive session needs so
	// SSH_AUTH_SOCK, TERM and locale reach the child unchanged.
	Env []string
}

// InteractiveRunner runs a command that owns the operator's terminal. It is an
// interface so callers can be tested against a fake without spawning anything.
type InteractiveRunner interface {
	RunInteractive(ctx context.Context, cmd InteractiveCommand) error
}

// ExitError reports that an interactive child ran to completion and exited
// non-zero. The interactive path has no Result to carry an exit code, so the
// code travels as a typed error the caller matches with errors.As. Its message
// carries the code and nothing else — never argv, environment, or output.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("interactive command exited with code %d", e.Code)
}

// InteractiveExecRunner is the real InteractiveRunner backed by os/exec.
type InteractiveExecRunner struct {
	// Redactor, if set, redacts diagnostics in addition to the default
	// well-known secret shapes. There is no retained output to redact: this
	// runner never sees the child's streams.
	Redactor *redact.Redactor
}

// RunInteractive runs cmd with the parent's standard input, output and error
// wired straight through to the child, and returns when the child exits. A
// clean exit returns nil; a non-zero exit returns an *ExitError with the code;
// a cancelled context returns an error wrapping ctx.Err().
//
// It is the sibling of ExecRunner.Run, not a replacement: that contract —
// captured, bounded, redacted output and no TTY or parent stdin — is right for
// a command Torio inspects, and wrong for a session the operator drives. Here
// there is no Result, no output bound, and no timeout, because there is nothing
// to inspect and nobody to hurry.
func (r *InteractiveExecRunner) RunInteractive(ctx context.Context, cmd InteractiveCommand) error {
	if strings.TrimSpace(cmd.Name) == "" {
		return errors.New("execx: empty interactive command name")
	}

	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	if cmd.Env != nil {
		c.Env = cmd.Env
	}
	// The whole point of this runner: the child gets the parent's real
	// descriptors, so a TTY stays a TTY and the operator's keystrokes reach
	// the remote shell. Nothing here captures, buffers, bounds or logs them.
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	// Deliberately no process group of its own (unlike ExecRunner): the child
	// stays in the terminal's foreground group, so the tty delivers Ctrl-C and
	// Ctrl-\ straight to it. An operator interrupting a remote command expects
	// the remote command to be interrupted, not torio.
	//
	// Our only job with those signals is to not die first and abandon a live
	// session. signal.Notify installs a Go handler — never signal.Ignore, whose
	// SIG_IGN disposition would be inherited across exec and leave the remote
	// shell deaf to Ctrl-C. The buffered channel is never drained on purpose:
	// the signals are the child's business.
	//
	// Sharing the group is also why cancellation kills the direct child only:
	// ExecRunner's group kill would take this process, and the operator's
	// terminal session, down with it.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGQUIT)
	defer signal.Stop(sigs)

	runErr := c.Run()
	if runErr == nil {
		return nil
	}

	// A cancelled context takes precedence over the raw run error: the session
	// was torn down, it did not exit, and reporting it as an exit status would
	// invent a remote failure that never happened.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("interactive %s: %w", r.redact(cmd.Name), ctxErr)
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return &ExitError{Code: exitErr.ExitCode()}
	}

	// Any other failure (typically: the executable could not be started). The
	// diagnostic names the executable and nothing else — an operator session's
	// argv and environment are the credential-bearing parts — and even that is
	// redacted.
	return fmt.Errorf("interactive %s: %s", r.redact(cmd.Name), r.redact(runErr.Error()))
}

// redact applies the configured redactor (or the default) to s.
func (r *InteractiveExecRunner) redact(s string) string {
	if r.Redactor != nil {
		return r.Redactor.String(s)
	}
	return redact.String(s)
}
