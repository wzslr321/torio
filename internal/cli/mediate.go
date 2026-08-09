package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/sshagent"
)

// agentAuditFileName is the host-side decision log for mediated sessions. It
// sits beside the config document, in the same trusted directory, because it is
// operator state with the same custody: mode-private, owned by the EUID that
// wrote it.
const agentAuditFileName = "agent-audit.jsonl"

// mediation is a live agent proxy and the socket a session reaches it on. It
// exists for exactly as long as the session does.
type mediation struct {
	socket *sshagent.Socket
	audit  *os.File
	cancel context.CancelFunc
	done   chan struct{}
}

// SocketPath is the path the session's SSH_AUTH_SOCK is pointed at. It is empty
// for an unmediated session, which is how the caller tells the two apart.
func (m *mediation) SocketPath() string {
	if m == nil {
		return ""
	}
	return m.socket.Path
}

// stop ends the capability and waits for the proxy to finish.
//
// It runs on every exit path from a session, including the ones the operator
// caused with Ctrl-C: a socket that outlived the shell it was opened for would
// be exactly the durable capability ADR-0003 exists to prevent.
func (m *mediation) stop() {
	if m == nil {
		return
	}
	m.cancel()
	<-m.done
	_ = m.socket.Close()
	_ = m.audit.Close()
}

// startMediation puts Torio's agent in front of the operator's, when the config
// document pins a key.
//
// With no pin it returns nil and the session forwards the operator's agent whole,
// exactly as every session did before this existed. That is not a fallback to a
// weaker mode by accident: a document with no `operator_key` was written by an
// operator who has not chosen a key, and choosing one for them is choosing which
// key a guest may use (ADR-0015).
func (a *app) startMediation(command string, sessionCtx sshagent.SessionContext) (*mediation, error) {
	pin := a.runtime.File.OperatorKey
	if pin == "" {
		return nil, nil
	}
	fail := func(err error) error {
		return &CLIError{Exit: ExitPrecondition, Code: "PRECONDITION_FAILED", Command: command, Message: err.Error()}
	}

	dial, err := sshagent.UpstreamFromEnv()
	if err != nil {
		return nil, fail(err)
	}
	pinned, err := sshagent.PinIdentity(dial, pin)
	if err != nil {
		return nil, fail(err)
	}

	audit, err := openAuditFile(filepath.Join(a.runtime.Paths.ConfigDir, agentAuditFileName))
	if err != nil {
		// Fail closed before the session, not during it. A mediated session
		// whose decisions cannot be recorded would refuse every signature at
		// the dialog instead, which is the same outcome reached later and worse
		// explained.
		return nil, fail(err)
	}

	socket, err := sshagent.Listen(os.TempDir())
	if err != nil {
		_ = audit.Close()
		return nil, fail(err)
	}

	proxy := &sshagent.Proxy{
		Upstream: dial,
		Pinned:   pinned,
		Confirm:  sshagent.DialogConfirmer{},
		Audit:    sshagent.NewJSONRecorder(audit),
		Session:  sessionCtx,
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := &mediation{socket: socket, audit: audit, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(m.done)
		_ = proxy.Serve(ctx, socket.Listener)
	}()
	return m, nil
}

// openAuditFile opens the decision log for append.
//
// O_NOFOLLOW because the path sits in a directory the operator can write: a
// symlink placed there must not silently redirect the record of what a guest was
// allowed to sign. The mode is asserted after opening the descriptor, not the
// path, so nothing can be substituted between the check and the writes.
func openAuditFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the agent decision log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat the agent decision log: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("the agent decision log is not a regular file")
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("the agent decision log is mode %04o; it must not be readable by anyone else", perm)
	}
	return file, nil
}

// mediatedContext is what the dialog says about the session asking to sign. It
// is built from the preflight, which measured it once, before the session opened
// — never from anything the session reports about itself later.
func mediatedContext(p projects.Project, review projects.ReviewContext) sshagent.SessionContext {
	return sshagent.SessionContext{
		ProjectID: p.ID,
		Remote:    p.Remote,
		Branch:    review.Branch,
		Ahead:     review.Ahead,
	}
}
