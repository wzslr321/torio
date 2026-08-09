//go:build darwin

package sshagent

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/execx"
)

// osascriptProgram is fixed and complete. Nothing from a session is
// concatenated into it: the message is read from the environment at run time by
// `system attribute`, so a project name or a branch cannot become AppleScript.
//
// "Deny" is both the default and the cancel button, so Return, Escape and the
// window's close box all refuse. Approval is the only outcome that takes an
// aimed click.
const osascriptProgram = `display dialog (system attribute "` + promptEnvVar + `") ` +
	`with title "Torio — allow this signature?" ` +
	`buttons {"Deny", "Allow"} default button "Deny" cancel button "Deny" ` +
	`with icon caution`

// approvedAnswer is what osascript prints when Allow was clicked. It is matched
// in full rather than by substring on "Allow", because the message itself is not
// in this output but a future one might be.
const approvedAnswer = "button returned:Allow"

// askOperator opens the dialog and reports approval.
//
// A dialog that stands past the timeout is killed with the process that owns it,
// which is why the timeout is the command's rather than a deadline checked
// after: an expired prompt must leave no window on screen for someone to click
// later.
func askOperator(ctx context.Context, runner execx.Runner, timeout time.Duration, message string) error {
	result, err := runner.Run(ctx, execx.Command{
		Name:    "osascript",
		Args:    []string{"-e", osascriptProgram},
		Env:     withPromptMessage(os.Environ(), message),
		Timeout: timeout,
	})
	if err != nil {
		// A dialog that could not be shown is a denial, not an error to
		// escalate. The signature does not happen either way, and the operator
		// is at the terminal that will say so.
		return errDenied
	}
	if result.ExitCode != 0 {
		return errDenied
	}
	if !strings.Contains(string(result.Stdout), approvedAnswer) {
		return errDenied
	}
	return nil
}
