package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hermes-box.local/hb/internal/execx"
	"hermes-box.local/hb/internal/lima"
)

// defaultNewLima builds the production Lima adapter over a real execx runner.
// The runner redacts retained output and diagnostics with the default
// well-known secret shapes; later slices may pass a config-derived redactor.
func defaultNewLima() *lima.Adapter {
	return lima.New(&execx.ExecRunner{})
}

// vmStateData is the minimal `data` object shared by `vm status` and `vm start`.
type vmStateData struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// vmSSHData is the `data` object for a successful `vm ssh`. Every value comes
// verbatim from the redacted, bounded execx.Result — no new logging or output
// channel is introduced here.
type vmSSHData struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
}

// newVMCmd builds the `hb vm` parent and its V1 subcommands. The parent takes
// no action itself: invoked with no (or an unknown) subcommand it returns a
// usage error, mirroring the root command's fail-closed dispatch.
func newVMCmd(a *app) *cobra.Command {
	vm := &cobra.Command{
		Use:   "vm",
		Short: "Control the Hermes Box VM",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'hb vm --help'")
			}
			return usageError(fmt.Sprintf("unknown vm subcommand %q", args[0]))
		},
	}
	vm.AddCommand(newVMStatusCmd(a))
	vm.AddCommand(newVMStartCmd(a))
	vm.AddCommand(newVMSSHCmd(a))
	return vm
}

func newVMStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the Hermes Box VM state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			st, err := a.newLima().Status(ctx)
			if err != nil {
				return mapLimaError("vm.status", err)
			}
			return a.emitVMState("vm.status", st.State)
		},
	}
}

func newVMStartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the Hermes Box VM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			// The adapter owns idempotency and the Running postcondition; a nil
			// error means the instance is confirmed running.
			if err := a.newLima().Start(ctx); err != nil {
				return mapLimaError("vm.start", err)
			}
			return a.emitVMState("vm.start", lima.StateRunning)
		},
	}
}

func newVMSSHCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- COMMAND...",
		Short: "Run a command inside the Hermes Box VM",
		// At least one token is required: V1 does not open an interactive
		// shell. Missing tokens is a Cobra arg error → usage (exit 2).
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			// args are the exact command tokens, passed through as a separate
			// argv per element; the adapter never joins them into a shell string.
			res, err := a.newLima().SSH(ctx, args)
			if err != nil {
				return mapLimaError("vm.ssh", err)
			}
			return a.emitVMSSH(res)
		},
	}
}

// emitVMState writes the minimal VM-state result for status/start. In JSON mode
// it emits exactly one success envelope; otherwise a single "name: state" line.
func (a *app) emitVMState(command string, state lima.State) error {
	if a.jsonOut {
		data := vmStateData{Name: lima.InstanceName, State: string(state)}
		return writeJSON(a.stdout, successEnvelope(command, data, nil))
	}
	_, err := fmt.Fprintf(a.stdout, "%s: %s\n", lima.InstanceName, state)
	return err
}

// emitVMSSH renders an ssh Result. A non-zero remote exit is never reported as
// success: it is surfaced through the same external-command-failure class as
// limactl itself exiting non-zero (mapVMSSHResult).
func (a *app) emitVMSSH(res execx.Result) error {
	if a.jsonOut {
		if res.ExitCode != 0 {
			return sshCommandFailed(res)
		}
		return writeJSON(a.stdout, successEnvelope("vm.ssh", sshData(res), nil))
	}
	// Human mode: route the remote streams to our own streams verbatim, keeping
	// stdout free of anything but the remote command's stdout.
	if _, err := a.stdout.Write(res.Stdout); err != nil {
		return err
	}
	if _, err := a.stderr.Write(res.Stderr); err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return sshCommandFailed(res)
	}
	return nil
}

func sshData(res execx.Result) vmSSHData {
	return vmSSHData{
		ExitCode:        res.ExitCode,
		Stdout:          string(res.Stdout),
		Stderr:          string(res.Stderr),
		StdoutTruncated: res.StdoutTruncated,
		StderrTruncated: res.StderrTruncated,
	}
}

// sshCommandFailed classifies a non-zero remote exit. A remote command exiting
// non-zero is mapped to the same external-command-failure class (exit 8) as
// limactl itself exiting non-zero (lima.KindCommandFailed): hb treats any
// non-zero exit under the Lima adapter uniformly and never reports success. The
// exact remote exit code and (bounded, redacted) output stay in error.details.
func sshCommandFailed(res execx.Result) *CLIError {
	return &CLIError{
		Exit:    ExitExternal,
		Code:    "COMMAND_FAILED",
		Command: "vm.ssh",
		Message: fmt.Sprintf("remote command exited %d", res.ExitCode),
		Details: map[string]any{
			"exit_code":        res.ExitCode,
			"stdout":           string(res.Stdout),
			"stderr":           string(res.Stderr),
			"stdout_truncated": res.StdoutTruncated,
			"stderr_truncated": res.StderrTruncated,
		},
	}
}

// mapLimaError maps a *lima.Error onto the CLI exit-code contract via ErrorKind
// (never string matching). The Code carries the kind for machine callers; the
// exit code follows docs/contracts/cli.md.
func mapLimaError(command string, err error) *CLIError {
	var lerr *lima.Error
	if !errors.As(err, &lerr) {
		e := internalError(err.Error())
		e.Command = command
		return e
	}
	code := strings.ToUpper(string(lerr.Kind))
	switch lerr.Kind {
	case lima.KindNotFound, lima.KindAmbiguousState, lima.KindPostconditionFailed:
		return &CLIError{Exit: ExitPrecondition, Code: code, Command: command, Message: lerr.Error()}
	case lima.KindBinaryUnavailable, lima.KindCommandFailed, lima.KindMalformedOutput,
		lima.KindVersionMismatch, lima.KindTimeout, lima.KindCancelled:
		return &CLIError{Exit: ExitExternal, Code: code, Command: command, Message: lerr.Error()}
	default:
		e := internalError(lerr.Error())
		e.Command = command
		return e
	}
}
