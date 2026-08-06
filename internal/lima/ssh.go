package lima

import (
	"context"

	"github.com/wzslr321/torio/internal/execx"
)

// guestShellArgs returns the fixed limactl prefix of every guest command.
//
// The working directory is pinned rather than inherited. `limactl shell` starts
// in the host's working directory when that path also exists on the guest, and
// otherwise falls back to the *Lima login user's* home — which no Torio guest
// command runs as. The hermes identity cannot enter the operator's home, so a
// command that merely remembers where it started fails there: GNU find restores
// its initial directory before exiting and reports
//
//	find: Failed to restore initial working directory: /home/<operator>: Permission denied
//
// with exit 1, after having produced correct output. `torio brain init` read
// that as a failed guest command and refused to scaffold the Brain on a machine
// where nothing was wrong. Every guest command addresses absolute paths, so the
// working directory carries no meaning here; "/" is the one directory every
// identity on the guest can enter.
//
// It is built at call time because cli.Run selects InstanceName during process
// startup. Capturing InstanceName in a package-level slice would permanently
// retain the default instance and route named-instance commands to the wrong VM.
func guestShellArgs() []string {
	return []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--"}
}

// SSH runs command inside InstanceName via `limactl shell`. Each element of
// command is passed as a separate argv entry — never joined into a shell
// string — and a literal "--" always precedes it so a command token that
// looks like a flag (e.g. "--looks-like-a-flag") can never be reinterpreted
// by limactl's own flag parser. A clean non-zero exit from the remote
// command is not an SSH/adapter failure: the caller reads Result.ExitCode,
// the same contract as execx itself.
func (a *Adapter) SSH(ctx context.Context, command []string) (execx.Result, error) {
	return a.SSHInput(ctx, nil, command)
}

// SSHInput is SSH with a fed standard input: stdin is delivered verbatim to the
// remote command (then closed). It is the no-shell primitive for writing a
// generated file onto the guest via a filter like `tee FILE` — the payload
// travels as stdin bytes, never as an argv element or a shell heredoc. The argv
// contract is identical to SSH (each token a separate element after a literal
// "--"), and a clean non-zero remote exit is the caller's to interpret.
func (a *Adapter) SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error) {
	const op = "ssh"

	prefix := guestShellArgs()
	args := make([]string, 0, len(command)+len(prefix)+1)
	args = append(args, prefix...)
	args = append(args, command...)

	res, err := a.Runner.Run(ctx, execx.Command{Name: bin, Args: args, Stdin: stdin})
	if err != nil {
		return execx.Result{}, classifyRunErr(op, err)
	}
	return res, nil
}
