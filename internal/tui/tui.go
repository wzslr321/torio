// Package tui implements the interactive hub: the screen `torio` opens when it
// is run on a terminal with no command.
//
// The hub owns no logic of its own. Every action it offers is the same manager
// call the equivalent command makes, reached through the Deps seams the command
// layer fills in, so the two surfaces can never drift into disagreeing about
// what an operation does. What the hub adds is the thing a command surface
// cannot: knowing where the operator is in a multi-step setup, and saying so.
//
// Nothing here writes to stderr. While the program owns the terminal, the
// renderer owns every cell of it, and a log line arriving underneath would
// corrupt the frame rather than inform anybody.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/serve"
	"github.com/wzslr321/torio/internal/status"
)

// ProjectAddRequest is the attach the add form asks for.
//
// It is a struct rather than a list of arguments because the three ways a
// project comes into being — cloned from a remote, carried in as a bundle,
// initialized empty — are mutually exclusive, and a caller passing two of them
// should be visible at the call site rather than hidden in argument order
// (ADR-0027).
type ProjectAddRequest struct {
	ID string
	// Remote is the address to clone from. Empty with neither of the two below
	// means the id is already on record and this guest is the one lacking the
	// checkout.
	Remote string
	// Bundle is a Git bundle on the host to attach from.
	Bundle string
	// Local asks for an empty repository and a record with no remote.
	Local bool
}

// VMInitOptions is the shape of `vm init` the hub collects in a form.
type VMInitOptions struct {
	CPUs   int
	Memory string
	Disk   string
}

// Deps is everything the hub can do, as seams the command layer fills in.
//
// It is a struct of functions rather than a set of adapters because the hub
// must not be able to reach anything the command layer did not hand it: the
// instance, the backend and the timeouts of an invocation are resolved once,
// before dispatch, and a hub that rebuilt an adapter could quietly resolve them
// a second time and disagree. It is also what lets every screen be tested
// without a VM.
//
// A nil operation is a capability this build or this backend does not have. The
// screens check before offering it rather than calling and reporting a failure.
type Deps struct {
	ctx context.Context

	// Instance, Backend and Version identify what the header describes.
	Instance string
	Backend  string
	Version  string

	// ServiceDeclared is whether this backend runs a guest service. It is fixed
	// for the life of the program because the backend is resolved before it.
	// Whether the backend has an interactive session is not a flag here: the
	// session seams below are nil when it does not, which is one fact in one
	// place rather than two that can disagree.
	ServiceDeclared bool

	// Timeout bounds an ordinary operation. LongTimeout bounds the ones that
	// legitimately take minutes: bootstrap, brain init, service install.
	Timeout     time.Duration
	LongTimeout time.Duration

	VMStatus func(context.Context) (lima.Status, error)
	VMInit   func(context.Context, VMInitOptions) error
	VMStart  func(context.Context) error
	// VMStop stops this box. Starting one was always in the hub and stopping it
	// was not, which sent the operator to the command line for the one thing
	// they do at the end of a day.
	VMStop func(context.Context) error

	// Bootstrap verifies and, unless verifyOnly, repairs. The hub calls it both
	// ways: verifying to learn where setup stands, repairing when the operator
	// asks for the step.
	Bootstrap func(ctx context.Context, verifyOnly bool) (lima.BootstrapReport, error)
	// CredentialState reads the backend's auth check out of a bootstrap report
	// in the vocabulary `torio backend status` answers in. It is a seam rather
	// than a copy so the hub and the command surface cannot disagree about what
	// counts as a logged-out box.
	CredentialState func(lima.BootstrapReport) string

	ServeStatus  func(context.Context) (serve.StatusReport, error)
	ServeInstall func(context.Context) error
	ServeStart   func(context.Context) error
	ServeStop    func(context.Context) error
	ServeRestart func(context.Context) error
	ServeLogs    func(context.Context, int) (string, error)

	// BrainSync reconciles this box's replica of the one Second Brain with the
	// copy on the host (ADR-0025). It returns what moved, in counts, because a
	// rebind runs it on the operator's behalf and has to say what it did
	// (ADR-0026); counts are all a report may carry. A nil seam is a build
	// that cannot.
	BrainSync func(ctx context.Context) (brain.SyncReport, error)

	BrainStatus func(context.Context) (brain.StatusReport, error)
	BrainInit   func(context.Context) error

	// BrainImport is `brain import`: a host directory carried into the vault
	// through the verified one-shot transport. dryRun is the same preflight
	// the command's --dry-run runs; the hub always preflights first and shows
	// what would move before anything does. The report carries counts and a
	// digest, never a note name (the Brain's privacy boundary).
	BrainImport func(ctx context.Context, source, into string, dryRun bool) (brain.TransferReport, error)

	ProjectList func() ([]projects.Project, error)
	// ProjectAdd returns the deploy key a failed add left the guest holding,
	// when there is one (ADR-0018). The command surface prints that key; the
	// hub has to render it too, or its failure banner instructs the operator
	// to add a key they cannot see.
	ProjectAdd    func(ctx context.Context, req ProjectAddRequest) (*projects.DeployKey, error)
	ProjectUse    func(ctx context.Context, id string) error
	ProjectRemove func(ctx context.Context, id string) error
	// ProjectShow is `project show` for one project: what the guest holds and
	// the markers naming what drifted. The hub lists projects, so the hub is
	// where a broken one is noticed, and reading why had meant leaving it.
	ProjectShow func(ctx context.Context, id string) (projects.ShowReport, error)

	// The MCP boundary (ADR-0004), as the commands see it. MCPStatus proves
	// and reports; MCPInstall provisions; MCPLoginSpec is the interactive
	// OAuth session for one policy service, and MCPActivate is what the login
	// command runs when that session ends — enabling the broker once every
	// service holds a private session. Torio sees no credential on any of
	// these paths.
	MCPStatus    func(context.Context) (lima.MCPBrokerReport, error)
	MCPInstall   func(context.Context) (lima.MCPBrokerInstallReport, error)
	MCPLoginSpec func(service string) (execx.InteractiveCommand, error)
	MCPActivate  func(context.Context) (lima.MCPBrokerActivationReport, error)

	// Poll is the cross-box status poll the dashboard renders.
	Poll func(context.Context) (status.Report, error)

	// StatusSetup is the recipe that puts the ambient status line on one
	// surface — what `status setup <surface>` prints. The hub shows it and
	// writes nothing, the same line the command holds about a dotfile that is
	// the operator's. StatusSurfaces is the command's own list of surfaces, in
	// its order, so the two pickers cannot disagree about what exists.
	StatusSetup    func(surface string) (string, error)
	StatusSurfaces []string

	// Backends is every backend this build can bind, for the rebind chooser
	// (ADR-0021). Rebind re-runs the resolution dispatch ran for this
	// invocation and returns the seams of the new binding; the hub swaps
	// them, discards every probed fact, and probes again from nothing. It
	// still resolves nothing itself. A nil Rebind is a build with no chooser.
	Backends []string
	Rebind   func(backend string) (Deps, error)

	// The interactive handoffs. Each returns the argv of a real session; the
	// hub releases the terminal to it and takes it back when the session ends.
	// A nil field is a session this backend does not have.
	//
	// The two project sessions take a project id rather than a path, because
	// resolving the path is part of the preflight the command surface runs
	// before it opens either of them: what is verified and what is opened have
	// to be the same checkout. The hub passes the id it was told to open and
	// the seam does the rest, so neither surface can drift into opening a
	// session the other would have refused (ADR-0019).
	LoginSpec func() (execx.InteractiveCommand, error)
	AgentSpec func(ctx context.Context, id string) (execx.InteractiveCommand, error)
	ShellSpec func(ctx context.Context, id string) (execx.InteractiveCommand, error)

	// VMShellSpec is the login identity's own shell inside the bound box: what
	// `torio vm shell` opens. It takes nothing, because there is no
	// caller-shaped value in that session at all.
	VMShellSpec func() (execx.InteractiveCommand, error)

	// ProjectMaterialize makes the checkout a registered project describes, on
	// the guest this hub is bound to (ADR-0024). It is the one-argument `add`:
	// the remote comes from the record, so opening a project on a second
	// backend attaches nothing new. A failed materialization may leave a deploy
	// key for the operator to authorize, exactly as a failed add does.
	ProjectMaterialize func(ctx context.Context, id string) (*projects.DeployKey, error)

	// ProjectSetRemote corrects the remote of a project already on record
	// (ADR-0023). It is here because the hub lists the records, so the hub is
	// where a wrong one is seen; sending the operator to a command to fix what
	// the screen is showing them is the dead end this removes.
	//
	// It is also how a local project gets a remote, and that is why it returns
	// a deploy key: the promotion is the moment the guest first has to read the
	// remote, so it is the moment the key first means anything (ADR-0027).
	ProjectSetRemote func(ctx context.Context, id, remote string) (*projects.DeployKey, error)
}

// Run opens the hub and blocks until the operator quits.
//
// The program is bound to ctx, so the signal handling the binary already
// installs tears the hub down the same way it tears down any other command.
func Run(ctx context.Context, d Deps) error {
	d.ctx = ctx
	p := tea.NewProgram(newRoot(d), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	if err != nil && ctx.Err() != nil {
		// A cancelled context is the operator interrupting, which is how the
		// hub is meant to end. It is not a failure of the hub.
		return nil
	}
	return err
}

func (d Deps) parentContext() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}
