package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/serve"
	"github.com/wzslr321/torio/internal/status"
)

// fakeDeps is a hub wired to nothing: every operation records that it was asked
// for and answers immediately. No test here reaches a VM, a guest or a clock.
type fakeDeps struct {
	calls []string

	boxState  lima.State
	bootErr   error
	credState string

	projectList []projects.Project
	serveReport serve.StatusReport
	brainReport brain.StatusReport
	pollReport  status.Report

	failWith  error
	deployKey *projects.DeployKey
	serveErr  error
}

func (f *fakeDeps) record(name string) error {
	f.calls = append(f.calls, name)
	return f.failWith
}

func (f *fakeDeps) called(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// deps builds the seam struct. Fields the test leaves nil stay nil, which is
// how a capability the backend does not declare is expressed.
func (f *fakeDeps) deps() Deps {
	return Deps{
		Instance:        "torio",
		Backend:         "hermes",
		Version:         "1.2.3",
		ServiceDeclared: true,
		Timeout:         time.Second,
		LongTimeout:     time.Second,

		VMStatus: func(context.Context) (lima.Status, error) {
			return lima.Status{State: f.boxState}, nil
		},
		VMInit:  func(context.Context, VMInitOptions) error { return f.record("vm-init") },
		VMStart: func(context.Context) error { return f.record("vm-start") },

		Bootstrap: func(_ context.Context, verifyOnly bool) (lima.BootstrapReport, error) {
			if verifyOnly {
				return lima.BootstrapReport{}, f.bootErr
			}
			return lima.BootstrapReport{}, f.record("bootstrap")
		},
		CredentialState: func(lima.BootstrapReport) string { return f.credState },

		ServeStatus:  func(context.Context) (serve.StatusReport, error) { return f.serveReport, f.serveErr },
		ServeInstall: func(context.Context) error { return f.record("serve-install") },
		ServeStart:   func(context.Context) error { return f.record("serve-start") },
		ServeStop:    func(context.Context) error { return f.record("serve-stop") },
		ServeRestart: func(context.Context) error { return f.record("serve-restart") },
		ServeLogs: func(context.Context, int) (string, error) {
			return "line one\nline two", f.record("serve-logs")
		},

		BrainStatus: func(context.Context) (brain.StatusReport, error) { return f.brainReport, nil },
		BrainInit:   func(context.Context) error { return f.record("brain-init") },

		ProjectList: func() ([]projects.Project, error) { return f.projectList, nil },
		ProjectAdd: func(_ context.Context, id, _ string) (*projects.DeployKey, error) {
			if err := f.record("project-add:" + id); err != nil {
				return f.deployKey, err
			}
			return nil, nil
		},
		ProjectUse:    func(_ context.Context, id string) error { return f.record("project-use:" + id) },
		ProjectRemove: func(_ context.Context, id string) error { return f.record("project-remove:" + id) },

		Poll: func(context.Context) (status.Report, error) { return f.pollReport, nil },
	}
}

// settled builds a hub whose facts have already been probed, so a test can
// drive a screen without stepping through the probe.
func settled(t *testing.T, f *fakeDeps) *root {
	t.Helper()
	r := newRoot(f.deps())
	drain(t, r, r.probeFacts())
	return r
}

// drain runs a command, feeds what it produces back into the model, and follows
// whatever that asks for next, which is what the bubbletea runtime does. The
// probe is two commands chained through a message, so a helper that stopped
// after the first would leave every test looking at a half-probed box.
//
// The depth bound is a test-only guard: a command that kept asking for itself
// would otherwise hang the suite instead of failing it.
func drain(t *testing.T, r *root, cmd tea.Cmd) {
	t.Helper()
	drainDepth(t, r, cmd, 0)
}

func drainDepth(t *testing.T, r *root, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil {
		return
	}
	if depth > 16 {
		t.Fatal("commands kept producing commands; the model is looping")
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drainDepth(t, r, c, depth+1)
		}
		return
	}
	_, next := r.Update(msg)
	drainDepth(t, r, next, depth+1)
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// press sends a key the way the runtime does and runs whatever it asked for.
func press(t *testing.T, r *root, s string) {
	t.Helper()
	_, cmd := r.Update(key(s))
	drain(t, r, cmd)
}

func TestOperationStopsWithTheHubContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := (&fakeDeps{}).deps()
	d.ctx = ctx
	r := newRoot(d)

	cmd := r.run("work", false, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	cancel()
	msg := cmd().(opMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("operation error = %v, want context cancellation", msg.err)
	}
}

// The probe must not ask a stopped box guest questions. Every answer would be
// a failure to reach it, reported as a fact about setup rather than about the
// box being off.
func TestProbeAsksNoGuestQuestionsOfABoxThatIsNotRunning(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateStopped}
	r := settled(t, f)

	if r.facts.Box != lima.StateStopped {
		t.Fatalf("box = %q, want %q", r.facts.Box, lima.StateStopped)
	}
	if r.facts.Bootstrapped {
		t.Error("a stopped box was reported as bootstrapped")
	}
	if len(f.calls) != 0 {
		t.Errorf("guest operations ran against a stopped box: %v", f.calls)
	}
}

// Between the box answering and the guest answering, the setup screen refuses
// to name a step, because the step would follow from facts nothing has proven.
// The key that runs a step has to refuse for the same reason: on a running box
// the unproven answer is "bootstrap", which is minutes of work the operator did
// not ask for.
func TestNoStepRunsBeforeTheGuestHasAnswered(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	r := newRoot(f.deps())

	// The box half of the probe only. The guest half is the command this
	// returns, deliberately left unrun.
	r.Update(boxMsg{state: lima.StateRunning})
	if !r.probed || r.verified {
		t.Fatalf("wanted a probed, unverified box; probed=%v verified=%v", r.probed, r.verified)
	}

	if keys := r.setup.keys(r); keys != "" {
		t.Errorf("the setup screen offers %q before the guest answered", keys)
	}
	press(t, r, "enter")
	if len(f.calls) != 0 {
		t.Errorf("enter ran %v against a box nothing had been proven about", f.calls)
	}
}

// The wizard's action is the manager call the equivalent command makes.
func TestSetupRunsTheStepTheGraphChose(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateStopped}
	r := settled(t, f)

	press(t, r, "enter")

	if !f.called("vm-start") {
		t.Fatalf("enter on a stopped box did not start it; calls=%v", f.calls)
	}
}

// A box that cannot run gets no action at all. Offering one would loop the
// operator through a command that cannot succeed.
func TestSetupOffersNoActionForAnUnusableBox(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateBroken}
	r := settled(t, f)

	press(t, r, "enter")

	if len(f.calls) != 0 {
		t.Errorf("an unusable box was acted on: %v", f.calls)
	}
	if view := r.View(); !strings.Contains(view, "cannot be used") {
		t.Errorf("view does not report the box as unusable:\n%s", view)
	}
}

// One operation at a time. A second guest command against the same box while
// the first is in flight would race it over the same state.
func TestSecondOperationIsRefusedWhileOneIsInFlight(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateStopped}
	r := settled(t, f)

	if cmd := r.run("first", false, func(context.Context) error { return nil }); cmd == nil {
		t.Fatal("the first operation was refused")
	}
	if cmd := r.run("second", false, func(context.Context) error { return nil }); cmd != nil {
		t.Fatal("a second operation started while one was in flight")
	}
}

// A failed operation names what failed and leaves the hub usable.
func TestFailedOperationIsReportedAndClearedOnEscape(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateStopped, failWith: errors.New("limactl said no")}
	r := settled(t, f)

	press(t, r, "enter")

	if r.errText == "" {
		t.Fatal("a failed operation reported nothing")
	}
	if !strings.Contains(r.errText, "limactl said no") {
		t.Errorf("error text = %q, want it to carry the cause", r.errText)
	}
	if r.busy != "" {
		t.Errorf("the hub is still busy after a failure: %q", r.busy)
	}

	press(t, r, "esc")
	if r.errText != "" {
		t.Errorf("escape left the error on screen: %q", r.errText)
	}
}

// A failed add can carry the deploy key the guest now holds (ADR-0018). The
// command surface prints that key; a hub that drops it tells the operator to
// add a key they cannot see (#45). The key must reach the screen with the host
// to authorize it on and the write-access warning, and escape must take it
// away with the failure it belongs to.
func TestFailedAddShowsTheDeployKey(t *testing.T) {
	f := &fakeDeps{
		boxState: lima.StateRunning,
		failWith: errors.New("the guest cannot read the remote yet"),
		deployKey: &projects.DeployKey{
			PublicKey: "ssh-ed25519 AAAATESTKEY torio:p1",
			Host:      "github.com",
			KeyPath:   "/home/agent/.ssh/torio/p1",
			Generated: true,
		},
	}
	r := settled(t, f)

	press(t, r, "3")
	press(t, r, "a")
	press(t, r, "p")
	press(t, r, "1")
	press(t, r, "enter")

	view := r.View()
	if !strings.Contains(view, "ssh-ed25519 AAAATESTKEY torio:p1") {
		t.Errorf("a failed add did not put the public key on screen:\n%s", view)
	}
	if !strings.Contains(view, "github.com") {
		t.Errorf("the view does not name the host to authorize the key on:\n%s", view)
	}
	if !strings.Contains(view, "write access off") {
		t.Errorf("the view does not carry the write-access warning:\n%s", view)
	}

	press(t, r, "esc")
	if strings.Contains(r.View(), "ssh-ed25519 AAAATESTKEY") {
		t.Error("escape left the key on screen")
	}
}

// A failure with no key on offer stays exactly what it was: a banner alone.
func TestFailedAddWithoutAKeyShowsNoDetailBlock(t *testing.T) {
	f := &fakeDeps{
		boxState: lima.StateRunning,
		failWith: errors.New("the box is not running"),
	}
	r := settled(t, f)

	press(t, r, "3")
	press(t, r, "a")
	press(t, r, "p")
	press(t, r, "enter")

	if r.errDetail != "" {
		t.Errorf("a keyless failure carried a detail block: %q", r.errDetail)
	}
}

// While a form has the keyboard, a typed "q" is a character, not a quit. This
// is the difference between typing a remote and losing it.
func TestTypingInAFormDoesNotQuitTheHub(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning, credState: "not-applicable"}
	r := settled(t, f)

	r.switchTo(screenProjects)
	r.projects.openForm()

	_, cmd := r.Update(key("q"))
	if isQuit(cmd) {
		t.Fatal("typing q in a form quit the hub")
	}
	if got := r.projects.fields[0].Value(); got != "q" {
		t.Errorf("field value = %q, want the typed character", got)
	}
}

// The same rule holds for the setup form, which is the first field a new
// operator ever types into. A screen left out of the capture check loses what
// was typed to the first character that happens to be a global key.
func TestTypingInTheSetupFormDoesNotQuitTheHub(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateNotFound}
	r := settled(t, f)

	press(t, r, "enter")
	if !r.setup.capturing() {
		t.Fatal("the create step did not open a form")
	}

	_, cmd := r.Update(key("q"))
	if isQuit(cmd) {
		t.Fatal("typing q in the setup form quit the hub")
	}
}

// The footer is the only place the hub says which keys are live. A form owns
// every key except the one that gives the keyboard back, so a footer that still
// offered the global ones would be naming keys that do nothing.
func TestTheFooterOffersNoGlobalKeysWhileAFormOwnsTheKeyboard(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateNotFound}
	r := settled(t, f)

	press(t, r, "enter")
	if !r.setup.capturing() {
		t.Fatal("the create step did not open a form")
	}

	footer := r.footer()
	for _, offered := range []string{"q quit", "tab screen", "r refresh"} {
		if strings.Contains(footer, offered) {
			t.Errorf("the form footer offers %q, which the form itself consumes: %s", offered, footer)
		}
	}
	if !strings.Contains(footer, "esc cancel") {
		t.Errorf("the form footer does not say how to give the keyboard back: %s", footer)
	}
}

// Outside a form the same key still quits.
func TestQuitOutsideAFormStillQuits(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	r := settled(t, f)

	_, cmd := r.Update(key("q"))
	if !isQuit(cmd) {
		t.Fatal("q outside a form did not quit")
	}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// A backend with no session still owes the operator a way forward on Enter.
// Its way into a project is the gateway, so the key opens a panel with the
// service's state and the tunnel that reaches it, in the words the runbooks
// use (docs/content/blocks/tunnel.md, desktop-connect.md). A failure banner
// here taught the operator that selecting a project is broken, when the
// backend was doing exactly what it declares.
func TestEnterOpensTheGatewayPanelWhenTheBackendHasNoSession(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
		serveReport: serve.StatusReport{
			ServiceDeclared: true,
			Installed:       true,
			Enabled:         true,
			Active:          true,
			ActiveState:     "active",
			EndpointReady:   true,
			EndpointCode:    200,
			Version:         "1.0",
			Ready:           true,
			URL:             "http://127.0.0.1:9119/api/status",
		},
	}
	r := settled(t, f)
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(r.deps))

	press(t, r, "enter")

	if r.busy != "" {
		t.Errorf("a session was started for a backend that declares none: %q", r.busy)
	}
	if r.errText != "" {
		t.Fatalf("enter reported a failure instead of the way forward: %q", r.errText)
	}
	view := r.View()
	for _, want := range []string{
		"ssh -F ~/.lima/torio/ssh.config",
		"-L 19119:127.0.0.1:9119",
		"lima-torio",
		"http://127.0.0.1:19119",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the gateway panel does not carry %q:\n%s", want, view)
		}
	}

	press(t, r, "esc")
	if strings.Contains(r.View(), "ssh -F") {
		t.Error("escape left the gateway panel on screen")
	}
}

// A gateway that is not ready is reported as what it is, with the tab that
// fixes it, not as a hub failure and not as a tunnel that would dial a dead
// endpoint as if nothing were wrong.
func TestGatewayPanelNamesTheServeTabWhenTheServiceIsNotReady(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
		serveReport: serve.StatusReport{
			ServiceDeclared: true,
			Installed:       true,
			ActiveState:     "inactive",
			URL:             "http://127.0.0.1:9119/api/status",
		},
		serveErr: errors.New("service is \"inactive\", not active; run `torio serve start`"),
	}
	r := settled(t, f)
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(r.deps))

	press(t, r, "enter")

	view := r.View()
	if !strings.Contains(view, "installed, not running") {
		t.Errorf("the panel does not carry the service state:\n%s", view)
	}
	if !strings.Contains(view, "Serve tab") {
		t.Errorf("the panel does not name the tab that starts the service:\n%s", view)
	}
}

// A backend with neither a session nor a service has no way forward to offer,
// and the screen reports the missing capability rather than opening a panel
// about a gateway that does not exist.
func TestEnterStillRefusesWhenThereIsNoSessionAndNoService(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	d.ServiceDeclared = false
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "enter")

	if !strings.Contains(r.errText, "no agent session") {
		t.Errorf("error text = %q, want it to name the missing capability", r.errText)
	}
}

// With a session declared the same key resolves the argv and hands over.
func TestProjectsHandsOverToADeclaredAgentSession(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	handed := ""
	d.AgentSpec = func(_ context.Context, id string) (execx.InteractiveCommand, error) {
		handed = id
		return execx.InteractiveCommand{Name: "limactl", Args: []string{"shell"}}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	_, cmd := r.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no work")
	}
	msg := cmd()
	spec, ok := msg.(specMsg)
	if !ok {
		t.Fatalf("enter produced %T, want a resolved session", msg)
	}
	if spec.err != nil {
		t.Fatalf("resolving the session failed: %v", spec.err)
	}
	if handed != "torio" {
		t.Errorf("session opened for %q, want the project the operator picked", handed)
	}
}

// A project id may be far longer than its column. It has to stay separated
// from the remote beside it, or the two read as one string and neither is
// legible.
func TestLongProjectIDStaysSeparatedFromItsRemote(t *testing.T) {
	f := &fakeDeps{
		boxState: lima.StateRunning,
		projectList: []projects.Project{{
			ID:     "lean-triage-sandbox-with-a-very-long-name",
			Remote: "git@github.com:you/thing.git",
		}},
	}
	r := settled(t, f)
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(r.deps))

	for _, line := range strings.Split(r.View(), "\n") {
		if !strings.Contains(line, "git@github.com") {
			continue
		}
		if strings.Contains(line, "namegit@github.com") {
			t.Fatalf("the id runs into the remote with no gap: %q", line)
		}
		return
	}
	t.Fatal("the project row was not rendered")
}

// Removing is the one destructive action on the screen, so it asks first.
func TestRemovingAProjectAsksBeforeItActs(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio"}},
	}
	r := settled(t, f)
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(r.deps))

	press(t, r, "d")
	if f.called("project-remove:torio") {
		t.Fatal("the project was removed without a confirmation")
	}
	if !strings.Contains(r.View(), "Remove torio") {
		t.Error("no confirmation was shown")
	}

	press(t, r, "y")
	if !f.called("project-remove:torio") {
		t.Fatalf("confirming did not remove the project; calls=%v", f.calls)
	}
}

func TestDecliningTheConfirmationKeepsTheProject(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio"}},
	}
	r := settled(t, f)
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(r.deps))

	press(t, r, "d")
	press(t, r, "n")

	if f.called("project-remove:torio") {
		t.Fatal("declining removed the project anyway")
	}
}

// A backend with no service has nothing missing, and must not be rendered as a
// service that is down.
func TestServeScreenReportsAnAbsentServiceAsAbsent(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.ServiceDeclared = false
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenServe)

	view := r.View()
	if !strings.Contains(view, "runs no guest service") {
		t.Errorf("view does not report the absent service:\n%s", view)
	}

	press(t, r, "i")
	if len(f.calls) != 0 {
		t.Errorf("a service operation ran for a backend that declares none: %v", f.calls)
	}
}

// The dashboard must tell three silences apart. A box whose facts could not be
// proven may never render as a quiet one.
func TestDashboardDistinguishesUnknownFromNotApplicable(t *testing.T) {
	f := &fakeDeps{
		boxState: lima.StateRunning,
		pollReport: status.Report{Instances: []status.Instance{{
			Name:     "torio",
			Box:      "running",
			Backend:  status.BackendField{State: status.Known, Name: "hermes"},
			Session:  status.SessionField{State: status.Unknown},
			Waiting:  status.WaitingField{State: status.NotApplicable},
			Progress: status.ProgressField{State: status.Known, AgeSeconds: 240},
		}}},
	}
	r := settled(t, f)
	r.switchTo(screenDashboard)
	drain(t, r, r.dash.load(r.deps))

	view := r.View()
	if !strings.Contains(view, "?") {
		t.Error("an unproven fact does not render as unknown")
	}
	if !strings.Contains(view, "—") {
		t.Error("a not-applicable fact does not render as absent")
	}
	if !strings.Contains(view, "4m") {
		t.Errorf("progress age is missing from:\n%s", view)
	}
}

// A backend blocked on a human is the loudest thing the hub says.
func TestDashboardAnnouncesABackendWaitingOnTheOperator(t *testing.T) {
	f := &fakeDeps{
		boxState: lima.StateRunning,
		pollReport: status.Report{Instances: []status.Instance{{
			Name:    "torio-claude-code",
			Box:     "running",
			Backend: status.BackendField{State: status.Known, Name: "claude-code"},
			Waiting: status.WaitingField{
				State:   status.Known,
				Waiting: true,
				Waits:   []status.Wait{{}, {}},
			},
		}}},
	}
	r := settled(t, f)
	r.switchTo(screenDashboard)
	drain(t, r, r.dash.load(r.deps))

	if view := r.View(); !strings.Contains(view, "needs you 2") {
		t.Errorf("the waiting backend is not announced:\n%s", view)
	}
}

// The dashboard's guidance is the wizard's answer, not a second opinion.
func TestDashboardGuidanceMatchesTheWizard(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateNotFound}
	r := settled(t, f)
	r.switchTo(screenDashboard)
	drain(t, r, r.dash.load(r.deps))

	if view := r.View(); !strings.Contains(view, "Create the box") {
		t.Errorf("the dashboard does not carry the wizard's next step:\n%s", view)
	}
}

// The header names what every operation in this session will act on.
func TestHeaderNamesTheInstanceAndBackend(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	r := settled(t, f)

	view := r.View()
	for _, want := range []string{"torio", "hermes", "running"} {
		if !strings.Contains(view, want) {
			t.Errorf("header does not carry %q:\n%s", want, view)
		}
	}
}

// Coming back from a session re-reads the box, so what the operator did inside
// it cannot leave a stale screen behind.
func TestReturningFromASessionReprobes(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning, credState: "present"}
	r := settled(t, f)
	r.busy = "agent session"

	_, cmd := r.Update(execDoneMsg{})

	if r.busy != "" {
		t.Errorf("the hub is still busy after the session ended: %q", r.busy)
	}
	if cmd == nil {
		t.Fatal("returning from a session did not re-read the box")
	}
	if _, ok := cmd().(boxMsg); !ok {
		t.Error("the work done on return is not a probe")
	}
}

// The rebind chooser is ADR-0021 at the keyboard: b offers every backend the
// build knows, and picking one swaps the whole seam struct, discards every
// probed fact, and probes the new binding from nothing. Nothing on screen may
// survive from the box the hub was on.
func TestRebindSwapsTheBindingAndReprobes(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := f.deps()
	d.ctx = ctx
	d.Backends = []string{"claude-code", "hermes"}
	rebound := ""
	next := &fakeDeps{boxState: lima.StateRunning}
	d.Rebind = func(name string) (Deps, error) {
		rebound = name
		nd := next.deps()
		nd.Backend = name
		return nd, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "b")
	if view := r.View(); !strings.Contains(view, "claude-code") {
		t.Fatalf("the chooser does not offer the other backend:\n%s", view)
	}
	press(t, r, "k")
	press(t, r, "enter")

	if rebound != "claude-code" {
		t.Fatalf("rebound to %q, want claude-code", rebound)
	}
	if r.deps.Backend != "claude-code" {
		t.Errorf("the hub still describes %q", r.deps.Backend)
	}
	if r.deps.ctx != ctx {
		t.Error("the program context did not survive the rebind")
	}
	if !r.probed {
		t.Error("the new binding was not probed")
	}
	if len(r.projects.list) != 0 {
		t.Error("the old binding's projects survived the rebind")
	}
	if view := r.View(); !strings.Contains(view, "claude-code") {
		t.Errorf("the header does not name the new backend:\n%s", view)
	}
}

// A rebind that fails leaves the hub exactly where it was: same binding, same
// screens, with the failure named like any other.
func TestFailedRebindKeepsTheOldBinding(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	d.Rebind = func(string) (Deps, error) {
		return Deps{}, errors.New("instance torio-claude-code has no box")
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "b")
	press(t, r, "k")
	press(t, r, "enter")

	if r.deps.Backend != "hermes" {
		t.Errorf("a failed rebind moved the binding to %q", r.deps.Backend)
	}
	if !strings.Contains(r.errText, "has no box") {
		t.Errorf("error text = %q, want it to carry the cause", r.errText)
	}
	if r.busy != "" {
		t.Errorf("the hub is still busy after a failed rebind: %q", r.busy)
	}
}

// A rebind tears down every seam an operation runs through, so it is refused
// while one is in flight, the same way a second operation is.
func TestRebindIsRefusedWhileAnOperationIsInFlight(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	d.Rebind = func(name string) (Deps, error) { return f.deps(), nil }
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.busy = "bootstrapping the guest"

	press(t, r, "b")

	if r.choosing {
		t.Fatal("the chooser opened while an operation was in flight")
	}
}

// Escape closes the chooser without touching the binding.
func TestEscapeClosesTheChooserWithoutRebinding(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	rebound := false
	d.Rebind = func(string) (Deps, error) { rebound = true; return f.deps(), nil }
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "b")
	if !r.choosing {
		t.Fatal("b did not open the chooser")
	}
	press(t, r, "esc")

	if r.choosing {
		t.Error("escape left the chooser open")
	}
	if rebound {
		t.Error("closing the chooser ran a rebind")
	}
}

// A build that fills no rebind seam has no chooser and offers no key for it.
// The footer naming a dead key would be the footer lying.
func TestNoChooserWhenTheBuildOffersNoRebind(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	r := settled(t, f)

	press(t, r, "b")

	if r.choosing {
		t.Fatal("a chooser opened with no rebind seam to run")
	}
	if strings.Contains(r.footer(), "b backend") {
		t.Errorf("the footer offers a key that does nothing: %s", r.footer())
	}
}

// A rebind is the operator's attention leaving one box for another, and the
// one Brain's promise is only as good as both boxes having synced (ADR-0025).
// The hub reconciles the box it is leaving and the one it arrives at, and its
// note says what each side did, in counts (ADR-0026).
func TestRebindReconcilesTheBrainOnBothSides(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	oldSynced, newSynced := 0, 0
	d.BrainSync = func(context.Context) (brain.SyncReport, error) {
		oldSynced++
		return brain.SyncReport{ToHub: 2}, nil
	}
	next := &fakeDeps{boxState: lima.StateRunning}
	d.Rebind = func(name string) (Deps, error) {
		nd := next.deps()
		nd.Backend = name
		nd.BrainSync = func(context.Context) (brain.SyncReport, error) {
			newSynced++
			return brain.SyncReport{ToGuest: 1}, nil
		}
		return nd, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "b")
	press(t, r, "k")
	press(t, r, "enter")

	if oldSynced != 1 || newSynced != 1 {
		t.Fatalf("synced the old side %d times and the new side %d, want 1 and 1", oldSynced, newSynced)
	}
	if r.deps.Backend != "claude-code" {
		t.Fatalf("the binding did not move: %q", r.deps.Backend)
	}
	if !strings.Contains(r.note, "rebound to claude-code") {
		t.Errorf("note = %q, want the rebind named first", r.note)
	}
	if !strings.Contains(r.note, "hermes") || !strings.Contains(r.note, "2 to the host") {
		t.Errorf("note = %q, want the side that was left and what it carried", r.note)
	}
	if !strings.Contains(r.note, "1 back") {
		t.Errorf("note = %q, want what the new side received", r.note)
	}
}

// The Brain not moving with the operator is something to say, not a reason to
// hold them on the box they were leaving: a sync failure is the note's
// content, never the rebind's failure (ADR-0026).
func TestRebindStillRebindsWhenTheBrainCannotSync(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	d.BrainSync = func(context.Context) (brain.SyncReport, error) {
		return brain.SyncReport{}, errors.New("verify the box first")
	}
	next := &fakeDeps{boxState: lima.StateRunning}
	d.Rebind = func(name string) (Deps, error) {
		nd := next.deps()
		nd.Backend = name
		return nd, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "b")
	press(t, r, "k")
	press(t, r, "enter")

	if r.deps.Backend != "claude-code" {
		t.Fatalf("a sync failure held the binding on %q", r.deps.Backend)
	}
	if r.errText != "" {
		t.Errorf("a sync failure became a rebind failure: %q", r.errText)
	}
	if !strings.Contains(r.note, "not reconciled") || !strings.Contains(r.note, "verify the box first") {
		t.Errorf("note = %q, want the sync outcome and its reason", r.note)
	}
}

// A rebind that fails leaves the old binding in place, but the box being left
// had already been reconciled on the way out; that outcome is kept, and the
// side never arrived at is never synced.
func TestFailedRebindKeepsTheOldBindingAndItsBrainNote(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	synced := 0
	d.BrainSync = func(context.Context) (brain.SyncReport, error) {
		synced++
		return brain.SyncReport{}, nil
	}
	d.Rebind = func(string) (Deps, error) {
		return Deps{}, errors.New("instance torio-claude-code has no box")
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "b")
	press(t, r, "k")
	press(t, r, "enter")

	if synced != 1 {
		t.Fatalf("the side being left was synced %d times, want 1", synced)
	}
	if r.deps.Backend != "hermes" {
		t.Errorf("a failed rebind moved the binding to %q", r.deps.Backend)
	}
	if !strings.Contains(r.errText, "has no box") {
		t.Errorf("error text = %q, want it to carry the cause", r.errText)
	}
	if !strings.Contains(r.note, "hermes") {
		t.Errorf("note = %q, want the outbound reconciliation kept", r.note)
	}
}

// A conflict stops one direction and is resolved in the host vault with Git
// (ADR-0025), so the note has to name that path or the operator knows a
// conflict exists and not where it is settled.
func TestRebindNamesTheHostVaultOnAConflict(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.Backends = []string{"claude-code", "hermes"}
	next := &fakeDeps{boxState: lima.StateRunning}
	d.Rebind = func(name string) (Deps, error) {
		nd := next.deps()
		nd.Backend = name
		nd.BrainSync = func(context.Context) (brain.SyncReport, error) {
			return brain.SyncReport{ConflictInbound: true, HubPath: "/home/op/.local/share/torio/brain/vault"}, nil
		}
		return nd, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "b")
	press(t, r, "k")
	press(t, r, "enter")

	if !strings.Contains(r.note, "conflict") || !strings.Contains(r.note, "/home/op/.local/share/torio/brain/vault") {
		t.Errorf("note = %q, want the conflict and the vault it is resolved in", r.note)
	}
}

// The header must not wait on the guest. A box that is running is knowable in
// one host command, and holding that back until several guest commands finish
// is what leaves an operator looking at an empty frame.
func TestBoxStateIsOnScreenBeforeTheGuestIsAsked(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning, credState: "not-applicable"}
	r := newRoot(f.deps())

	// Only the first half of the probe.
	msg := r.probeFacts()()
	r.Update(msg)

	if !r.probed {
		t.Fatal("the box state was not recorded from the first half of the probe")
	}
	if r.verified {
		t.Fatal("the guest was reported as verified before it was asked")
	}
	if view := r.View(); !strings.Contains(view, "running") {
		t.Errorf("the box state is not on screen yet:\n%s", view)
	}
}

// Until the guest answers, no setup step is named. The facts that would decide
// it have not been proven, and the step they imply would be the wrong one.
func TestNoSetupStepIsNamedBeforeTheGuestAnswers(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning, credState: "not-applicable"}
	r := newRoot(f.deps())
	r.Update(r.probeFacts()())

	if view := r.View(); strings.Contains(view, "Bootstrap the guest") {
		t.Errorf("a setup step was named from unproven facts:\n%s", view)
	}
}

// The seam that opens a session is also the seam that preflights it, so a
// checkout a session cannot be opened in comes back as the reason and the
// remedy, on screen, and no terminal is handed over. Before this the hub
// reached the guest helper with an unverified path and the operator was left
// reading a bare exit status the repaint had already eaten.
func TestProjectsReportsAFailedSessionPreflightAndOpensNothing(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	d.AgentSpec = func(context.Context, string) (execx.InteractiveCommand, error) {
		return execx.InteractiveCommand{}, errors.New(
			"the checkout for \"torio\" is not in a state a session can be opened in (checkout_absent); " +
				"re-run `torio project add torio` to reconcile it")
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	_, cmd := r.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no work")
	}
	msg := cmd()
	spec, ok := msg.(specMsg)
	if !ok {
		t.Fatalf("enter produced %T, want a resolution result", msg)
	}
	if spec.err == nil {
		t.Fatal("the refused preflight produced a session")
	}
	r.Update(spec)
	if !strings.Contains(r.errText, "checkout_absent") {
		t.Errorf("error text = %q, want it to name what drifted", r.errText)
	}
	if !strings.Contains(r.errText, "torio project add torio") {
		t.Errorf("error text = %q, want it to name the remedy", r.errText)
	}
	if r.busy != "" {
		t.Errorf("busy = %q, want the operation released", r.busy)
	}
}

// The footer is the one place the hub says which keys are live, so it must not
// name a key the backend has nothing to answer with.
func TestProjectsFooterOmitsUseWhereThereIsNoRegistry(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	d.ProjectUse = nil
	r := newRoot(d)
	r.switchTo(screenProjects)

	if got := r.projects.keys(r); strings.Contains(got, "u use") {
		t.Errorf("footer = %q, want no use key on a backend that keeps no registry", got)
	}
}

// Where the registry exists the key stays offered.
func TestProjectsFooterKeepsUseWhereThereIsARegistry(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	d.ProjectUse = func(context.Context, string) error { return nil }
	r := newRoot(d)
	r.switchTo(screenProjects)

	if got := r.projects.keys(r); !strings.Contains(got, "u use") {
		t.Errorf("footer = %q, want the use key on a backend that keeps a registry", got)
	}
}

// The tail is bounded because a session can print without limit and the hub
// only needs the end of it: the last thing a helper said before it exited is
// what names the reason.
func TestSessionTailKeepsOnlyTheEnd(t *testing.T) {
	tail := &tailBuffer{max: 8}

	if _, err := tail.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatalf("writing the tail failed: %v", err)
	}
	if got, want := tail.String(), "89abcdef"; got != want {
		t.Errorf("tail = %q, want the last %d bytes, %q", got, tail.max, want)
	}

	if _, err := tail.Write([]byte("XY")); err != nil {
		t.Fatalf("writing the tail failed: %v", err)
	}
	if got, want := tail.String(), "abcdefXY"; got != want {
		t.Errorf("tail = %q, want %q after the second write", got, want)
	}
}

// A session still gets the operator's real terminal; the tail is a copy taken
// on the way past, never a replacement for it.
func TestSessionProcessCopiesStderrWithoutTakingIt(t *testing.T) {
	c, tail, err := sessionProcess(context.Background(), execx.InteractiveCommand{Name: "true"})
	if err != nil {
		t.Fatalf("building the session process failed: %v", err)
	}
	if c.Stderr == nil {
		t.Fatal("the session was given no stderr")
	}
	if _, err := c.Stderr.Write([]byte("torio-agent-session: project directory does not exist\n")); err != nil {
		t.Fatalf("writing to the session stderr failed: %v", err)
	}
	if !strings.Contains(tail.String(), "project directory does not exist") {
		t.Errorf("tail = %q, want it to hold what the session said", tail.String())
	}
}

// A session that ends non-zero leaves the operator looking at a repainted
// screen, so whatever it said on the way out has to come back with the failure.
// Before this the hub showed the exit status alone, which named nothing.
func TestSessionFailureShowsWhatTheSessionSaid(t *testing.T) {
	r := newRoot((&fakeDeps{boxState: lima.StateRunning}).deps())
	r.busy = "agent session in torio"

	r.Update(execDoneMsg{
		err:    errors.New("exit status 64"),
		detail: "torio-agent-session: project directory does not exist",
	})

	if !strings.Contains(r.errText, "exit status 64") {
		t.Errorf("error text = %q, want the exit status", r.errText)
	}
	if !strings.Contains(r.errDetail, "project directory does not exist") {
		t.Errorf("detail = %q, want what the session said before it exited", r.errDetail)
	}
	if r.busy != "" {
		t.Errorf("busy = %q, want the operation released", r.busy)
	}
}

// A session that ended cleanly is not a failure and leaves no banner behind.
func TestCleanSessionEndLeavesNoError(t *testing.T) {
	r := newRoot((&fakeDeps{boxState: lima.StateRunning}).deps())
	r.busy = "agent session in torio"

	r.Update(execDoneMsg{detail: "torio: agent session in torio, running as codex."})

	if r.errText != "" {
		t.Errorf("error text = %q, want none after a clean session", r.errText)
	}
	if r.errDetail != "" {
		t.Errorf("detail = %q, want none after a clean session", r.errDetail)
	}
	if !strings.Contains(r.note, "session ended") {
		t.Errorf("note = %q, want it to say the session ended", r.note)
	}
}

// Everything the hub lists, it has to be able to act on. A record whose remote
// names a host no guest can resolve is corrected here, prefilled with what the
// record holds, so the operator edits an address rather than retyping one
// (ADR-0023).
func TestProjectsCorrectsARemoteFromTheHub(t *testing.T) {
	f := &fakeDeps{
		boxState: lima.StateRunning,
		projectList: []projects.Project{{
			ID:     "lean-triage",
			Remote: "git@gh-lean-triage:leancodepl/lean-triage.git",
			Path:   "/w/lean-triage",
		}},
	}
	d := f.deps()
	gotID, gotRemote := "", ""
	d.ProjectSetRemote = func(_ context.Context, id, remote string) error {
		gotID, gotRemote = id, remote
		return nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "e")
	if !r.projects.editing {
		t.Fatal("e did not open the remote correction")
	}
	if got := r.projects.fields[0].Value(); got != "git@gh-lean-triage:leancodepl/lean-triage.git" {
		t.Errorf("field = %q, want it prefilled with the recorded remote", got)
	}
	// The screen owns the keyboard while it is open, or a typed "q" quits the
	// hub in the middle of an edit.
	if !r.projects.capturing() {
		t.Error("the correction form does not hold the keyboard")
	}

	r.projects.fields[0].SetValue("git@github.com:leancodepl/lean-triage.git")
	_, cmd := r.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no work")
	}
	drain(t, r, cmd)

	if gotID != "lean-triage" {
		t.Errorf("corrected %q, want the selected project", gotID)
	}
	if got, want := gotRemote, "git@github.com:leancodepl/lean-triage.git"; got != want {
		t.Errorf("corrected to %q, want %q", got, want)
	}
	if r.projects.editing {
		t.Error("the form stayed open after it was submitted")
	}
}

// A backend build with no correction seam does not offer the key.
func TestProjectsOffersNoRemoteCorrectionWithoutTheSeam(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	d.ProjectSetRemote = nil
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "e")

	if r.projects.editing {
		t.Error("a correction was opened with no seam to run it")
	}
	if strings.Contains(r.projects.keys(r), "e remote") {
		t.Error("the footer offers a key the build cannot answer")
	}
}

// The whole point of switching backends in the hub: press enter on a project
// this guest has never held, and it is made and opened. Before this the hub
// handed the operator an error naming a command to go and run, which is the
// dead end the rebind was supposed to remove (ADR-0024).
func TestProjectsMaterializesAnAbsentCheckoutThenOpensTheSession(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "lean-triage", Path: "/w/lean-triage"}},
	}
	d := f.deps()
	materialized := ""
	opened := 0
	d.ProjectMaterialize = func(_ context.Context, id string) (*projects.DeployKey, error) {
		materialized = id
		return nil, nil
	}
	d.AgentSpec = func(context.Context, string) (execx.InteractiveCommand, error) {
		opened++
		if materialized == "" {
			return execx.InteractiveCommand{}, &projects.Error{
				Op:     "enter",
				Kind:   projects.KindVerification,
				Issues: []string{"checkout_absent"},
				Err:    errors.New("checkout_absent"),
			}
		}
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "enter")

	if materialized != "lean-triage" {
		t.Errorf("materialized %q, want the project the operator opened", materialized)
	}
	if opened != 2 {
		t.Errorf("the session was resolved %d times, want a retry after the checkout was made", opened)
	}
	if r.errText != "" {
		t.Errorf("error text = %q, want none once the checkout was made", r.errText)
	}
}

// A materialization that fails closed on an authorization leaves the operator
// the key to authorize, and never retries into the same refusal.
func TestProjectsShowsTheDeployKeyWhenMaterializingFails(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "lean-triage", Path: "/w/lean-triage"}},
	}
	d := f.deps()
	opened := 0
	d.ProjectMaterialize = func(context.Context, string) (*projects.DeployKey, error) {
		return &projects.DeployKey{
			PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample torio-deploy-lean-triage",
			Host:      "github.com",
		}, errors.New("the guest cannot read the remote yet")
	}
	d.AgentSpec = func(context.Context, string) (execx.InteractiveCommand, error) {
		opened++
		return execx.InteractiveCommand{}, &projects.Error{
			Op:     "enter",
			Kind:   projects.KindVerification,
			Issues: []string{"checkout_absent"},
			Err:    errors.New("checkout_absent"),
		}
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "enter")

	if opened != 1 {
		t.Errorf("the session was resolved %d times, want no retry after the refusal", opened)
	}
	if !strings.Contains(r.errDetail, "ssh-ed25519") {
		t.Errorf("detail = %q, want the deploy key to authorize", r.errDetail)
	}
}

// Drift that is not an absent checkout is a working tree, and the hub refuses
// it the way it always did rather than cloning over it.
func TestProjectsDoesNotMaterializeOverDriftItMustNotTouch(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "lean-triage", Path: "/w/lean-triage"}},
	}
	d := f.deps()
	called := false
	d.ProjectMaterialize = func(context.Context, string) (*projects.DeployKey, error) {
		called = true
		return nil, nil
	}
	d.AgentSpec = func(context.Context, string) (execx.InteractiveCommand, error) {
		return execx.InteractiveCommand{}, &projects.Error{
			Op:     "enter",
			Kind:   projects.KindVerification,
			Issues: []string{"origin_mismatch"},
			Err:    errors.New("origin_mismatch"),
		}
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "enter")

	if called {
		t.Error("a checkout the hub must not touch was cloned over")
	}
	if !strings.Contains(r.errText, "origin_mismatch") {
		t.Errorf("error text = %q, want the drift reported", r.errText)
	}
}

// One Brain means the hub has to be able to make this box agree with the rest,
// or the operator is sent to a command for the one thing the Brain tab is about
// (ADR-0025).
func TestBrainTabSyncsWithTheHostVault(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	synced := 0
	d.BrainSync = func(context.Context) (brain.SyncReport, error) {
		synced++
		return brain.SyncReport{}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenBrain)
	drain(t, r, r.brain.load(d))

	press(t, r, "y")

	if synced != 1 {
		t.Errorf("sync ran %d times, want 1", synced)
	}
	if !strings.Contains(r.brain.keys(r), "y sync") {
		t.Errorf("footer = %q, want the sync key offered", r.brain.keys(r))
	}
}

// A build with no sync seam does not offer the key.
func TestBrainTabOffersNoSyncWithoutTheSeam(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.BrainSync = nil
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenBrain)

	press(t, r, "y")

	if strings.Contains(r.brain.keys(r), "y sync") {
		t.Errorf("footer = %q, want no sync key", r.brain.keys(r))
	}
}

// Starting a box is offered in the hub and stopping it was not, so the one
// operation an operator runs at the end of a day sent them back to the command
// line. It is asked for before it happens: a box carries the running agent
// sessions, and stopping one is not what a mistyped key should do.
func TestDashboardStopsTheBoxAfterConfirming(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	stopped := 0
	d.VMStop = func(context.Context) error {
		stopped++
		return nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenDashboard)

	press(t, r, "x")
	if stopped != 0 {
		t.Fatal("the box was stopped without asking")
	}
	if !strings.Contains(r.dash.keys(r), "y stop") {
		t.Errorf("footer = %q, want the confirmation offered", r.dash.keys(r))
	}

	press(t, r, "y")
	if stopped != 1 {
		t.Errorf("stop ran %d times, want 1", stopped)
	}
}

// Declining leaves the box alone.
func TestDashboardKeepsTheBoxWhenTheStopIsDeclined(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	stopped := 0
	d.VMStop = func(context.Context) error {
		stopped++
		return nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenDashboard)

	press(t, r, "x")
	press(t, r, "n")

	if stopped != 0 {
		t.Error("the box was stopped after the operator declined")
	}
	if strings.Contains(r.dash.keys(r), "y stop") {
		t.Errorf("footer = %q, want the confirmation closed", r.dash.keys(r))
	}
}

// A box that is not running has nothing to stop.
func TestDashboardOffersNoStopForABoxThatIsNotRunning(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateStopped}
	d := f.deps()
	d.VMStop = func(context.Context) error { return nil }
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenDashboard)

	press(t, r, "x")

	if r.dash.confirmStop {
		t.Error("a stopped box was offered a stop")
	}
	if strings.Contains(r.dash.keys(r), "x stop") {
		t.Errorf("footer = %q, want no stop key for a box that is not running", r.dash.keys(r))
	}
}

// The hub lists projects but could not say what was wrong with one, so an
// operator who saw a session refuse had to leave the hub to find out why. The
// panel is `project show`: what the guest holds, and the markers naming what
// drifted.
func TestProjectsShowsWhatIsWrongWithOneProject(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "lean-triage", Path: "/w/lean-triage"}},
	}
	d := f.deps()
	asked := ""
	d.ProjectShow = func(_ context.Context, id string) (projects.ShowReport, error) {
		asked = id
		return projects.ShowReport{
			Project:  projects.Project{ID: "lean-triage", Remote: "git@github.com:leancodepl/lean-triage.git"},
			Checkout: projects.CheckoutStatus{PathExists: true, Repository: true},
			Issues:   []string{"origin_mismatch", "worktree_dirty"},
		}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "v")

	if asked != "lean-triage" {
		t.Errorf("asked about %q, want the selected project", asked)
	}
	view := r.View()
	for _, want := range []string{"origin_mismatch", "worktree_dirty"} {
		if !strings.Contains(view, want) {
			t.Errorf("panel does not name %q:\n%s", want, view)
		}
	}
	// esc closes it, the way every other panel in the hub closes.
	press(t, r, "esc")
	if r.projects.showID != "" {
		t.Error("the panel stayed open after esc")
	}
}

// A build with no seam does not offer the key.
func TestProjectsOffersNoDetailWithoutTheSeam(t *testing.T) {
	f := &fakeDeps{
		boxState:    lima.StateRunning,
		projectList: []projects.Project{{ID: "torio", Path: "/w/torio"}},
	}
	d := f.deps()
	d.ProjectShow = nil
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenProjects)
	drain(t, r, r.projects.load(d))

	press(t, r, "v")

	if r.projects.showID != "" {
		t.Error("a detail panel opened with no seam to fill it")
	}
	if strings.Contains(r.projects.keys(r), "v show") {
		t.Errorf("footer = %q, want no detail key", r.projects.keys(r))
	}
}

// The box itself is a place an operator sometimes has to stand — reading logs,
// checking a unit — and the hub sent them away to do it. `s` on the dashboard
// opens the login identity's shell inside the bound box, the same session
// `torio vm shell` opens.
func TestDashboardOpensAShellIntoTheBox(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	opened := false
	d.VMShellSpec = func() (execx.InteractiveCommand, error) {
		opened = true
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenDashboard)

	_, cmd := r.Update(key("s"))
	if cmd == nil {
		t.Fatal("s produced no work")
	}
	msg := cmd()
	spec, ok := msg.(specMsg)
	if !ok {
		t.Fatalf("s produced %T, want a resolved session", msg)
	}
	if spec.err != nil {
		t.Fatalf("resolving the shell failed: %v", spec.err)
	}
	if !opened {
		t.Error("the shell seam was never asked")
	}
	if !strings.Contains(r.dash.keys(r), "s shell") {
		t.Errorf("footer = %q, want the shell key offered", r.dash.keys(r))
	}
}

// A stopped box has no shell to open, and a build without the seam has no key.
func TestDashboardOffersNoShellIntoAStoppedBox(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateStopped}
	d := f.deps()
	d.VMShellSpec = func() (execx.InteractiveCommand, error) {
		t.Fatal("a stopped box was asked for a shell")
		return execx.InteractiveCommand{}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenDashboard)

	_, cmd := r.Update(key("s"))
	if cmd != nil {
		t.Fatal("s produced work on a stopped box")
	}
	if strings.Contains(r.dash.keys(r), "s shell") {
		t.Errorf("footer = %q, want no shell key", r.dash.keys(r))
	}
}

// The ambient status line had a printing command and no place in the hub, so
// the surface that shows cross-box status could not say how to put that status
// on a tmux bar or a zsh prompt. `t` on the dashboard picks a surface and shows
// the same recipe `status setup` prints, naming the file it belongs in; the hub
// still writes nothing.
func TestDashboardShowsTheStatusLineRecipe(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.StatusSurfaces = []string{"tmux", "zsh"}
	d.StatusSetup = func(surface string) (string, error) {
		if surface != "zsh" {
			t.Fatalf("surface = %q, want zsh", surface)
		}
		return "# Torio ambient status. Add to ~/.zshrc, then: exec zsh\nadd-zsh-hook precmd torio_status_prompt\n", nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenDashboard)

	if !strings.Contains(r.dash.keys(r), "t status line") {
		t.Fatalf("footer = %q, want the recipe key offered", r.dash.keys(r))
	}
	press(t, r, "t")
	if view := r.View(); !strings.Contains(view, "tmux") || !strings.Contains(view, "zsh") {
		t.Fatalf("the surface picker does not offer both surfaces:\n%s", view)
	}
	press(t, r, "j")
	press(t, r, "enter")
	view := r.View()
	if !strings.Contains(view, "add-zsh-hook precmd torio_status_prompt") {
		t.Errorf("the recipe is not on screen:\n%s", view)
	}
	if !strings.Contains(view, "torio status setup zsh") {
		t.Errorf("the panel does not name the command that prints the recipe cleanly:\n%s", view)
	}
	press(t, r, "esc")
	if view := r.View(); strings.Contains(view, "add-zsh-hook") {
		t.Error("esc did not close the recipe panel")
	}
}

// A build without the seam offers no key for it.
func TestDashboardOffersNoStatusLineRecipeWithoutTheSeam(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	r := settled(t, f)
	r.switchTo(screenDashboard)

	press(t, r, "t")

	if strings.Contains(r.dash.keys(r), "t status line") {
		t.Errorf("footer = %q, want no recipe key", r.dash.keys(r))
	}
}

// Importing a vault was the one Brain operation with no hub answer. `m` opens
// a form for the host directory and an optional contained subtree, runs the
// preflight first — the same `--dry-run` the command offers — and shows what
// would move before anything does; the second enter is the import.
func TestBrainTabImportsAVaultThroughItsPreflight(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	var dryRuns []bool
	d.BrainImport = func(_ context.Context, source, into string, dryRun bool) (brain.TransferReport, error) {
		if source != "/tmp/notes" || into != "inbox" {
			t.Fatalf("source = %q, into = %q", source, into)
		}
		dryRuns = append(dryRuns, dryRun)
		return brain.TransferReport{DryRun: dryRun, Files: 3, Markdown: 2, Attachments: 1, Bytes: 42}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenBrain)
	drain(t, r, r.brain.load(d))

	if !strings.Contains(r.brain.keys(r), "m import") {
		t.Fatalf("footer = %q, want the import key offered", r.brain.keys(r))
	}
	press(t, r, "m")
	for _, ch := range "/tmp/notes" {
		press(t, r, string(ch))
	}
	press(t, r, "tab")
	for _, ch := range "inbox" {
		press(t, r, string(ch))
	}
	press(t, r, "enter")

	if len(dryRuns) != 1 || !dryRuns[0] {
		t.Fatalf("first enter ran %v, want exactly one preflight", dryRuns)
	}
	view := r.View()
	if !strings.Contains(view, "3 file") {
		t.Errorf("the preflight's counts are not on screen:\n%s", view)
	}
	if !strings.Contains(view, "enter import") {
		t.Errorf("the preflight does not offer the import:\n%s", view)
	}

	press(t, r, "enter")
	if len(dryRuns) != 2 || dryRuns[1] {
		t.Fatalf("second enter ran %v, want the real import after the preflight", dryRuns)
	}
	if !strings.Contains(r.note, "finished") {
		t.Errorf("note = %q, want the import's outcome", r.note)
	}
}

// Esc after the preflight walks away without importing; nothing has moved.
func TestBrainImportPreflightCanBeDeclined(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	imports := 0
	d.BrainImport = func(_ context.Context, _, _ string, dryRun bool) (brain.TransferReport, error) {
		if !dryRun {
			imports++
		}
		return brain.TransferReport{DryRun: dryRun, Files: 1}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenBrain)
	drain(t, r, r.brain.load(d))

	press(t, r, "m")
	for _, ch := range "/tmp/n" {
		press(t, r, string(ch))
	}
	press(t, r, "enter")
	press(t, r, "esc")

	if imports != 0 {
		t.Fatalf("declining the preflight still imported %d time(s)", imports)
	}
	if view := r.View(); strings.Contains(view, "enter import") {
		t.Errorf("the preflight panel survived esc:\n%s", view)
	}
}

// A build without the seam offers no key.
func TestBrainTabOffersNoImportWithoutTheSeam(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.BrainImport = nil
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenBrain)

	press(t, r, "m")

	if strings.Contains(r.brain.keys(r), "m import") {
		t.Errorf("footer = %q, want no import key", r.brain.keys(r))
	}
}

// The MCP boundary had three commands and no screen, which sent the operator
// away for provisioning, for login, and even for reading whether the boundary
// holds. The sixth tab is `mcp status` rendered: the checks, the identity
// separation they establish, and the grant.
func TestMCPTabProvesTheBoundary(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.MCPStatus = func(context.Context) (lima.MCPBrokerReport, error) {
		return lima.MCPBrokerReport{
			Instance:  "torio",
			AgentUser: "hermes",
			Checks:    []lima.CheckResult{{Name: "broker_user", OK: true, Detail: "present"}},
			Policy: lima.PolicyGrant{Digest: "abc123", Services: []lima.PolicyService{
				{Name: "linear", UpstreamEndpoint: "https://mcp.linear.app/sse", Tools: 3, WriteTools: 1},
			}},
		}, nil
	}
	d.MCPInstall = func(context.Context) (lima.MCPBrokerInstallReport, error) {
		return lima.MCPBrokerInstallReport{}, nil
	}
	d.MCPLoginSpec = func(string) (execx.InteractiveCommand, error) {
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenMCP)
	drain(t, r, r.mcp.load(d))

	view := r.View()
	for _, want := range []string{"broker_user", "linear", "3 tool(s)", "abc123"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if !strings.Contains(r.mcp.keys(r), "i install") || !strings.Contains(r.mcp.keys(r), "l login") {
		t.Errorf("footer = %q, want install and login offered", r.mcp.keys(r))
	}
}

// `6` reaches the tab like any other number key.
func TestNumberSixSwitchesToTheMCPTab(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.MCPStatus = func(context.Context) (lima.MCPBrokerReport, error) {
		return lima.MCPBrokerReport{}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())

	press(t, r, "6")

	if r.active != screenMCP {
		t.Fatalf("active screen = %v, want the MCP tab", r.active)
	}
}

// `i` provisions the boundary, and the note carries the one thing left to do
// when the backend only just joined the client group: a running process does
// not gain a group under itself.
func TestMCPTabInstallReportsTheRestart(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.MCPStatus = func(context.Context) (lima.MCPBrokerReport, error) {
		return lima.MCPBrokerReport{}, errors.New("never provisioned")
	}
	d.MCPInstall = func(context.Context) (lima.MCPBrokerInstallReport, error) {
		return lima.MCPBrokerInstallReport{Changed: true, RestartRequired: true}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenMCP)
	drain(t, r, r.mcp.load(d))

	press(t, r, "i")

	if !strings.Contains(r.note, "restart") {
		t.Errorf("note = %q, want the restart named", r.note)
	}
}

// `l` picks a service from the grant — the same names `mcp login <service>`
// takes — opens the login session, and on its end runs the activation the
// command runs, so the broker starts when the last service is signed in.
func TestMCPTabLoginPicksAServiceAndActivates(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.MCPStatus = func(context.Context) (lima.MCPBrokerReport, error) {
		return lima.MCPBrokerReport{Policy: lima.PolicyGrant{Services: []lima.PolicyService{
			{Name: "atlassian"}, {Name: "linear"},
		}}}, nil
	}
	handed := ""
	d.MCPLoginSpec = func(service string) (execx.InteractiveCommand, error) {
		handed = service
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}
	activated := 0
	d.MCPActivate = func(context.Context) (lima.MCPBrokerActivationReport, error) {
		activated++
		return lima.MCPBrokerActivationReport{Activated: true}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenMCP)
	drain(t, r, r.mcp.load(d))

	press(t, r, "l")
	if view := r.View(); !strings.Contains(view, "atlassian") || !strings.Contains(view, "linear") {
		t.Fatalf("the picker does not offer the grant's services:\n%s", view)
	}
	press(t, r, "j")
	_, cmd := r.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no session")
	}
	msg := cmd()
	if spec, ok := msg.(specMsg); !ok || spec.err != nil {
		t.Fatalf("enter produced %T (%v), want a resolved session", msg, msg)
	}
	if handed != "linear" {
		t.Fatalf("session opened for %q, want the service the operator picked", handed)
	}

	drain(t, r, func() tea.Msg { return execDoneMsg{} })

	if activated != 1 {
		t.Fatalf("activation ran %d times, want once after the session", activated)
	}
	if !strings.Contains(r.note, "broker") {
		t.Errorf("note = %q, want the activation outcome", r.note)
	}
}

// A login session that ends non-zero does not activate: the credential was not
// stored, and activation would report a half-truth over the real failure.
func TestMCPTabDoesNotActivateAfterAFailedLogin(t *testing.T) {
	f := &fakeDeps{boxState: lima.StateRunning}
	d := f.deps()
	d.MCPStatus = func(context.Context) (lima.MCPBrokerReport, error) {
		return lima.MCPBrokerReport{Policy: lima.PolicyGrant{Services: []lima.PolicyService{{Name: "linear"}}}}, nil
	}
	d.MCPLoginSpec = func(string) (execx.InteractiveCommand, error) {
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}
	d.MCPActivate = func(context.Context) (lima.MCPBrokerActivationReport, error) {
		t.Fatal("a failed session still ran activation")
		return lima.MCPBrokerActivationReport{}, nil
	}
	r := newRoot(d)
	drain(t, r, r.probeFacts())
	r.switchTo(screenMCP)
	drain(t, r, r.mcp.load(d))

	press(t, r, "l")
	_, _ = r.Update(key("enter"))
	drain(t, r, func() tea.Msg { return execDoneMsg{err: errors.New("exit status 1")} })
}
