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

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
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
	stateDir   string
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
		// so fall back to scanning args for --json. D1 ingests no runtime
		// secrets, so no registered literals exist yet; known-shape redaction in
		// fail still applies. Later slices pass a populated redactor here.
		jsonOut := a.jsonOut || wantsJSON(args)
		// A categorized error knows its concrete command (e.g. "vm.status");
		// only early parse/usage errors fall back to scanning args.
		command := cerr.Command
		if command == "" {
			command = firstNonFlag(args)
		}
		return fail(a.stdout, a.stderr, command, jsonOut, cerr, nil)
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

			// Resolve the D2 configuration: XDG paths plus --config/--state-dir,
			// loading and strictly validating the on-disk config document. A
			// resolution/validation failure is a usage/schema error (exit 2).
			// config.Load never surfaces secret-shaped material, and the final
			// error renderer redacts known shapes as defense in depth.
			rt, err := config.Load(config.Options{
				ConfigPath: a.configPath,
				StateDir:   a.stateDir,
			})
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
				"config_loaded", rt.ConfigLoaded,
				"state_dir", rt.Paths.StateDir)
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
	root.PersistentFlags().StringVar(&a.stateDir, "state-dir", "", "override the state directory (test/diagnostic)")

	root.AddCommand(newVersionCmd(a))
	root.AddCommand(newVMCmd(a))
	root.AddCommand(newServeCmd(a))
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
func wantsJSON(args []string) bool {
	for _, a := range args {
		switch a {
		case "--json", "-json", "--json=true", "-json=true":
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
