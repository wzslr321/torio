package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
	"github.com/wzslr321/torio/internal/serve"
	"github.com/wzslr321/torio/internal/tui"
)

// hubApp builds an invocation whose terminal answer and hub launch are both
// injected, so no test opens a real terminal or starts a real program.
func hubApp(t *testing.T, tty bool, launched *bool) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	a := &app{stdout: &stdout, stderr: &stderr, build: testBuild()}
	a.isTerminal = func() bool { return tty }
	a.runTUI = func(context.Context) error {
		if launched != nil {
			*launched = true
		}
		return nil
	}
	return a, &stdout, &stderr
}

// The message and exit code a script sees must not move. Everything that runs
// Torio without a terminal, from CI to a shell pipeline, reads this one.
func TestBareTorioWithoutATerminalIsUnchanged(t *testing.T) {
	launched := false
	a, stdout, stderr := hubApp(t, false, &launched)

	code := runWithApp(context.Background(), a, nil)

	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr.String())
	}
	if got, want := stderr.String(), "torio: no command given; run 'torio --help'\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if launched {
		t.Error("the hub was launched without a terminal")
	}
}

// A terminal is what makes the hub possible, so it is what decides.
func TestBareTorioOnATerminalOpensTheHub(t *testing.T) {
	launched := false
	a, stdout, stderr := hubApp(t, true, &launched)

	code := runWithApp(context.Background(), a, nil)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !launched {
		t.Fatal("the hub was not launched on a terminal")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty: the hub owns the terminal, not the envelope", stdout.String())
	}
}

// --json asks for one machine document. The hub is not one, and answering with
// a screen would break the single-envelope rule every command holds to.
func TestBareTorioWithJSONNeverOpensTheHub(t *testing.T) {
	launched := false
	a, stdout, _ := hubApp(t, true, &launched)

	code := runWithApp(context.Background(), a, []string{"--json"})

	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if launched {
		t.Fatal("the hub was launched for a --json invocation")
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v; got %q", err, stdout.String())
	}
	if env["ok"] != false {
		t.Errorf("envelope reports ok=%v, want false", env["ok"])
	}
}

func TestUICommandOpensTheHubOnATerminal(t *testing.T) {
	launched := false
	a, _, stderr := hubApp(t, true, &launched)

	code := runWithApp(context.Background(), a, []string{"ui"})

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !launched {
		t.Fatal("torio ui did not launch the hub")
	}
}

// Asking for the hub where no terminal exists is an unmet precondition of the
// machine, not a mistake in how the command was written: the command is
// spelled correctly and would work on a terminal.
func TestUICommandWithoutATerminalIsAPreconditionFailure(t *testing.T) {
	launched := false
	a, _, stderr := hubApp(t, false, &launched)

	code := runWithApp(context.Background(), a, []string{"ui"})

	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitPrecondition, stderr.String())
	}
	if launched {
		t.Fatal("the hub was launched without a terminal")
	}
	if !strings.Contains(stderr.String(), "terminal") {
		t.Errorf("stderr = %q, want it to say a terminal is required", stderr.String())
	}
}

func TestUICommandRefusesJSON(t *testing.T) {
	launched := false
	a, stdout, _ := hubApp(t, true, &launched)

	code := runWithApp(context.Background(), a, []string{"ui", "--json"})

	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if launched {
		t.Fatal("the hub was launched for a --json invocation")
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v; got %q", err, stdout.String())
	}
}

// An unknown command stays an unknown command. The terminal check applies to
// the empty invocation only, so a typo is never answered with a screen.
func TestUnknownCommandOnATerminalStillFails(t *testing.T) {
	launched := false
	a, _, stderr := hubApp(t, true, &launched)

	code := runWithApp(context.Background(), a, []string{"nope"})

	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if launched {
		t.Fatal("the hub was launched for an unknown command")
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Errorf("stderr = %q, want it to name the unknown command", stderr.String())
	}
}

// The seam must default to a real check, or every invocation in production
// would take whichever branch the zero value happens to mean.
func TestTerminalCheckDefaultsToProductionWiring(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{stdout: &stdout, stderr: &stderr, build: testBuild()}

	// Dispatching a command that needs neither wiring is enough to run the
	// defaulting path in runWithApp.
	if code := runWithApp(context.Background(), a, []string{"version"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if a.isTerminal == nil {
		t.Fatal("isTerminal was left nil")
	}
	if a.runTUI == nil {
		t.Fatal("runTUI was left nil")
	}
	// Test processes have no terminal on their standard streams, so the real
	// check must answer false here. A true would mean it is not really asking.
	if a.isTerminal() {
		t.Error("the default terminal check reports a terminal in a test process")
	}
}

// hubSeamApp builds an invocation whose hub seams are wired the way production
// wires them, over fakes for everything that would reach a box. It exists so a
// test can call one hub seam and see which manager calls it makes.
func hubSeamApp(t *testing.T, service projectService) *app {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return &app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, build: testBuild(),
		// A backend that declares an interactive session, because these are the
		// seams that open one. Hermes runs a service instead and declares none.
		backend:            claudecode.New(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newLima:            func() *lima.Adapter { return lima.New(&fakeLimaRunner{}) },
		newServe:           func() *serve.Adapter { return serve.New(lima.New(&fakeLimaRunner{}), claudecode.New()) },
		newBrain: func(*lima.Adapter, lima.BootstrapOptions) brainService {
			return &fakeBrainService{}
		},
		newProjects: func(*lima.Adapter, lima.BootstrapOptions) projectService {
			return service
		},
	}
}

// The hub opens a session through the same preflight the command surface runs.
// Without it the hub reaches the guest helper with a path nothing verified, and
// a checkout that is not there answers with a bare exit status the operator
// cannot act on (ADR-0019: the hub is a second way to reach the operations,
// never a second implementation of them).
func TestHubAgentSessionPreflightsBeforeItResolvesTheArgv(t *testing.T) {
	service := &fakeProjectService{
		enterSession: projects.EnterSession{EnterSpec: projects.EnterSpec{Project: sampleProject()}},
	}
	var built string
	a := hubSeamApp(t, service)
	a.newAgentSpec = func(path string) (execx.InteractiveCommand, error) {
		built = path
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if d.AgentSpec == nil {
		t.Fatal("the hub was given no agent session seam")
	}
	if _, err := d.AgentSpec(context.Background(), "torio"); err != nil {
		t.Fatalf("opening the session failed: %v", err)
	}

	if service.enterID != "torio" {
		t.Errorf("preflight ran for %q, want the project the operator picked", service.enterID)
	}
	if built != sampleProject().Path {
		t.Errorf("argv built for %q, want the path the preflight verified", built)
	}
}

// A preflight that refuses is the whole point: the operator reads what drifted
// and what fixes it, and no terminal is ever handed to a session that cannot
// work.
func TestHubAgentSessionStopsAtAFailedPreflight(t *testing.T) {
	service := &fakeProjectService{enterErr: errors.New("checkout_absent")}
	built := false
	a := hubSeamApp(t, service)
	a.newAgentSpec = func(string) (execx.InteractiveCommand, error) {
		built = true
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if _, err := d.AgentSpec(context.Background(), "torio"); err == nil {
		t.Fatal("a failed preflight still produced a session")
	}
	if built {
		t.Error("the argv was built after the preflight refused")
	}
}

// The shell seam holds to the same rule, and its preflight is the one that also
// proves the operator has an agent to forward.
func TestHubShellSessionPreflightsBeforeItResolvesTheArgv(t *testing.T) {
	service := &fakeProjectService{
		shellSession: projects.ShellSession{ShellSpec: projects.ShellSpec{Project: sampleProject()}},
	}
	var built string
	a := hubSeamApp(t, service)
	a.newShellSpec = func(path string) (execx.InteractiveCommand, error) {
		built = path
		return execx.InteractiveCommand{Name: "ssh"}, nil
	}

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if _, err := d.ShellSpec(context.Background(), "torio"); err != nil {
		t.Fatalf("opening the shell failed: %v", err)
	}
	if service.shellID != "torio" {
		t.Errorf("preflight ran for %q, want the project the operator picked", service.shellID)
	}
	if built != sampleProject().Path {
		t.Errorf("argv built for %q, want the path the preflight verified", built)
	}
}

// The hub's add form says the remote is optional for a project this backend
// already knows, and this is what makes that true: the id alone completes from
// the shared registry, exactly as the one-argument `project add` does. Both the
// remote and the display name come from the record, because an add that
// renamed the project on the way through would be refused as a conflict.
func TestHubAddWithoutARemoteCompletesFromTheRegistry(t *testing.T) {
	service := &fakeProjectService{
		listOut: []projects.Project{{
			ID:          "lean-triage",
			DisplayName: "Lean Triage",
			Remote:      "git@github.com:leancodepl/lean-triage.git",
		}},
	}
	a := hubSeamApp(t, service)

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if _, err := d.ProjectAdd(context.Background(), tui.ProjectAddRequest{ID: "lean-triage"}); err != nil {
		t.Fatalf("materializing a registered project failed: %v", err)
	}

	if got, want := service.addReq.Remote, "git@github.com:leancodepl/lean-triage.git"; got != want {
		t.Errorf("add used remote %q, want the one on record %q", got, want)
	}
	if got, want := service.addReq.DisplayName, "Lean Triage"; got != want {
		t.Errorf("add used display name %q, want the one on record %q", got, want)
	}
}

// An id with nothing on record has no remote to complete from, and saying so
// is better than sending an empty remote into validation.
func TestHubAddWithoutARemoteRefusesAnUnregisteredID(t *testing.T) {
	service := &fakeProjectService{}
	a := hubSeamApp(t, service)

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	_, err = d.ProjectAdd(context.Background(), tui.ProjectAddRequest{ID: "nothing"})
	if err == nil {
		t.Fatal("an unregistered id was added with no remote")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error = %q, want it to say there is nothing on record", err.Error())
	}
	if service.addReq.ID != "" {
		t.Error("the add ran despite having no remote to run with")
	}
}

// A remote the operator typed is still theirs, and the display name defaults to
// the id the way it always has.
func TestHubAddWithARemoteIsUnchanged(t *testing.T) {
	service := &fakeProjectService{}
	a := hubSeamApp(t, service)

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if _, err := d.ProjectAdd(context.Background(), tui.ProjectAddRequest{ID: "demo", Remote: "https://example.test/demo.git"}); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if got, want := service.addReq.Remote, "https://example.test/demo.git"; got != want {
		t.Errorf("add used remote %q, want %q", got, want)
	}
	if got, want := service.addReq.DisplayName, "demo"; got != want {
		t.Errorf("add used display name %q, want %q", got, want)
	}
}

// `project use` selects the active project in a backend's own registry. A
// backend that keeps no registry has nothing to select, so the hub is given no
// seam and the screen stops offering the key (ADR-0009: an absent capability is
// reported as a state, never offered as an action that fails).
func TestHubHasNoUseSeamOnABackendWithNoRegistry(t *testing.T) {
	a := hubSeamApp(t, &fakeProjectService{})

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if d.ProjectUse != nil {
		t.Error("the hub was given a use seam on a backend that keeps no registry")
	}
}

// On the backend that does keep one, the seam is there.
func TestHubHasAUseSeamOnABackendWithARegistry(t *testing.T) {
	a := hubSeamApp(t, &fakeProjectService{})
	a.backend = lima.Hermes()
	a.newServe = func() *serve.Adapter { return serve.New(lima.New(&fakeLimaRunner{}), lima.Hermes()) }

	d, err := a.tuiDeps()
	if err != nil {
		t.Fatalf("wiring the hub failed: %v", err)
	}
	if d.ProjectUse == nil {
		t.Error("the hub was given no use seam on a backend that keeps a registry")
	}
}
