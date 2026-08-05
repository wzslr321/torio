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
	jsonOut    bool
	verbose    bool
	timeout    time.Duration
	configPath string
	logger     *slog.Logger

	// runtime is the resolved D2 configuration (paths + config document). It is
	// populated by PersistentPreRunE and consumed by command execution.
	runtime config.Runtime

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
	newEnterSpec   func(projectPath string) (execx.InteractiveCommand, error)
	newShellSpec   func(projectPath string) (execx.InteractiveCommand, error)
	newInteractive func() execx.InteractiveRunner

	// lookupOperatorUser resolves the Lima login identity for `vm init`.
	// Production uses the current OS user; tests inject a fixed name.
	lookupOperatorUser func() (string, error)
}

// Run builds the command tree, executes it, and returns the process exit code.
// stdout carries human-readable or JSON machine output; stderr carries
// diagnostics only. Errors are mapped to the contract exit-code table and, in
// JSON mode, rendered as a single error envelope on stdout.
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
		a.newServe = func() *serve.Adapter { return serve.New(a.newLima()) }
	}
	if a.newBrain == nil {
		a.newBrain = func(adapter *lima.Adapter, opts lima.BootstrapOptions) brainService {
			return brain.New(adapter, opts)
		}
	}
	if a.newProjects == nil {
		a.newProjects = func(adapter *lima.Adapter, opts lima.BootstrapOptions) projectService {
			// The registry resolves the same canonical config path this
			// invocation was started with, so --config applies to the project
			// registry exactly as it does to everything else.
			return projects.New(adapter, projects.FileRegistry{
				Options: config.Options{ConfigPath: a.configPath},
			}, opts)
		}
	}
	if a.newShellSpec == nil {
		a.newShellSpec = lima.OperatorShellSpec
	}
	if a.newEnterSpec == nil {
		a.newEnterSpec = lima.ProjectEnterSpec
	}
	if a.newInteractive == nil {
		a.newInteractive = func() execx.InteractiveRunner { return &execx.InteractiveExecRunner{} }
	}
	if a.lookupOperatorUser == nil {
		a.lookupOperatorUser = defaultLookupOperatorUser
	}
	// The managed instance is fixed here, once, before the command tree runs and
	// before anything can touch a VM or a config path (ADR-0001). It comes from
	// the environment rather than a flag, so it does not depend on flag parsing
	// and cannot be forgotten on an individual invocation. A malformed name is a
	// usage error: falling back to the default would send a command meant for a
	// test VM to the operator's daily one.
	instance, err := config.ResolveInstance(config.Options{})
	if err != nil {
		return fail(a.stdout, a.stderr, firstNonFlag(args), a.jsonOut || wantsJSON(args), usageError(err.Error()))
	}
	lima.InstanceName = instance

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

			// Resolve the D2 configuration: the XDG config path plus --config,
			// loading and strictly validating the on-disk config document. A
			// resolution/validation failure is a usage/schema error (exit 2).
			// config.Load never surfaces secret-shaped material, and the final
			// error renderer redacts known shapes as defense in depth.
			rt, err := config.Load(config.Options{ConfigPath: a.configPath})
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

			a.logger.Debug("dispatching command", "command", cmd.Name(), "json", a.jsonOut)
			a.logger.Debug("configuration resolved",
				"config_file", rt.Paths.ConfigFile,
				"config_loaded", rt.ConfigLoaded)
			a.logger.Debug("operation bounded", "timeout", a.timeout)
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

	root.AddCommand(newVersionCmd(a))
	root.AddCommand(newVMCmd(a))
	root.AddCommand(newServeCmd(a))
	root.AddCommand(newBrainCmd(a))
	root.AddCommand(newProjectCmd(a))
	root.AddCommand(newMCPCmd(a))
	return root
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
