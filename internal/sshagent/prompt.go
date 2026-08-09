package sshagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/execx"
)

// DefaultConfirmTimeout bounds how long one dialog may stand unanswered.
//
// The session itself is deliberately unbounded — an operator session ends when
// the operator ends it — but a prompt is not the session. A dialog nobody
// answered, on a Mac nobody is sitting at, is a live grant waiting for whoever
// walks past, so it expires and the signature is denied.
const DefaultConfirmTimeout = 2 * time.Minute

// promptEnvVar carries the message to the platform dialog.
//
// It is an environment variable rather than an argument because the Darwin
// dialog is an AppleScript program, and a project name, branch or remote
// concatenated into a program is a program the guest helped write. The script
// is a fixed constant that reads this variable at run time.
const promptEnvVar = "TORIO_AGENT_PROMPT"

// DialogConfirmer asks the operator with the host's native dialog.
type DialogConfirmer struct {
	// Runner is the typed command boundary. It is a field so tests drive the
	// decision without a window ever opening.
	Runner execx.Runner
	// Timeout defaults to DefaultConfirmTimeout when zero.
	Timeout time.Duration
}

func (d DialogConfirmer) Confirm(ctx context.Context, req SignRequest) error {
	runner := d.Runner
	if runner == nil {
		runner = &execx.ExecRunner{}
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultConfirmTimeout
	}
	return askOperator(ctx, runner, timeout, promptMessage(req))
}

// promptMessage is what the operator reads.
//
// It says what Torio measured and then says what Torio does not know. The
// second half is not padding: a sign request carries no Git context at all, so a
// dialog that read "allow this push of 3 commits" would be claiming to have seen
// something it cannot see.
func promptMessage(req SignRequest) string {
	var b strings.Builder
	b.WriteString("A session is asking to use your SSH key.\n\n")
	writeField(&b, "project", req.Session.ProjectID)
	writeField(&b, "origin", req.Session.Host)
	writeField(&b, "branch", describeBranch(req.Session))
	writeField(&b, "key", describeKey(req.Identity))
	b.WriteString("\nTorio cannot see what the key will be used for. The host is\n")
	b.WriteString("where origin pushes, and the commit count is what the checkout\n")
	b.WriteString("held when the session opened. Allow only if you just asked\n")
	b.WriteString("for this.")
	return b.String()
}

func writeField(b *strings.Builder, label, value string) {
	if value == "" {
		value = "unknown"
	}
	fmt.Fprintf(b, "%-8s %s\n", label, value)
}

func describeBranch(session SessionContext) string {
	if session.Branch == "" {
		return ""
	}
	switch session.Ahead {
	case 0:
		return session.Branch + ", level with its upstream at session start"
	case 1:
		return session.Branch + ", 1 commit ahead at session start"
	default:
		return fmt.Sprintf("%s, %d commits ahead at session start", session.Branch, session.Ahead)
	}
}

func describeKey(id Identity) string {
	if id.Comment == "" {
		return id.Fingerprint()
	}
	return id.Fingerprint() + " (" + id.Comment + ")"
}

// withPromptMessage returns env carrying exactly one assignment of
// promptEnvVar.
//
// Every existing assignment is dropped rather than a new one being appended
// after them. Which of two assignments a program resolves to is not a thing to
// rely on, and here the answer decides what the operator reads before approving
// a signature: a variable of this name already exported in the operator's shell
// — left over from testing the dialog, say — must not be able to write the text
// of a security prompt.
func withPromptMessage(env []string, message string) []string {
	prefix := promptEnvVar + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, prefix+message)
}

// errDenied is the outcome for every answer that is not approval, including a
// closed dialog, an expired one and a platform with no way to ask. They are one
// outcome on purpose: the proxy acts on approval, and everything else is its
// absence.
var errDenied = errors.New("the operator did not approve the signature")
