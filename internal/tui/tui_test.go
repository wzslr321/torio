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
	d.AgentSpec = func(path string) (execx.InteractiveCommand, error) {
		handed = path
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
	if handed != "/w/torio" {
		t.Errorf("session opened in %q, want the project path", handed)
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
