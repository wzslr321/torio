// Package cli implements the `torio` command-line surface: command dispatch via
// Cobra, the stable JSON envelope, exit-code mapping, and strict separation of
// machine output (stdout) from diagnostics (stderr). Cobra owns the command
// tree, per-command flags, and help; this package owns the envelope/exit-code
// contract and redaction so those invariants hold regardless of framework.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/serve"
)

// app holds the per-invocation wiring shared across the command tree.
type app struct {
	stdout io.Writer
	stderr io.Writer
	build  BuildInfo

	// Populated from persistent flags / pre-run.
	jsonOut     bool
	verbose     bool
	timeout     time.Duration
	configPath  string
	backendName string
	logger      *slog.Logger

	// instance is the managed instance this invocation talks to, resolved once
	// in PersistentPreRunE. It is held here because every config path in the
	// invocation must be resolved against the same one — a registry that
	// re-derived it would be free to disagree with the command it serves.
	instance string

	// runtime is the resolved configuration (paths + config document). It is
	// populated by PersistentPreRunE and consumed by command execution.
	runtime config.Runtime

	// backend is the agent backend this instance runs. It is resolved once, in
	// PersistentPreRunE, before any command can touch the guest: which agent a
	// box runs decides what every later command is allowed to assume about it.
	backend backend.Backend

	// newLima builds the Lima adapter for a command run. It is the unexported
	// test seam: production defaults to a real execx-backed adapter, tests
	// inject one wired to a fake runner. It never touches a real VM in tests.
	newLima func() *lima.Adapter

	// newServe builds the serve-lifecycle adapter for a command run. Same test
	// seam pattern as newLima: production wires it over the real Lima adapter,
	// tests inject one backed by a fake guest.
	newServe func() *serve.Adapter

	// newBrain builds the private Brain manager. Tests inject a service fake;
	// production wires the manager over the same typed Lima guest boundary.
	newBrain func(*lima.Adapter, lima.BootstrapOptions) brainService

	// newProjects builds the project manager over the guest and the config
	// registry. Same test seam pattern as newBrain.
	newProjects func(*lima.Adapter, lima.BootstrapOptions) projectService

	// newEnterSpec and newShellSpec build the ordinary and push-capable SSH argv
	// for a project path; newInteractive executes either command. They are seams
	// because the production spec reads host state (the Lima ssh config, the
	// SSH agent) that a test must not depend on.
	newEnterSpec func(projectPath string) (execx.InteractiveCommand, error)
	newShellSpec func(projectPath string) (execx.InteractiveCommand, error)
	// newMediatedShellSpec is newShellSpec with Torio's own agent in front of
	// the operator's. It is a second seam rather than a parameter on the first
	// so the unmediated argv keeps its own pinned test: the two shapes differ
	// only in one environment variable, which is exactly the kind of difference
	// a shared constructor loses.
	newMediatedShellSpec func(projectPath, agentSocket string) (execx.InteractiveCommand, error)
	newAgentSpec         func(projectPath string) (execx.InteractiveCommand, error)
	// newAgentPushSpec is newAgentSpec plus the remote-forwarded socket of the
	// mediated agent. Separate for the same reason as newMediatedShellSpec: the
	// no-push argv keeps its own pinned test, and the test that forbids
	// ForwardAgent from the agent transport keeps covering the default.
	newAgentPushSpec func(projectPath, hostSocket, guestSocket string) (execx.InteractiveCommand, error)
	newInteractive   func() execx.InteractiveRunner
	newMCPLoginSpec  func(service string) (execx.InteractiveCommand, error)
	installMCP       func(context.Context, *lima.Adapter, backend.Identity) (lima.MCPBrokerInstallReport, error)
	verifyMCP        func(context.Context, *lima.Adapter, backend.Identity) (lima.MCPBrokerReport, error)
	activateMCP      func(context.Context, *lima.Adapter, backend.Identity) (lima.MCPBrokerActivationReport, error)

	// lookupOperatorUser resolves the Lima login identity for `vm init`.
	// Production uses the current OS user; tests inject a fixed name.
	lookupOperatorUser func() (string, error)
}

// Run builds the command tree, executes it, and returns the process exit code.
// stdout carries human-readable or JSON machine output; stderr carries
// diagnostics only. Errors are mapped to the contract exit-code table and, in
// JSON mode, rendered as a single error envelope on stdout.
// The backends this build knows. Registration happens here, in the composition
// root, rather than in each implementation's init: importing a backend must not
// be what decides whether an instance can select it.
func init() {
	backend.Register(lima.Hermes())
	backend.Register(claudecode.New())
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) int {
	a := &app{stdout: stdout, stderr: stderr, build: build}
	return runWithApp(ctx, a, args)
}

// runWithApp builds and executes the command tree for a preconfigured app. Run
// is the production wrapper; tests call this directly to inject a fake Lima
// adapter via a.newLima before dispatch. Any unset seam defaults to production
// wiring so the two paths stay identical everywhere else.
func runWithApp(ctx context.Context, a *app, args []string) int {
	if a.newLima == nil {
		a.newLima = defaultNewLima
	}
	if a.newServe == nil {
		a.newServe = func() *serve.Adapter { return serve.New(a.newLima(), a.backend) }
	}
	if a.newBrain == nil {
		a.newBrain = func(adapter *lima.Adapter, opts lima.BootstrapOptions) brainService {
			return brain.New(adapter, opts)
		}
	}
	if a.newProjects == nil {
		a.newProjects = func(adapter *lima.Adapter, opts lima.BootstrapOptions) projectService {
			// The registry resolves the same canonical paths this invocation
			// was started with, so --config and the resolved instance apply to
			// the project registry exactly as they do to everything else.
			return projects.New(adapter, projects.FileRegistry{Options: a.configOptions()}, opts)
		}
	}
	// The workspace root is read at call time, not at wiring time: the backend
	// is resolved from the config document during pre-run, which happens after
	// these seams are set.
	if a.newShellSpec == nil {
		a.newShellSpec = func(p string) (execx.InteractiveCommand, error) {
			return lima.OperatorShellSpec(a.backend.Identity().WorkspacePath, p)
		}
	}
	if a.newMediatedShellSpec == nil {
		a.newMediatedShellSpec = func(p, socket string) (execx.InteractiveCommand, error) {
			return lima.MediatedShellSpec(a.backend.Identity().WorkspacePath, p, socket)
		}
	}
	if a.newEnterSpec == nil {
		a.newEnterSpec = func(p string) (execx.InteractiveCommand, error) {
			return lima.ProjectEnterSpec(a.backend.Identity().WorkspacePath, p)
		}
	}
	if a.newAgentSpec == nil {
		a.newAgentSpec = func(p string) (execx.InteractiveCommand, error) {
			session := a.backend.Session()
			helper := ""
			if session != nil {
				helper = session.HelperPath
			}
			return lima.ProjectAgentSpec(helper, a.backend.Identity().WorkspacePath, p)
		}
	}
	if a.newAgentPushSpec == nil {
		a.newAgentPushSpec = func(p, hostSocket, guestSocket string) (execx.InteractiveCommand, error) {
			session := a.backend.Session()
			helper := ""
			if session != nil {
				helper = session.PushHelperPath
			}
			return lima.ProjectAgentPushSpec(helper, a.backend.Identity().WorkspacePath, p, hostSocket, guestSocket)
		}
	}
	if a.newInteractive == nil {
		a.newInteractive = func() execx.InteractiveRunner { return &execx.InteractiveExecRunner{} }
	}
	if a.newMCPLoginSpec == nil {
		a.newMCPLoginSpec = lima.MCPLoginSpec
	}
	if a.installMCP == nil {
		a.installMCP = defaultInstallMCP
	}
	if a.verifyMCP == nil {
		a.verifyMCP = func(ctx context.Context, adapter *lima.Adapter, identity backend.Identity) (lima.MCPBrokerReport, error) {
			return adapter.VerifyMCPBrokerFor(ctx, identity)
		}
	}
	if a.activateMCP == nil {
		a.activateMCP = func(ctx context.Context, adapter *lima.Adapter, identity backend.Identity) (lima.MCPBrokerActivationReport, error) {
			return adapter.ActivateMCPBroker(ctx, identity)
		}
	}
	if a.lookupOperatorUser == nil {
		a.lookupOperatorUser = defaultLookupOperatorUser
	}
	// An unsupported host is rejected here, once, rather than deep inside the
	// first command that needs a pin. The adapter still fails closed on its own
	// (lima.Adapter.profile), but that message would arrive after an operator
	// had already been told to install Lima and create a VM that could never
	// verify. This is a precondition of the machine, not a usage mistake.
	if _, err := lima.HostProfile(); err != nil {
		return fail(a.stdout, a.stderr, firstNonFlag(args), wantsJSON(args),
			&CLIError{Exit: ExitPrecondition, Code: "unsupported_host", Message: err.Error()})
	}

	root := newRootCmd(a)
	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		// A categorized CLIError carries its own exit code; anything else is a
		// Cobra usage/flag/argument error, which maps to a usage error.
		var cerr *CLIError
		if !errors.As(err, &cerr) {
			cerr = usageError(err.Error())
		}
		// The pre-run may not have set jsonOut (e.g. a flag error occurs first),
		// so fall back to scanning args for --json.
		jsonOut := a.jsonOut || wantsJSON(args)
		// A categorized error knows its concrete command (e.g. "vm.status");
		// only early parse/usage errors fall back to scanning args.
		command := cerr.Command
		if command == "" {
			command = firstNonFlag(args)
		}
		return fail(a.stdout, a.stderr, command, jsonOut, cerr)
	}
	return int(ExitOK)
}

// newRootCmd constructs the `torio` root command and its subcommands.
func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "torio",
		Short:         "Torio control-plane CLI",
		SilenceErrors: true, // this package renders errors; Cobra must not
		SilenceUsage:  true,
		// The root is runnable so unknown input reaches RunE and we control the
		// message, rather than Cobra's default handling.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no command given; run 'torio --help'")
			}
			return usageError(fmt.Sprintf("unknown command %q", args[0]))
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			a.logger = newLogger(a.stderr, a.verbose)

			// The managed instance is fixed here, once, before any command can
			// touch a VM or a config path (ADR-0001). --backend names the agent
			// this invocation is about and the instance that runs it follows;
			// TORIO_INSTANCE still names a box directly and wins, so a flag can
			// never redirect an invocation that already named its target. A
			// malformed name is a usage error: falling back to the default
			// would send a command meant for a test VM to the operator's daily
			// one.
			if err := a.resolveInstance(); err != nil {
				return usageError(err.Error())
			}
			lima.InstanceName = a.instance

			// Resolve the configuration: the XDG config path plus --config,
			// loading and strictly validating the on-disk config document. A
			// resolution/validation failure is a usage/schema error (exit 2).
			// config.Load never surfaces secret-shaped material, and the final
			// error renderer redacts known shapes as defense in depth.
			rt, err := config.Load(a.configOptions())
			if err != nil {
				return usageError(err.Error())
			}
			a.runtime = rt

			// The config document's default_timeout feeds the operation timeout
			// policy, but only when --timeout was not explicitly given: an
			// explicit flag always wins over configured defaults.
			timeout := a.timeout
			if !cmd.Flags().Changed("timeout") && rt.File.Timeout > 0 {
				timeout = rt.File.Timeout
			}
			if err := (config.Settings{Timeout: timeout}).Validate(); err != nil {
				return usageError(err.Error())
			}
			a.timeout = timeout

			// The backend is resolved once the document is in hand, before any
			// command can touch the guest. A name this build does not know is a
			// usage error rather than a silent fallback: a document written by a
			// newer Torio must not have its commands run against a different
			// agent than it names.
			b, err := a.resolveBackend()
			if err != nil {
				return err
			}
			a.backend = b

			a.logger.Debug("dispatching command", "command", cmd.Name(), "json", a.jsonOut)
			a.logger.Debug("configuration resolved",
				"config_file", rt.Paths.ConfigFile,
				"config_loaded", rt.ConfigLoaded)
			a.logger.Debug("operation bounded", "timeout", a.timeout)
			a.logger.Debug("backend resolved", "backend", b.Identity().Name)
			return nil
		},
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.CompletionOptions.DisableDefaultCmd = true

	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "emit a single machine-readable JSON document on stdout")
	root.PersistentFlags().BoolVar(&a.verbose, "verbose", false, "emit more redacted diagnostics on stderr")
	root.PersistentFlags().DurationVar(&a.timeout, "timeout", config.DefaultTimeout, "bound the operation; cannot exceed the policy maximum")
	root.PersistentFlags().StringVar(&a.configPath, "config", "", "path to an explicit non-secret config file")
	root.PersistentFlags().StringVar(&a.backendName, "backend", "",
		"agent backend this invocation is about; selects the instance that runs it (default hermes)")

	root.AddCommand(newVersionCmd(a))
	root.AddCommand(newVMCmd(a))
	root.AddCommand(newServeCmd(a))
	root.AddCommand(newBrainCmd(a))
	root.AddCommand(newProjectCmd(a))
	root.AddCommand(newBackendCmd(a))
	root.AddCommand(newMCPCmd(a))
	return root
}

// configOptions are the config inputs of this invocation. Everything that
// resolves a path — the loader, the project registry — must build its Options
// through here, so one invocation can never read two instances.
func (a *app) configOptions() config.Options {
	return config.Options{ConfigPath: a.configPath, Instance: a.instance}
}

// resolveInstance settles which box this invocation talks to.
//
// The instance is derived from the backend rather than recorded against it. A
// table of instance names would make the operator responsible for a fact Torio
// can compute, and would give two places to disagree about which box runs which
// agent; deriving it means the answer is the same every time it is asked. An
// unknown backend name is rejected here rather than deriving an instance for a
// backend this build cannot run.
func (a *app) resolveInstance() error {
	if a.backendName != "" {
		if _, err := backend.Lookup(a.backendName); err != nil {
			return err
		}
	}
	derived, err := config.InstanceForBackend(a.backendName, backend.DefaultName)
	if err != nil {
		return err
	}
	// Derived first, then resolved: ResolveInstance is what decides whether the
	// environment or the derivation wins, and that decision belongs in one
	// place rather than being re-made by every caller.
	instance, err := config.ResolveInstance(config.Options{ConfigPath: a.configPath, Instance: derived})
	if err != nil {
		return err
	}
	a.instance = instance
	return nil
}

// resolveBackend settles which agent this invocation is about, from what the
// instance declares and what --backend asked for.
//
// The two must agree. They can only disagree when TORIO_INSTANCE named a box
// directly — a derived instance is named after the backend that derived it —
// and that disagreement is exactly the mistake worth stopping: a guest built
// for one identity driven as another. An absent declaration means the default
// backend, so it is compared as the default rather than as "unset", or
// `--backend claude-code` against the Hermes box would read as a match.
//
// A document that was never written declares nothing at all, which is the
// ordinary state of a derived instance before `vm init`. There the flag is the
// declaration.
func (a *app) resolveBackend() (backend.Backend, error) {
	declared := a.runtime.File.Backend
	if declared == "" {
		declared = backend.DefaultName
	}
	if a.backendName != "" {
		if a.runtime.ConfigLoaded && declared != a.backendName {
			return nil, &CLIError{
				Exit: ExitUsage,
				Code: "BACKEND_MISMATCH",
				Message: fmt.Sprintf(
					"instance %q runs backend %q, not %q; drop %s or pass --backend %s",
					a.instance, declared, a.backendName, config.InstanceEnvKey, declared),
			}
		}
		declared = a.backendName
	}
	b, err := backend.Lookup(declared)
	if err != nil {
		return nil, usageError(err.Error())
	}
	return b, nil
}

// newLogger returns a slog logger writing diagnostics to w (stderr). Level is
// debug when verbose, otherwise warn so non-verbose runs stay quiet.
func newLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// opContext derives the bounded operation context from the command context and
// the validated timeout. The caller must call the returned cancel func.
func (a *app) opContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), a.timeout)
}

// wantsJSON reports whether args request JSON output, used only to render an
// error envelope when a flag/parse error prevents normal flag binding.
//
// It recognizes exactly the spellings pflag accepts for a long bool flag, so
// this fallback and normal binding never disagree about what was asked for. A
// single-dash `-json` is not one of them: pflag reads it as a cluster of short
// flags, so it could never have bound the flag it looks like.
//
// The scan stops at the first bare `--`: everything from there on is the
// operator's payload for a remote command (`torio vm ssh -- echo --json`), not
// a request for torio's own output mode. Only flags the operator gave to torio
// itself, before the terminator, can select the machine envelope.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "--json", "--json=true":
			return true
		}
	}
	return false
}

// firstNonFlag returns the first non-flag argument, used as the envelope
// command name for early errors. It returns "" if there is none.
func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}
