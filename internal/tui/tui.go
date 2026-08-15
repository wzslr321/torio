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

	BrainStatus func(context.Context) (brain.StatusReport, error)
	BrainInit   func(context.Context) error

	ProjectList func() ([]projects.Project, error)
	// ProjectAdd returns the deploy key a failed add left the guest holding,
	// when there is one (ADR-0018). The command surface prints that key; the
	// hub has to render it too, or its failure banner instructs the operator
	// to add a key they cannot see.
	ProjectAdd    func(ctx context.Context, id, remote string) (*projects.DeployKey, error)
	ProjectUse    func(ctx context.Context, id string) error
	ProjectRemove func(ctx context.Context, id string) error

	// Poll is the cross-box status poll the dashboard renders.
	Poll func(context.Context) (status.Report, error)

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
