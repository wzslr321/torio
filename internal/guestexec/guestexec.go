// Package guestexec holds the guest-command building blocks shared by the
// packages that drive fixed argv through the VM transport (projects, brain,
// serve): the sudo argv builders, the truncation-refusing run wrappers, and the
// stat-output parser. Error classification stays in each caller — this package
// reports what happened, never which ErrorKind it maps to.
package guestexec

import (
	"context"
	"errors"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// Transport is the one-shot guest command channel (satisfied by *lima.Adapter).
type Transport interface {
	SSH(ctx context.Context, command []string) (execx.Result, error)
}

// InputTransport is Transport with a fed standard input.
type InputTransport interface {
	SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error)
}

// ErrTruncated reports that bounded guest output was cut. Truncated output is
// not evidence of anything, so every caller must refuse it rather than parse
// it; callers detect it with errors.Is and map it to their own error kind.
var ErrTruncated = errors.New("bounded guest output was truncated")

// Run executes argv through the transport, failing closed on truncated output.
// A transport failure is returned as-is; a clean non-zero exit is the caller's
// to interpret.
func Run(ctx context.Context, g Transport, argv []string) (execx.Result, error) {
	res, err := g.SSH(ctx, argv)
	if err != nil {
		return execx.Result{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, ErrTruncated
	}
	return res, nil
}

// RunInput is Run with a fed standard input.
func RunInput(ctx context.Context, g InputTransport, stdin []byte, argv []string) (execx.Result, error) {
	res, err := g.SSHInput(ctx, stdin, argv)
	if err != nil {
		return execx.Result{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, ErrTruncated
	}
	return res, nil
}

// RootExec builds `sudo -n -- <args...>`: a system command run with root.
func RootExec(args ...string) []string {
	return append([]string{"sudo", "-n", "--"}, args...)
}

// UserExec builds `sudo -n -u hermes -- <args...>`: a guest command run as the
// hermes service identity.
func UserExec(args ...string) []string {
	return UserExecAs(lima.HermesUser, args...)
}

// UserExecAs builds `sudo -n -u <user> -- <args...>`.
func UserExecAs(user string, args ...string) []string {
	return append([]string{"sudo", "-n", "-u", user, "--"}, args...)
}

// ParseOwnershipMode parses `stat -c '%U:%G %a'` output. Unparseable input
// returns three empty strings, which no expected owner/group/mode matches, so
// a caller comparing against a spec fails closed without a separate check.
func ParseOwnershipMode(out string) (owner, group, mode string) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return "", "", ""
	}
	parts := strings.SplitN(fields[0], ":", 2)
	if len(parts) != 2 {
		return "", "", ""
	}
	return parts[0], parts[1], fields[1]
}
