// Package cli implements the `hb` command-line surface: command dispatch via
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

	"hermes-box.local/hb/internal/config"
)

// app holds the per-invocation wiring shared across the command tree.
type app struct {
	stdout io.Writer
	stderr io.Writer
	build  BuildInfo

	// Populated from persistent flags / pre-run.
	jsonOut bool
	verbose bool
	timeout time.Duration
	logger  *slog.Logger
}

// Run builds the command tree, executes it, and returns the process exit code.
// stdout carries human-readable or JSON machine output; stderr carries
// diagnostics only. Errors are mapped to the contract exit-code table and, in
// JSON mode, rendered as a single error envelope on stdout.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) int {
	a := &app{stdout: stdout, stderr: stderr, build: build}
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
		return fail(stdout, stderr, firstNonFlag(args), jsonOut, cerr, nil)
	}
	return int(ExitOK)
}

// newRootCmd constructs the `hb` root command and its subcommands.
func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "hb",
		Short:         "Hermes Box control-plane CLI",
		SilenceErrors: true, // this package renders errors; Cobra must not
		SilenceUsage:  true,
		// The root is runnable so unknown input reaches RunE and we control the
		// message, rather than Cobra's default handling.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no command given; run 'hb --help'")
			}
			return usageError(fmt.Sprintf("unknown command %q", args[0]))
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			a.logger = newLogger(a.stderr, a.verbose)
			// Bound the operation with the validated timeout policy.
			if err := (config.Settings{Timeout: a.timeout}).Validate(); err != nil {
				return usageError(err.Error())
			}
			a.logger.Debug("dispatching command", "command", cmd.Name(), "json", a.jsonOut)
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

	root.AddCommand(newVersionCmd(a))
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
