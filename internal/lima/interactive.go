package lima

import (
	"crypto/rand"
	"encoding/hex"
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

// validateProjectPath accepts exactly one project directory directly under the
// backend's workspace root. The path is the only caller-shaped value in the
// argv and it is handed to the guest helper, so anything ambiguous — a
// traversal, a nested path, a leading dash, whitespace, shell syntax — fails
// closed here rather than being escaped later.
//
// The root is passed in rather than fixed: it belongs to the backend the
// instance runs, and a validator that knew only one backend's root would either
// reject every path on the other or, worse, be widened until it accepted both.
func validateProjectPath(workspaceRoot, projectPath string) error {
	if workspaceRoot == "" {
		return fmt.Errorf("no workspace root; the backend declares no workspace")
	}
	prefix := workspaceRoot + "/"
	if !strings.HasPrefix(projectPath, prefix) {
		return fmt.Errorf("project path %q is not a project under %s", projectPath, workspaceRoot)
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
func OperatorShellSpec(workspaceRoot, projectPath string) (execx.InteractiveCommand, error) {
	if err := validateProjectPath(workspaceRoot, projectPath); err != nil {
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

// MediatedShellSpec is OperatorShellSpec with Torio's own agent in front of the
// operator's.
//
// The argv is deliberately identical, down to the flags and their order. `-A`
// forwards whatever SSH_AUTH_SOCK names in this process, so pointing that one
// variable at the proxy socket changes what crosses into the guest without
// changing how it crosses: same auth-agent channel, same root-owned helper,
// same guest-side SSH_AUTH_SOCK written by sshd. The guest cannot tell the
// difference, which is the point — nothing on that side had to be trusted to
// make this narrower.
//
// This is the one session spec with a non-nil Env. OperatorShellSpec leaves it
// nil so the session inherits the operator's SSH_AUTH_SOCK, TERM and locale
// untouched; here exactly one of those is replaced and the rest are passed
// through, because a session that composed a fresh environment would lose the
// operator's terminal along with their keyring.
func MediatedShellSpec(workspaceRoot, projectPath, agentSocket string) (execx.InteractiveCommand, error) {
	if err := validateProjectPath(workspaceRoot, projectPath); err != nil {
		return execx.InteractiveCommand{}, &Error{Op: operatorShellOp, Kind: KindVerificationFailed, Err: err}
	}
	if !filepath.IsAbs(agentSocket) {
		return execx.InteractiveCommand{}, &Error{
			Op:   operatorShellOp,
			Kind: KindVerificationFailed,
			Err:  errors.New("the mediated agent socket must be an absolute path"),
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: operatorShellOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   operatorShellOp,
			Kind: KindNotFound,
			Err:  fmt.Errorf("no lima ssh config at %s; run `torio vm start` first", sshConfig),
		}
	}
	// Fail closed on the socket for the same reason OperatorShellSpec fails
	// closed on the agent: -A pointed at nothing opens a session that looks
	// working and refuses at the push.
	if _, statErr := os.Stat(agentSocket); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   operatorShellOp,
			Kind: KindNotFound,
			Err:  errors.New("the mediated agent socket is not there to forward"),
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
		Env: withAgentSocket(os.Environ(), agentSocket),
	}, nil
}

// withAgentSocket replaces every SSH_AUTH_SOCK in env with one naming socket.
//
// Every existing assignment is dropped rather than the last one being appended
// after them: which of two assignments an exec resolves to is not a thing to
// rely on when the answer decides whether a guest reaches one key or all of
// them.
func withAgentSocket(env []string, socket string) []string {
	const prefix = "SSH_AUTH_SOCK="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, prefix+socket)
}

// ProjectEnterSpec builds an interactive SSH command for ordinary project work
// without forwarding the operator's SSH agent. Multiplexing is disabled so the
// session cannot reuse a push-capable operator-shell connection.
func ProjectEnterSpec(workspaceRoot, projectPath string) (execx.InteractiveCommand, error) {
	if err := validateProjectPath(workspaceRoot, projectPath); err != nil {
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

// vmShellOp names the box-shell operation in errors.
const vmShellOp = "vm_shell"

// VMShellSpec builds an interactive SSH command that opens the Lima login
// identity's own shell inside the box.
//
// There is no remote command: sshd runs the login shell, which is what `vm
// ssh` cannot give (its argv is a command to run, typed at a shell the
// operator already has). No caller-shaped value appears anywhere in the argv,
// so no root-owned guest helper is needed — a helper exists to stop a
// host-composed value from becoming a remote command, and here there is none.
//
// The transport is the no-agent shape: no forwarding, no multiplexing, so a
// box shell cannot ride or become a push-capable connection. The login
// identity's own sudo is untouched; this opens nothing the operator's
// `limactl shell` does not already open.
func VMShellSpec() (execx.InteractiveCommand, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: vmShellOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   vmShellOp,
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
		},
	}, nil
}

// backendLoginOp names the login-session operation in errors.
const backendLoginOp = "backend_login"

const mcpLoginOp = "mcp_login"

// MCPLoginSpec opens the one callback tunnel the broker's OAuth client needs
// and runs its fixed login command as the credential-owning guest identity.
// The operator's SSH agent is explicitly disabled and multiplexing is disabled,
// so this session cannot inherit Git write capability from an operator shell.
func MCPLoginSpec(service string) (execx.InteractiveCommand, error) {
	if err := ValidateServiceName(service); err != nil {
		return execx.InteractiveCommand{}, &Error{Op: mcpLoginOp, Kind: KindVerificationFailed, Err: err}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: mcpLoginOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op: mcpLoginOp, Kind: KindNotFound,
			Err: fmt.Errorf("no lima ssh config at %s; run `torio vm start` first", sshConfig),
		}
	}
	return execx.InteractiveCommand{Name: "ssh", Args: []string{
		"-F", sshConfig,
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ForwardAgent=no",
		"-o", "ExitOnForwardFailure=yes",
		"-a",
		"-L", "127.0.0.1:43119:127.0.0.1:43119",
		sshHostAlias(),
		"sudo", "-n", "-u", TorioMCPUser, "--",
		TorioMCPBrokerPath, "login", service,
	}}, nil
}

// BackendLoginSpec builds an interactive SSH command that runs a backend's own
// fixed login argv on the guest.
//
// It carries no operator input at all: the argv is a constant the backend
// declares, assembled from its identity and its pinned install path. That is
// why this one needs no root-owned guest helper — a helper exists to stop a
// host-composed value from becoming a remote command, and here there is no such
// value to stop.
//
// The transport is the no-agent shape: no forwarding, no multiplexing, so a
// login session cannot ride a push-capable connection and cannot itself reach a
// Git remote.
func BackendLoginSpec(argv []string) (execx.InteractiveCommand, error) {
	if len(argv) == 0 {
		return execx.InteractiveCommand{}, &Error{
			Op:   backendLoginOp,
			Kind: KindVerificationFailed,
			Err:  fmt.Errorf("the backend declares no login command"),
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: backendLoginOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   backendLoginOp,
			Kind: KindNotFound,
			Err:  fmt.Errorf("no lima ssh config at %s; run `torio vm start` first", sshConfig),
		}
	}
	args := []string{
		"-F", sshConfig,
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ForwardAgent=no",
		"-a",
		"-t",
		sshHostAlias(),
	}
	return execx.InteractiveCommand{Name: "ssh", Args: append(args, argv...)}, nil
}

// projectAgentOp names the agent-session operation in errors.
const projectAgentOp = "project_agent"

// ProjectAgentSpec builds an interactive SSH command that opens the backend's
// own agent session inside a checkout.
//
// The transport is the enter shape, not the shell shape: no agent forwarding,
// no multiplexing. That is the decision, not an omission. The agent works in a
// tree it owns and commits there; pushing stays a human act from `project
// shell`, so a session that runs an agent must not be able to reach a remote —
// and must not be able to inherit a connection that can.
//
// helper is the backend's declared guest entry point. Like the other session
// helpers it is root-owned and receives exactly one validated path; the command
// it runs is a constant inside it.
func ProjectAgentSpec(helper, workspaceRoot, projectPath string) (execx.InteractiveCommand, error) {
	if helper == "" {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectAgentOp,
			Kind: KindVerificationFailed,
			Err:  fmt.Errorf("the backend declares no agent session helper"),
		}
	}
	if err := validateProjectPath(workspaceRoot, projectPath); err != nil {
		return execx.InteractiveCommand{}, &Error{Op: projectAgentOp, Kind: KindVerificationFailed, Err: err}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: projectAgentOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectAgentOp,
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
			helper,
			projectPath,
		},
	}, nil
}

// guestPushSocketPattern is the shape of the forwarded socket path on the guest,
// and the guest helper enforces the identical pattern. The random component is
// what makes the path unguessable, so nothing can be sitting at it: sshd refuses
// to bind over an existing file and ExitOnForwardFailure turns that refusal into
// a session that does not open.
var guestPushSocketPattern = regexp.MustCompile(`^/tmp/torio-push-[0-9a-f]{32}\.sock$`)

// NewGuestPushSocketPath mints the guest-side path for one granted session.
//
// /tmp rather than a provisioned directory: it exists on every guest, both
// identities can traverse it, and its sticky bit means only the owner can remove
// what they put there. The socket lands owned by the operator and unreadable by
// anyone else; the helper hands it to the shared group and no wider.
func NewGuestPushSocketPath() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate the guest agent socket path: %w", err)
	}
	return "/tmp/torio-push-" + hex.EncodeToString(raw[:]) + ".sock", nil
}

// ProjectAgentPushSpec opens an agent session that may ask to push.
//
// The transport still forwards no SSH agent: ForwardAgent stays off and -a stays
// on, so nothing about the operator's own keyring reaches this session, and it
// still cannot inherit a multiplexed connection that could. What it gains is one
// remote-forwarded Unix socket whose far end is Torio's own agent — a single
// pinned key that answers nothing without a dialog on the operator's Mac
// (ADR-0015). The agent gets the ability to ask, never the ability to answer.
//
// ExitOnForwardFailure is what makes that honest: a session whose forward failed
// would otherwise open, run the agent, and fail at the push with no explanation.
func ProjectAgentPushSpec(helper, workspaceRoot, projectPath, hostSocket, guestSocket string) (execx.InteractiveCommand, error) {
	if helper == "" {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectAgentOp,
			Kind: KindVerificationFailed,
			Err:  fmt.Errorf("the backend declares no push-capable agent session helper"),
		}
	}
	if err := validateProjectPath(workspaceRoot, projectPath); err != nil {
		return execx.InteractiveCommand{}, &Error{Op: projectAgentOp, Kind: KindVerificationFailed, Err: err}
	}
	if !guestPushSocketPattern.MatchString(guestSocket) {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectAgentOp,
			Kind: KindVerificationFailed,
			Err:  errors.New("the guest agent socket path is not the shape the guest helper accepts"),
		}
	}
	if !filepath.IsAbs(hostSocket) {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectAgentOp,
			Kind: KindVerificationFailed,
			Err:  errors.New("the host agent socket must be an absolute path"),
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return execx.InteractiveCommand{}, &Error{Op: projectAgentOp, Kind: KindNotFound, Err: err}
	}
	sshConfig := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return execx.InteractiveCommand{}, &Error{
			Op:   projectAgentOp,
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
			"-o", "ExitOnForwardFailure=yes",
			"-a",
			"-R", guestSocket + ":" + hostSocket,
			"-t",
			sshHostAlias(),
			helper,
			projectPath,
			guestSocket,
		},
	}, nil
}
