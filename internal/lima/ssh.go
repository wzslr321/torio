package lima

import (
	"context"

	"hermes-box.local/hb/internal/execx"
)

// SSH runs command inside InstanceName via `limactl shell`. Each element of
// command is passed as a separate argv entry — never joined into a shell
// string — and a literal "--" always precedes it so a command token that
// looks like a flag (e.g. "--looks-like-a-flag") can never be reinterpreted
// by limactl's own flag parser. A clean non-zero exit from the remote
// command is not an SSH/adapter failure: the caller reads Result.ExitCode,
// the same contract as execx itself.
func (a *Adapter) SSH(ctx context.Context, command []string) (execx.Result, error) {
	const op = "ssh"

	args := make([]string, 0, len(command)+4)
	args = append(args, "shell", "--tty=false", InstanceName, "--")
	args = append(args, command...)

	res, err := a.runRaw(ctx, args...)
	if err != nil {
		return execx.Result{}, classifyRunErr(op, err)
	}
	return res, nil
}

// SSHInput is SSH with a fed standard input: stdin is delivered verbatim to the
// remote command (then closed). It is the no-shell primitive for writing a
// generated file onto the guest via a filter like `tee FILE` — the payload
// travels as stdin bytes, never as an argv element or a shell heredoc. The argv
// contract is identical to SSH (each token a separate element after a literal
// "--"), and a clean non-zero remote exit is the caller's to interpret.
func (a *Adapter) SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error) {
	const op = "ssh"

	args := make([]string, 0, len(command)+4)
	args = append(args, "shell", "--tty=false", InstanceName, "--")
	args = append(args, command...)

	res, err := a.Runner.Run(ctx, execx.Command{Name: a.bin(), Args: args, Stdin: stdin})
	if err != nil {
		return execx.Result{}, classifyRunErr(op, err)
	}
	return res, nil
}
