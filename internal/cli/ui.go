package cli

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/brain"
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
		Long: "Open the setup, status, project, Brain, service, and MCP hub. Bare `torio` " +
			"opens the same hub on a terminal. The hub emits no JSON; use the individual " +
			"commands for machine-readable output.",
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
	identity := a.backend.Identity()
	session := a.backend.Session()

	d := tui.Deps{
		Instance:    a.instance,
		Backend:     identity.Name,
		Version:     a.build.Version,
		Timeout:     a.timeout,
		LongTimeout: config.MaxTimeout,

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
		VMStop:  adapter.Stop,
		// The same session `vm shell` opens, from the same seam, so the two
		// surfaces cannot drift about what a box shell is.
		VMShellSpec: a.newVMShellSpec,

		Bootstrap: func(ctx context.Context, verifyOnly bool) (lima.BootstrapReport, error) {
			o := opts
			o.VerifyOnly = verifyOnly
			return adapter.Bootstrap(ctx, o)
		},
		CredentialState: func(rep lima.BootstrapReport) string {
			return credentialState(rep, a.backend.StatusChecks().Auth)
		},

		BrainStatus: brainSvc.Status,
		BrainSync:   brainSvc.Sync,
		BrainInit: func(ctx context.Context) error {
			_, err := brainSvc.Init(ctx)
			return err
		},
		BrainImport: func(ctx context.Context, source, into string, dryRun bool) (brain.TransferReport, error) {
			return brainSvc.Import(ctx, brain.ImportOptions{Source: source, Into: into, DryRun: dryRun})
		},

		ProjectList: projectSvc.List,
		ProjectAdd: func(ctx context.Context, req tui.ProjectAddRequest) (*projects.DeployKey, error) {
			// An id with no source of its own means the project is already on
			// record and this backend's guest is the one lacking the checkout.
			// The remote and the display name both come from the record, the
			// way the one-argument `project add` takes them: retyping either
			// would attach a different repository, or be refused as a conflict,
			// under a name that already means something.
			//
			// A local project and a bundle attach are decisions, not lookups:
			// neither reads the registry, because both create what is not there.
			name := req.ID
			remote := req.Remote
			if remote == "" && !req.Local && req.Bundle == "" {
				known, err := findRegistered(projectSvc, req.ID)
				if err != nil {
					return nil, err
				}
				remote, name = known.Remote, known.DisplayName
			}
			report, err := projectSvc.Add(ctx, projects.AddRequest{
				ID:          req.ID,
				DisplayName: name,
				Remote:      remote,
				Local:       req.Local,
				BundlePath:  req.Bundle,
			})
			if err != nil {
				return report.DeployKey, err
			}
			return nil, nil
		},
		ProjectShow: func(ctx context.Context, id string) (projects.ShowReport, error) {
			return projectSvc.Show(ctx, id)
		},
		ProjectRemove: func(ctx context.Context, id string) error {
			_, err := projectSvc.Remove(ctx, id)
			return err
		},
		ProjectMaterialize: func(ctx context.Context, id string) (*projects.DeployKey, error) {
			// The one-argument `add`: the remote and the display name come off
			// the record, so opening a project on a second backend attaches
			// nothing new and renames nothing.
			known, err := findRegistered(projectSvc, id)
			if err != nil {
				return nil, err
			}
			report, err := projectSvc.Add(ctx, projects.AddRequest{
				ID:          id,
				DisplayName: known.DisplayName,
				Remote:      known.Remote,
			})
			if err != nil {
				return report.DeployKey, err
			}
			return nil, nil
		},
		ProjectSetRemote: func(ctx context.Context, id, remote string) (*projects.DeployKey, error) {
			report, err := projectSvc.SetRemote(ctx, id, remote)
			if err != nil {
				return report.DeployKey, err
			}
			return nil, nil
		},

		Poll: func(ctx context.Context) (status.Report, error) {
			return a.newPoller().Poll(ctx)
		},

		// The MCP seams are the commands' own calls on the commands' own
		// adapter functions, credential-free on every path (ADR-0004).
		MCPStatus: func(ctx context.Context) (lima.MCPBrokerReport, error) {
			return a.verifyMCP(ctx, adapter, identity)
		},
		MCPInstall: func(ctx context.Context) (lima.MCPBrokerInstallReport, error) {
			return a.installMCP(ctx, adapter, identity)
		},
		MCPLoginSpec: a.newMCPLoginSpec,
		MCPActivate: func(ctx context.Context) (lima.MCPBrokerActivationReport, error) {
			return a.activateMCP(ctx, adapter, identity)
		},

		// The same composed text `status setup <surface>` prints, from the
		// same functions, so the two surfaces cannot show different recipes.
		StatusSetup: func(surface string) (string, error) {
			return statusSetupSnippet(surface, shellQuote(a.executable()))
		},
		StatusSurfaces: statusSurfaces,

		Backends: backend.Names(),
		Rebind:   a.rebindDeps,
	}

	// A session the backend does not declare stays nil, so the screens report a
	// capability the backend lacks rather than offering an action that fails.
	if session != nil {
		d.LoginSpec = func() (execx.InteractiveCommand, error) {
			return lima.BackendLoginSpec(session.LoginArgv)
		}
		// Each session seam is the command's own sequence: preflight the
		// project, then build the argv from the path the preflight verified.
		// `project agent` and `project shell` do exactly this, so a checkout
		// that drifted refuses here with the reason and the remedy rather than
		// reaching the guest helper and coming back as an exit status.
		d.AgentSpec = func(ctx context.Context, id string) (execx.InteractiveCommand, error) {
			session, err := projectSvc.EnterPreflight(ctx, id)
			if err != nil {
				return execx.InteractiveCommand{}, err
			}
			return a.newAgentSpec(session.Project.Path)
		}
		d.ShellSpec = func(ctx context.Context, id string) (execx.InteractiveCommand, error) {
			session, err := projectSvc.ShellPreflight(ctx, id)
			if err != nil {
				return execx.InteractiveCommand{}, err
			}
			return a.newShellSpec(session.Project.Path)
		}
	}
	return d, nil
}

// rebindDeps re-runs, for one backend name, the resolution the pre-run did for
// this invocation: derive the instance, load and strictly validate its config
// document, require the instance's declaration to agree, and rebuild every
// seam (ADR-0021). Resolution stays in this layer; the hub only swaps what it
// is handed. A failure at any step restores the old binding untouched, so the
// hub keeps acting on the box it was on.
//
// The settled timeout is kept: a per-instance default_timeout applies at
// launch, where an explicit flag can still override it, and a rebind has no
// flags to lose.
func (a *app) rebindDeps(name string) (tui.Deps, error) {
	prevName, prevInstance := a.backendName, a.instance
	prevRuntime, prevBackend := a.runtime, a.backend
	restore := func() {
		a.backendName, a.instance = prevName, prevInstance
		a.runtime, a.backend = prevRuntime, prevBackend
		lima.InstanceName = prevInstance
	}

	a.backendName = name
	if err := a.resolveInstance(); err != nil {
		restore()
		return tui.Deps{}, err
	}
	lima.InstanceName = a.instance
	rt, err := config.Load(a.configOptions())
	if err != nil {
		restore()
		return tui.Deps{}, err
	}
	a.runtime = rt
	b, err := a.resolveBackend()
	if err != nil {
		restore()
		return tui.Deps{}, err
	}
	a.backend = b
	d, err := a.tuiDeps()
	if err != nil {
		restore()
		return tui.Deps{}, err
	}
	return d, nil
}
