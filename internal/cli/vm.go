package cli

import (
	"errors"
	"fmt"
	"os/user"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/config"
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
			"An instance runs one agent backend, chosen by the global --backend flag " +
			"and recorded in this instance's config. That flag also selects which box " +
			"this is, so `vm init --backend NAME` builds the box for that agent rather " +
			"than converting the one you have: a second backend is a second VM, never a " +
			"second agent inside one, because two identities sharing a workspace would " +
			"contend over the same checkouts and make every custody statement " +
			"ambiguous.\n\n" +
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
			// The backend is settled and recorded before the VM exists. It is a
			// creation-time fact about the instance: the guest is provisioned
			// for one identity, so a box that came up for one backend cannot be
			// re-declared as another by editing a document afterwards.
			if err := a.declareBackend(a.backendName); err != nil {
				return err
			}
			res, err := a.newLima().Init(ctx, lima.InitOptions{
				CPUs:         cpus,
				Memory:       memory,
				Disk:         disk,
				OperatorUser: opUser,
				Backend:      a.backend,
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

// declareBackend settles which backend this instance runs and records it before
// anything creates a VM for it.
//
// A rerun with no flag keeps what the instance already declares, so init stays
// idempotent. A flag naming a different backend than the instance already
// declares is a usage error: switching the agent a provisioned guest runs is
// not an edit, it is a new instance, and silently accepting the flag would
// leave a guest built for one identity being driven as another.
//
// The pre-run rejects that pair before any command runs, so nothing should
// reach the branch below. It stays because this is the function that writes the
// declaration, and a write path may not depend on a check made somewhere else
// to know what it is allowed to persist.
func (a *app) declareBackend(name string) error {
	if name == "" {
		return nil
	}
	if _, err := backend.Lookup(name); err != nil {
		return usageError(err.Error())
	}
	current := a.runtime.File.Backend
	if current == "" && name == backend.DefaultName {
		// Nothing to record: an absent declaration already means the default.
		return nil
	}
	if current != "" && current != name {
		return &CLIError{
			Exit:    ExitUsage,
			Code:    "BACKEND_MISMATCH",
			Command: "vm.init",
			Message: fmt.Sprintf("instance %q already declares backend %q; --backend %q would re-point a provisioned guest. Each backend gets its own instance, so `torio vm init --backend %s` builds one rather than converting this one.",
				lima.InstanceName, current, name, name),
		}
	}
	if current == name {
		return nil
	}
	file := a.runtime.File
	file.SchemaVersion = config.ConfigSchemaVersion
	file.Backend = name
	if err := config.WriteFile(a.runtime.Paths.ConfigFile, file); err != nil {
		return &CLIError{
			Exit:    ExitUsage,
			Code:    "BACKEND_DECLARATION_FAILED",
			Command: "vm.init",
			Message: err.Error(),
		}
	}
	a.runtime.File = file
	b, err := backend.Lookup(name)
	if err != nil {
		return usageError(err.Error())
	}
	a.backend = b
	return nil
}

// vmInitData is the `data` object for a successful `vm init`.
type vmInitData struct {
	Name          string `json:"name"`
	Backend       string `json:"backend"`
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
			Backend:       a.backend.Identity().Name,
			Created:       res.Created,
			Unchanged:     !res.Created,
			ImageLocation: res.ImageLocation,
			ImageDigest:   res.ImageDigest,
			NextStep:      next,
		}
		return writeJSON(a.stdout, successEnvelope("vm.init", data))
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
		Short: "Reconcile and verify the existing Torio target for Hermes",
		Long: "Reconcile and verify the already-created Torio VM so an operator has a " +
			"usable Hermes path: a stable non-interactive `hermes` command " +
			"and the guest filesystem layout on native ext4.\n\n" +
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
			"Bootstrap issues several bounded guest probes and can install Hermes from source, " +
			"so give it the largest timeout the policy allows: --timeout 10m.\n\n" +
			"After a successful run, reach the remote Hermes instance yourself (operator-controlled), " +
			"e.g.:  torio vm ssh -- sudo -u " + lima.HermesUser + " -- hermes --version",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			// V1 runs unpinned: the observed hermes version is reported so drift is
			// visible. Enforcing a pin would need a new pin source and a new ADR;
			// the D2 version-lock manifest was never wired and is gone (ADR-0005).
			opUser, err := a.lookupOperatorUser()
			if err != nil {
				return &CLIError{
					Exit:    ExitExternal,
					Code:    "OPERATOR_LOOKUP_FAILED",
					Command: "vm.bootstrap",
					Message: err.Error(),
				}
			}
			rep, err := a.newLima().Bootstrap(ctx, lima.BootstrapOptions{OperatorUser: opUser, Backend: a.backend})
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
		return writeJSON(a.stdout, successEnvelope(command, data))
	}
	_, err := fmt.Fprintf(a.stdout, "%s: %s\n", lima.InstanceName, state)
	return err
}

// vmBootstrapData is the `data` object for a successful `vm bootstrap`. It
// carries the proven checks plus the guest locations the operator needs for the
// connection handoff — no secrets, no raw output.
//
// `hermes_home` is a legacy alias for `home`, emitted on every backend so no
// existing reader breaks. New readers should use `home`, which is the identity's
// home whichever backend owns it.
type vmBootstrapData struct {
	Instance      string      `json:"instance"`
	Backend       string      `json:"backend"`
	Checks        []checkData `json:"checks"`
	GuestUser     string      `json:"guest_user"`
	Home          string      `json:"home"`
	HermesHome    string      `json:"hermes_home"`
	ProfilePath   string      `json:"profile_path"`
	BrainPath     string      `json:"brain_path"`
	WorkspacePath string      `json:"workspace_path"`
}

func bootstrapData(rep lima.BootstrapReport, id backend.Identity) vmBootstrapData {
	return vmBootstrapData{
		Instance:      rep.Instance,
		Backend:       id.Name,
		Checks:        checkPayload(rep.Checks),
		GuestUser:     id.GuestUser,
		Home:          id.Home,
		HermesHome:    id.Home,
		ProfilePath:   id.ProfilePath,
		BrainPath:     id.BrainPath,
		WorkspacePath: id.WorkspacePath,
	}
}

// bootstrapReportDetails renders the checks recorded before a failure as error
// details, so a failing bootstrap still tells the operator which postcondition
// was not met. Values pass through the final redactor in fail().
func bootstrapReportDetails(rep lima.BootstrapReport) map[string]any {
	if len(rep.Checks) == 0 {
		return nil
	}
	return map[string]any{"instance": rep.Instance, "checks": checkDetails(rep.Checks)}
}

// emitVMBootstrap renders a successful bootstrap. JSON mode emits exactly one
// success envelope; human mode prints one line per proven check plus the
// operator connection handoff (the persistent profile/brain locations and the
// stable command path). The post-bootstrap action to reach Hermes stays operator-controlled.
func (a *app) emitVMBootstrap(rep lima.BootstrapReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("vm.bootstrap", bootstrapData(rep, a.backend.Identity())))
	}
	if err := a.writeCheckLines(rep.Checks); err != nil {
		return err
	}
	id := a.backend.Identity()
	if _, err := fmt.Fprintf(a.stdout,
		"\nBackend %s ready on %s.\n"+
			"Guest identity:            %s\n"+
			"Persistent home:           %s\n"+
			"Persistent profile:        %s\n"+
			"Persistent Second Brain:   %s\n"+
			"Persistent workspace:      %s\n",
		id.Name, rep.Instance, id.GuestUser, id.Home, id.ProfilePath, id.BrainPath, id.WorkspacePath); err != nil {
		return err
	}
	return a.writeBootstrapNextStep(rep)
}

// writeBootstrapNextStep prints the one thing the operator should do next, which
// differs by what the backend declares and by what bootstrap just observed. A
// backend with a credential it has not been granted yet needs a login before
// anything else is worth trying.
func (a *app) writeBootstrapNextStep(rep lima.BootstrapReport) error {
	if credentialState(rep, a.backend.StatusChecks().Auth) == "absent" {
		_, err := fmt.Fprintf(a.stdout, "next: torio backend login\n")
		return err
	}
	if a.backend.Service() != nil {
		_, err := fmt.Fprintf(a.stdout, "next: torio serve install\n")
		return err
	}
	if a.backend.Session() != nil {
		_, err := fmt.Fprintf(a.stdout, "next: torio project add <id> <remote>, then torio project agent <id>\n")
		return err
	}
	return nil
}

// emitVMSSH renders an ssh Result. A non-zero remote exit is never reported as
// success: it is surfaced through the same external-command-failure class as
// limactl itself exiting non-zero (mapVMSSHResult).
func (a *app) emitVMSSH(res execx.Result) error {
	if a.jsonOut {
		if res.ExitCode != 0 {
			return sshCommandFailed(res)
		}
		return writeJSON(a.stdout, successEnvelope("vm.ssh", sshData(res)))
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
		lima.KindTimeout, lima.KindCancelled:
		return &CLIError{Exit: ExitExternal, Code: code, Command: command, Message: lerr.Error()}
	default:
		e := internalError(lerr.Error())
		e.Command = command
		return e
	}
}
