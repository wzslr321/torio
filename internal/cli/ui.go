package cli

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/status"
	"github.com/wzslr321/torio/internal/tui"
)

func newUICmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the interactive hub",
		Long: "Open the setup, status, project, Brain, and service hub. Bare `torio` opens " +
			"the same hub on a terminal. The hub emits no JSON; use the individual commands " +
			"for machine-readable output.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.jsonOut {
				return usageError("torio ui is interactive and emits no JSON document; the individual commands do")
			}
			if !a.isTerminal() {
				return &CLIError{
					Exit:    ExitPrecondition,
					Code:    "NOT_A_TERMINAL",
					Command: "ui",
					Message: "the hub requires a terminal on standard input and output; run an individual command instead",
				}
			}
			return a.launchHub(cmd.Context())
		},
	}
}

// openHubOrUsage is the bare `torio` path.
//
// Only a terminal can show a hub, and only an invocation that did not ask for a
// machine document wants one. Everything else keeps the answer it has always
// had, byte for byte: a script that pipes Torio, a CI job, and a `--json` caller
// all still read the same usage error on stderr and exit 2.
func (a *app) openHubOrUsage(ctx context.Context) error {
	if a.jsonOut || !a.isTerminal() {
		return usageError("no command given; run 'torio --help'")
	}
	return a.launchHub(ctx)
}

// launchHub hands the terminal to the hub.
//
// The logger is silenced first. While the program runs it owns every cell of
// the screen, and a slog line arriving on stderr underneath would corrupt the
// frame rather than inform anybody; --verbose keeps its meaning on every
// command that prints.
func (a *app) launchHub(ctx context.Context) error {
	a.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := a.runTUI(ctx); err != nil {
		return internalError(err.Error())
	}
	return nil
}

// defaultIsTerminal answers whether this invocation has a terminal to draw on.
//
// Both streams are asked. Input is where the hub reads keys from and output is
// where it draws, so a hub is only possible when both are a terminal: `torio |
// less` has a terminal on input alone, and drawing a full-screen program into a
// pipe would produce escape sequences nobody can read and no way to quit them.
func defaultIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// defaultRunTUI is the production hub launch.
func (a *app) defaultRunTUI(ctx context.Context) error {
	deps, err := a.tuiDeps()
	if err != nil {
		return err
	}
	return tui.Run(ctx, deps)
}

// tuiDeps wires the hub to this invocation.
//
// Every field is the same call the equivalent command makes, taken from the
// same seams, so the two surfaces cannot drift into disagreeing about what an
// operation does. Nothing is resolved a second time: the instance, the backend
// and the timeouts are the ones the pre-run settled before dispatch.
func (a *app) tuiDeps() (tui.Deps, error) {
	opUser, err := a.lookupOperatorUser()
	if err != nil {
		return tui.Deps{}, &CLIError{
			Exit:    ExitExternal,
			Code:    "OPERATOR_LOOKUP_FAILED",
			Command: "ui",
			Message: err.Error(),
		}
	}
	opts := lima.BootstrapOptions{OperatorUser: opUser, Backend: a.backend}
	adapter := a.newLima()
	brainSvc := a.newBrain(adapter, opts)
	projectSvc := a.newProjects(adapter, opts)
	serveAdapter := a.newServe()
	identity := a.backend.Identity()
	session := a.backend.Session()

	d := tui.Deps{
		Instance:        a.instance,
		Backend:         identity.Name,
		Version:         a.build.Version,
		ServiceDeclared: a.backend.Service() != nil,
		Timeout:         a.timeout,
		LongTimeout:     config.MaxTimeout,

		VMStatus: adapter.Status,
		VMInit: func(ctx context.Context, o tui.VMInitOptions) error {
			// The declaration is written before the box exists, exactly as
			// `vm init` writes it: a guest provisioned for one backend must
			// never be reachable as another.
			if err := a.declareBackend(a.backendName); err != nil {
				return err
			}
			_, err := adapter.Init(ctx, lima.InitOptions{
				CPUs:         o.CPUs,
				Memory:       o.Memory,
				Disk:         o.Disk,
				OperatorUser: opUser,
				Backend:      a.backend,
			})
			return err
		},
		VMStart: adapter.Start,

		Bootstrap: func(ctx context.Context, verifyOnly bool) (lima.BootstrapReport, error) {
			o := opts
			o.VerifyOnly = verifyOnly
			return adapter.Bootstrap(ctx, o)
		},
		CredentialState: func(rep lima.BootstrapReport) string {
			return credentialState(rep, a.backend.StatusChecks().Auth)
		},

		ServeStatus: serveAdapter.Status,
		ServeInstall: func(ctx context.Context) error {
			_, err := serveAdapter.Install(ctx)
			return err
		},
		ServeStart: func(ctx context.Context) error {
			_, err := serveAdapter.Start(ctx)
			return err
		},
		ServeStop: func(ctx context.Context) error {
			_, err := serveAdapter.Stop(ctx)
			return err
		},
		ServeRestart: func(ctx context.Context) error {
			_, err := serveAdapter.Restart(ctx)
			return err
		},
		ServeLogs: func(ctx context.Context, lines int) (string, error) {
			rep, err := serveAdapter.Logs(ctx, lines)
			return rep.Text, err
		},

		BrainStatus: brainSvc.Status,
		BrainInit: func(ctx context.Context) error {
			_, err := brainSvc.Init(ctx)
			return err
		},

		ProjectList: projectSvc.List,
		ProjectAdd: func(ctx context.Context, id, remote string) error {
			_, err := projectSvc.Add(ctx, projects.AddRequest{ID: id, DisplayName: id, Remote: remote})
			return err
		},
		ProjectUse: func(ctx context.Context, id string) error {
			_, err := projectSvc.Use(ctx, id)
			return err
		},
		ProjectRemove: func(ctx context.Context, id string) error {
			_, err := projectSvc.Remove(ctx, id)
			return err
		},

		Poll: func(ctx context.Context) (status.Report, error) {
			return a.newPoller().Poll(ctx)
		},
	}

	// A session the backend does not declare stays nil, so the screens report a
	// capability the backend lacks rather than offering an action that fails.
	if session != nil {
		d.LoginSpec = func() (execx.InteractiveCommand, error) {
			return lima.BackendLoginSpec(session.LoginArgv)
		}
		d.AgentSpec = a.newAgentSpec
		d.ShellSpec = a.newShellSpec
	}
	return d, nil
}
