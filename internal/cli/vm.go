package cli

import (
	"errors"
	"fmt"
	"os/user"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// defaultNewLima builds the production Lima adapter over a real execx runner.
// The runner redacts retained output and diagnostics with the default
// well-known secret shapes; later slices may pass a config-derived redactor.
func defaultNewLima() *lima.Adapter {
	return lima.New(&execx.ExecRunner{})
}

func defaultLookupOperatorUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current user for Lima login identity: %w", err)
	}
	name := strings.TrimSpace(u.Username)
	if name == "" {
		return "", fmt.Errorf("current user has empty username")
	}
	return name, nil
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

// newVMCmd builds the `torio vm` parent and its V1 subcommands. The parent takes
// no action itself: invoked with no (or an unknown) subcommand it returns a
// usage error, mirroring the root command's fail-closed dispatch.
func newVMCmd(a *app) *cobra.Command {
	vm := &cobra.Command{
		Use:   "vm",
		Short: "Control the Torio VM",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio vm --help'")
			}
			return usageError(fmt.Sprintf("unknown vm subcommand %q", args[0]))
		},
	}
	vm.AddCommand(newVMInitCmd(a))
	vm.AddCommand(newVMStatusCmd(a))
	vm.AddCommand(newVMStartCmd(a))
	vm.AddCommand(newVMStopCmd(a))
	vm.AddCommand(newVMBootstrapCmd(a))
	vm.AddCommand(newVMSSHCmd(a))
	return vm
}

func newVMInitCmd(a *app) *cobra.Command {
	var cpus int
	var memory string
	var disk string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create or verify the Torio VM from the trusted template",
		Long: "Create the Torio Lima VM from the embedded Gate-0 template, or succeed " +
			"idempotently when an existing instance already matches the trusted pins " +
			"(image digest, empty mounts, no persistent SSH agent forwarding). " +
			"Incompatible existing instances fail closed — there is no --force and Torio " +
			"never recreates or deletes them.\n\n" +
			"Defaults: 4 CPUs, 8GiB memory, 60GiB disk. Next step after success: torio vm start.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			opUser, err := a.lookupOperatorUser()
			if err != nil {
				return &CLIError{
					Exit:    ExitExternal,
					Code:    "OPERATOR_LOOKUP_FAILED",
					Command: "vm.init",
					Message: err.Error(),
				}
			}
			res, err := a.newLima().Init(ctx, lima.InitOptions{
				CPUs:         cpus,
				Memory:       memory,
				Disk:         disk,
				OperatorUser: opUser,
			})
			if err != nil {
				return mapLimaError("vm.init", err)
			}
			return a.emitVMInit(res)
		},
	}
	cmd.Flags().IntVar(&cpus, "cpus", 0, "vCPU count (default 4)")
	cmd.Flags().StringVar(&memory, "memory", "", "memory size, e.g. 8GiB (default 8GiB)")
	cmd.Flags().StringVar(&disk, "disk", "", "disk size, e.g. 60GiB (default 60GiB)")
	return cmd
}

// vmInitData is the `data` object for a successful `vm init`.
type vmInitData struct {
	Name          string `json:"name"`
	Created       bool   `json:"created"`
	Unchanged     bool   `json:"unchanged"`
	ImageLocation string `json:"image_location"`
	ImageDigest   string `json:"image_digest"`
	NextStep      string `json:"next_step"`
}

func (a *app) emitVMInit(res lima.InitResult) error {
	const next = "torio vm start"
	if a.jsonOut {
		data := vmInitData{
			Name:          lima.InstanceName,
			Created:       res.Created,
			Unchanged:     !res.Created,
			ImageLocation: res.ImageLocation,
			ImageDigest:   res.ImageDigest,
			NextStep:      next,
		}
		return writeJSON(a.stdout, successEnvelope("vm.init", data, nil))
	}
	if res.Created {
		_, err := fmt.Fprintf(a.stdout, "%s: created\nnext: %s\n", lima.InstanceName, next)
		return err
	}
	_, err := fmt.Fprintf(a.stdout, "%s: unchanged (compatible existing instance)\nnext: %s\n", lima.InstanceName, next)
	return err
}

func newVMStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the Torio VM state",
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
		Short: "Start the Torio VM",
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

func newVMStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the Torio VM",
		Long: "Gracefully stop the Torio VM. Idempotent: an already-stopped VM " +
			"succeeds without acting. Stop never removes the VM or its data, and never " +
			"trusts a clean exit — it re-queries and requires a Stopped post-state.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			// The adapter owns idempotency and the Stopped postcondition; a nil
			// error means the instance is confirmed stopped.
			if err := a.newLima().Stop(ctx); err != nil {
				return mapLimaError("vm.stop", err)
			}
			return a.emitVMState("vm.stop", lima.StateStopped)
		},
	}
}

func newVMBootstrapCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap",
		Short: "Reconcile and verify the existing Torio target for Remote Second Brain V1",
		Long: "Reconcile and verify the already-created Torio VM so an operator has a " +
			"usable Remote Second Brain V1 path: a stable non-interactive `hermes` command " +
			"and the V1 guest filesystem layout on native ext4.\n\n" +
			"It operates only on the existing target after a verified Running precondition, " +
			"through the typed Lima boundary. It is idempotent and narrow: when the pinned " +
			"Hermes Agent launcher is missing it installs the Gate-0 commit via the upstream " +
			"install script (verifiable postcondition: git HEAD pin + launcher path), may ensure " +
			"the `hermes` PATH shim, but never recreates or re-images the VM, installs a " +
			"model/provider, accepts secrets, or creates services. It verifies (not merely trusts) " +
			"the hermes user, torio-projects membership for hermes and the operator, absence of " +
			"docker-group membership for hermes, architecture, the hermes command, git, the " +
			"persistent profile/brain/workspace paths with correct ownership and modes on native " +
			"Linux, and the absence of a broad host mount — failing closed with remediation on " +
			"any drift.\n\n" +
			"Bootstrap issues several bounded guest probes; run it with an ample --timeout " +
			"(e.g. --timeout 15m — Hermes install can be slow).\n\n" +
			"After a successful run, reach the remote Hermes instance yourself (operator-controlled), " +
			"e.g.:  torio vm ssh -- sudo -u " + lima.HermesUser + " -- hermes --version",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			// V1 runs unpinned: the observed hermes version is reported so drift is
			// visible. A later slice can thread version-lock pins through
			// BootstrapOptions for enforcement.
			opUser, err := a.lookupOperatorUser()
			if err != nil {
				return &CLIError{
					Exit:    ExitExternal,
					Code:    "OPERATOR_LOOKUP_FAILED",
					Command: "vm.bootstrap",
					Message: err.Error(),
				}
			}
			rep, err := a.newLima().Bootstrap(ctx, lima.BootstrapOptions{OperatorUser: opUser})
			if err != nil {
				ce := mapLimaError("vm.bootstrap", err)
				// Surface the checks recorded up to the failure (already bounded and
				// redacted) so the operator sees exactly which postcondition failed.
				ce.Details = bootstrapReportDetails(rep)
				return ce
			}
			return a.emitVMBootstrap(rep)
		},
	}
}

func newVMSSHCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh -- COMMAND...",
		Short: "Run a command inside the Torio VM",
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

// vmBootstrapData is the `data` object for a successful `vm bootstrap`. It
// carries the proven checks plus the persistent Hermes locations the operator
// needs for the connection handoff — no secrets, no raw output.
type vmBootstrapData struct {
	Instance      string        `json:"instance"`
	Checks        []vmCheckData `json:"checks"`
	GuestUser     string        `json:"guest_user"`
	HermesHome    string        `json:"hermes_home"`
	ProfilePath   string        `json:"profile_path"`
	BrainPath     string        `json:"brain_path"`
	WorkspacePath string        `json:"workspace_path"`
}

// vmCheckData is one bootstrap check in the envelope. Detail is a short derived
// value (a parsed version, an fstype), never a raw output blob.
type vmCheckData struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func bootstrapData(rep lima.BootstrapReport) vmBootstrapData {
	checks := make([]vmCheckData, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		checks = append(checks, vmCheckData{Name: c.Name, OK: c.OK, Detail: c.Detail})
	}
	return vmBootstrapData{
		Instance:      rep.Instance,
		Checks:        checks,
		GuestUser:     lima.HermesUser,
		HermesHome:    lima.HermesHome,
		ProfilePath:   lima.HermesProfilePath,
		BrainPath:     lima.HermesBrainPath,
		WorkspacePath: lima.HermesWorkspacePath,
	}
}

// bootstrapReportDetails renders the checks recorded before a failure as error
// details, so a failing bootstrap still tells the operator which postcondition
// was not met. Values pass through the final redactor in fail().
func bootstrapReportDetails(rep lima.BootstrapReport) map[string]any {
	if len(rep.Checks) == 0 {
		return nil
	}
	checks := make([]map[string]any, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		checks = append(checks, map[string]any{"name": c.Name, "ok": c.OK, "detail": c.Detail})
	}
	return map[string]any{"instance": rep.Instance, "checks": checks}
}

// emitVMBootstrap renders a successful bootstrap. JSON mode emits exactly one
// success envelope; human mode prints one line per proven check plus the
// operator connection handoff (the persistent profile/brain locations and the
// stable command path). The post-bootstrap action to reach Hermes stays operator-controlled.
func (a *app) emitVMBootstrap(rep lima.BootstrapReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("vm.bootstrap", bootstrapData(rep), nil))
	}
	for _, c := range rep.Checks {
		mark := "ok"
		if !c.OK {
			mark = "FAIL"
		}
		if _, err := fmt.Fprintf(a.stdout, "[%s] %s: %s\n", mark, c.Name, c.Detail); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(a.stdout,
		"\nRemote Second Brain V1 path ready on %s.\n"+
			"Persistent Hermes home:    %s\n"+
			"Persistent profile:        %s\n"+
			"Persistent Second Brain:   %s\n"+
			"Persistent workspace:      %s\n"+
			"Reach Hermes (operator-controlled): torio vm ssh -- sudo -u %s -- hermes --version\n",
		rep.Instance, lima.HermesHome, lima.HermesProfilePath, lima.HermesBrainPath, lima.HermesWorkspacePath, lima.HermesUser)
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
// limactl itself exiting non-zero (lima.KindCommandFailed): torio treats any
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
	case lima.KindNotFound, lima.KindNotRunning, lima.KindAmbiguousState, lima.KindPostconditionFailed:
		return &CLIError{Exit: ExitPrecondition, Code: code, Command: command, Message: lerr.Error()}
	case lima.KindVerificationFailed, lima.KindIncompatible:
		return &CLIError{Exit: ExitVerification, Code: code, Command: command, Message: lerr.Error()}
	case lima.KindBinaryUnavailable, lima.KindCommandFailed, lima.KindMalformedOutput,
		lima.KindVersionMismatch, lima.KindTimeout, lima.KindCancelled:
		return &CLIError{Exit: ExitExternal, Code: code, Command: command, Message: lerr.Error()}
	default:
		e := internalError(lerr.Error())
		e.Command = command
		return e
	}
}
