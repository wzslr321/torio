package lima

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
)

const (
	// operatorShellOp names the push-capable operator session in errors.
	operatorShellOp = "project_shell"
	// projectEnterOp names the ordinary workspace session in errors.
	projectEnterOp = "project_enter"
)

// OperatorShellHelper is the fixed guest entry point of an operator session.
// It is the guest-side counterpart of
// `sg torio-projects -c 'cd <project> …'`: it takes exactly one argument, the
// project path, and drops the operator into that checkout under the shared
// project group. Keeping it a constant is what makes the remote side a fixed
// argv instead of a caller-supplied command string.
const OperatorShellHelper = "/usr/local/bin/torio-project-shell"

// ProjectEnterHelper is the fixed guest entry point of an ordinary workspace
// session. Its SSH transport never forwards the operator's agent.
const ProjectEnterHelper = "/usr/local/bin/torio-project-enter"

// sshHostAlias is the host entry Lima writes into the instance ssh config. It
// follows the selected instance because Lima derives the alias from the
// instance name (ADR-0001); a fixed alias would point an operator shell at the
// wrong VM.
func sshHostAlias() string { return "lima-" + InstanceName }

// projectIDPattern is the strict allowlist for the single path segment below
// the guest workspace. It admits ordinary repository names and nothing that
// could be read as a flag, a path segment, or shell syntax on the guest.
var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateProjectPath accepts exactly one project directory directly under
// HermesWorkspacePath. The path is the only caller-shaped value in the argv and
// it is handed to the guest helper, so anything ambiguous — a traversal, a
// nested path, a leading dash, whitespace, shell syntax — fails closed here
// rather than being escaped later.
func validateProjectPath(projectPath string) error {
	prefix := HermesWorkspacePath + "/"
	if !strings.HasPrefix(projectPath, prefix) {
		return fmt.Errorf("project path %q is not a project under %s", projectPath, HermesWorkspacePath)
	}
	id := strings.TrimPrefix(projectPath, prefix)
	if !projectIDPattern.MatchString(id) {
		return fmt.Errorf("project id %q is not allowed", id)
	}
	return nil
}

// OperatorShellSpec builds the exact, evidence-pinned argv for an ephemeral
// operator shell into projectPath.
//
// The flags are the shape proven against Lima 2.2.0 and OpenSSH 10.2p1:
//
//	ssh -F ~/.lima/<instance>/ssh.config \
//	  -o ControlMaster=no -o ControlPath=none -o ForwardAgent=yes -A \
//	  lima-<instance> …
//
// The -o overrides MUST follow -F: Lima's own ssh.config sets
// ControlMaster/ControlPersist, so an override placed ahead of -F is the one
// that loses, and the session then rides a multiplexed connection whose master
// was opened without agent forwarding. In this order it was measured with a
// stale mux socket already open and the forwarded agent still reached the
// guest. -t forces a TTY because a remote command is present, and -n is
// deliberately absent: it is required only to background a session, and here it
// would redirect the operator's stdin from /dev/null.
//
// TestOperatorShellSpecBuildsThePromotedArgv pins this argv element by element.
//
// The remote side is the fixed guest helper plus the validated project path.
// There is no caller-supplied remote command string, and no environment is
// attached: a nil Env means the session inherits the operator's SSH_AUTH_SOCK,
// TERM and locale instead of Torio composing a new one.
func OperatorShellSpec(projectPath string) (execx.InteractiveCommand, error) {
	if err := validateProjectPath(projectPath); err != nil {
		return execx.InteractiveCommand{}, &Error{Op: operatorShellOp, Kind: KindVerificationFailed, Err: err}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: operatorShellOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")

	// Fail closed on the host preconditions rather than opening a session that
	// forwards nothing: -A without an agent is a shell with no write
	// capability, and the operator would only discover it when the push fails.
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   operatorShellOp,
			Kind: KindNotFound,
			Err:  fmt.Errorf("no lima ssh config at %s; run `torio vm start` first", sshConfig),
		}
	}
	if strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")) == "" {
		return execx.InteractiveCommand{}, &Error{
			Op:   operatorShellOp,
			Kind: KindNotFound,
			Err:  errors.New("no SSH agent to forward: SSH_AUTH_SOCK is not set; start ssh-agent and `ssh-add` the key that can push"),
		}
	}

	return execx.InteractiveCommand{
		Name: "ssh",
		Args: []string{
			"-F", sshConfig,
			"-o", "ControlMaster=no",
			"-o", "ControlPath=none",
			"-o", "ForwardAgent=yes",
			"-A",
			"-t",
			sshHostAlias(),
			OperatorShellHelper,
			projectPath,
		},
	}, nil
}

// ProjectEnterSpec builds an interactive SSH command for ordinary project work
// without forwarding the operator's SSH agent. Multiplexing is disabled so the
// session cannot reuse a push-capable operator-shell connection.
func ProjectEnterSpec(projectPath string) (execx.InteractiveCommand, error) {
	if err := validateProjectPath(projectPath); err != nil {
		return execx.InteractiveCommand{}, &Error{Op: projectEnterOp, Kind: KindVerificationFailed, Err: err}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: projectEnterOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectEnterOp,
			Kind: KindNotFound,
			Err:  fmt.Errorf("no lima ssh config at %s; run `torio vm start` first", sshConfig),
		}
	}

	return execx.InteractiveCommand{
		Name: "ssh",
		Args: []string{
			"-F", sshConfig,
			"-o", "ControlMaster=no",
			"-o", "ControlPath=none",
			"-o", "ForwardAgent=no",
			"-a",
			"-t",
			sshHostAlias(),
			ProjectEnterHelper,
			projectPath,
		},
	}, nil
}
