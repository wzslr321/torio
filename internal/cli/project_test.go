package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
)

// fakeProjectService records what the command layer asked the manager to do and
// replays a canned report. The project commands own argument shape, output and
// exit-code mapping only; the manager's own behavior is tested in its package.
type fakeProjectService struct {
	remoteAccess      projects.RemoteAccess
	remoteAccessErr   error
	remoteAccessID    string
	remoteAccessWho   projects.SessionIdentity
	remoteAccessCalls int

	addReq    projects.AddRequest
	addReport projects.AddReport
	addErr    error

	listOut []projects.Project
	listErr error

	showID     string
	showReport projects.ShowReport
	showErr    error

	removeID     string
	removeReport projects.RemoveReport
	removeErr    error

	enterID      string
	enterCalls   int
	enterSession projects.EnterSession
	enterErr     error
	enterErrOnce error

	shellID      string
	shellSession projects.ShellSession
	shellErr     error

	// onAdd observes the context an addition runs under, so a test can pin how
	// long the work is allowed to take.
	onAdd func(context.Context)

	setRemoteID     string
	setRemoteURL    string
	setRemoteReport projects.SetRemoteReport
	setRemoteErr    error

	syncID     string
	syncReport projects.SyncReport
	syncErr    error
}

func (f *fakeProjectService) Add(ctx context.Context, req projects.AddRequest) (projects.AddReport, error) {
	f.addReq = req
	if f.onAdd != nil {
		f.onAdd(ctx)
	}
	return f.addReport, f.addErr
}

func (f *fakeProjectService) List() ([]projects.Project, error) { return f.listOut, f.listErr }

func (f *fakeProjectService) Show(_ context.Context, id string) (projects.ShowReport, error) {
	f.showID = id
	return f.showReport, f.showErr
}

func (f *fakeProjectService) Remove(_ context.Context, id string) (projects.RemoveReport, error) {
	f.removeID = id
	return f.removeReport, f.removeErr
}

func (f *fakeProjectService) SetRemote(_ context.Context, id, remote string) (projects.SetRemoteReport, error) {
	f.setRemoteID, f.setRemoteURL = id, remote
	return f.setRemoteReport, f.setRemoteErr
}

func (f *fakeProjectService) Sync(_ context.Context, id string) (projects.SyncReport, error) {
	f.syncID = id
	return f.syncReport, f.syncErr
}

func (f *fakeProjectService) EnterPreflight(_ context.Context, id string) (projects.EnterSession, error) {
	f.enterID = id
	f.enterCalls++
	// enterErrOnce models a checkout that is absent until something makes it:
	// the first preflight refuses, and the one after the materialization does
	// not, which is the whole sequence under test.
	if f.enterErrOnce != nil && f.enterCalls == 1 {
		return projects.EnterSession{}, f.enterErrOnce
	}
	return f.enterSession, f.enterErr
}

func (f *fakeProjectService) ShellPreflight(_ context.Context, id string) (projects.ShellSession, error) {
	f.shellID = id
	return f.shellSession, f.shellErr
}

// remoteAccess defaults to the shape that says nothing: an unset fake reports an
// SSH origin whose host is trusted, so tests about other things are not made to
// care about a probe they did not set up.
func (f *fakeProjectService) RemoteAccess(_ context.Context, id string, who projects.SessionIdentity) (projects.RemoteAccess, error) {
	f.remoteAccessID = id
	f.remoteAccessWho = who
	f.remoteAccessCalls++
	if f.remoteAccessErr != nil {
		return projects.RemoteAccess{}, f.remoteAccessErr
	}
	if f.remoteAccess == (projects.RemoteAccess{}) {
		return projects.RemoteAccess{Transport: projects.TransportSSH, Host: "github.com", HostKnown: true}, nil
	}
	return f.remoteAccess, nil
}

// fakeInteractiveRunner records the exact interactive command it was handed and
// never spawns anything.
type fakeInteractiveRunner struct {
	cmds []execx.InteractiveCommand
	err  error
}

func (f *fakeInteractiveRunner) RunInteractive(_ context.Context, cmd execx.InteractiveCommand) error {
	f.cmds = append(f.cmds, cmd)
	return f.err
}

func sampleProject() projects.Project {
	return projects.Project{
		ID:          "torio",
		DisplayName: "Torio",
		Remote:      "git@github.com:wzslr321/torio.git",
		Path:        claudecode.WorkspacePath + "/torio",
	}
}

func runProjectCLI(t *testing.T, args []string, service projectService, opts ...func(*app)) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:             &stdout,
		stderr:             &stderr,
		build:              testBuild(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newProjects: func(*lima.Adapter, lima.BootstrapOptions) projectService {
			return service
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	code := runWithApp(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

func TestProjectNoSubcommandIsUsage(t *testing.T) {
	code, _, _ := runProjectCLI(t, []string{"project"}, &fakeProjectService{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

func TestProjectCommandsWireLimaAdapterAndOperator(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	wantAdapter := &lima.Adapter{}
	var gotAdapter *lima.Adapter
	var gotOpts lima.BootstrapOptions
	a := &app{
		stdout:             &stdout,
		stderr:             &stderr,
		build:              testBuild(),
		newLima:            func() *lima.Adapter { return wantAdapter },
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newProjects: func(adapter *lima.Adapter, opts lima.BootstrapOptions) projectService {
			gotAdapter = adapter
			gotOpts = opts
			return &fakeProjectService{listOut: []projects.Project{sampleProject()}}
		},
	}
	if code := runWithApp(context.Background(), a, []string{"project", "list", "--json"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotAdapter != wantAdapter {
		t.Fatalf("project manager adapter = %p, want %p", gotAdapter, wantAdapter)
	}
	if gotOpts.OperatorUser != "testop" {
		t.Fatalf("bootstrap operator = %q, want testop", gotOpts.OperatorUser)
	}
}

// TestProjectCommandsCarryTheInstanceBackend is the assertion whose absence let
// every `project` command run as the wrong agent on an instance running another backend.
//
// The project manager derives the guest identity, the workspace, the registry
// and the interactive session from the backend in its options, and falls back to
// the backend Torio shipped first when given none. The CLI passed none. Nothing
// failed loudly: `project add` verified another backend's guest identity on a
// box that has no such user, and `project agent` would have asked for a session
// the fallback backend does not declare.
func TestProjectCommandsCarryTheInstanceBackend(t *testing.T) {
	// Declared the way an instance really declares it — in the config document —
	// so this exercises the resolution the CLI actually performs.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	want := claudecode.New()
	if err := os.MkdirAll(filepath.Join(home, "torio"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := `{"schema_version":"3","backend":"` + want.Identity().Name + `","projects":[]}`
	if err := os.WriteFile(filepath.Join(home, "torio", "config.json"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	var gotOpts lima.BootstrapOptions
	a := &app{
		stdout:             &stdout,
		stderr:             &stderr,
		build:              testBuild(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newProjects: func(_ *lima.Adapter, opts lima.BootstrapOptions) projectService {
			gotOpts = opts
			return &fakeProjectService{listOut: []projects.Project{sampleProject()}}
		},
	}
	if code := runWithApp(context.Background(), a, []string{"project", "list", "--json"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotOpts.Backend == nil {
		t.Fatal("project manager was handed no backend; it silently falls back to the first one Torio shipped")
	}
	if got, wantName := gotOpts.Backend.Identity().Name, want.Identity().Name; got != wantName {
		t.Fatalf("project manager backend = %q, want %q", got, wantName)
	}
}

// --- add ---

func TestProjectAddDefaultsIDToNameAndReportsNextStep(t *testing.T) {
	service := &fakeProjectService{addReport: projects.AddReport{
		Project:    sampleProject(),
		Cloned:     true,
		Registered: true,
	}}
	code, stdout, stderr := runProjectCLI(t,
		[]string{"project", "add", "torio", "git@github.com:wzslr321/torio.git"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	want := projects.AddRequest{ID: "torio", DisplayName: "torio", Remote: "git@github.com:wzslr321/torio.git"}
	if !reflect.DeepEqual(service.addReq, want) {
		t.Fatalf("add request = %+v, want %+v", service.addReq, want)
	}
	if !strings.Contains(stdout, "cloned") {
		t.Errorf("human output does not report the attach state: %q", stdout)
	}
	if !strings.Contains(stdout, claudecode.WorkspacePath+"/torio") {
		t.Errorf("human output does not report the derived path: %q", stdout)
	}
	if !strings.Contains(stdout, "next: torio project enter torio") {
		t.Errorf("human output does not report the exact next step: %q", stdout)
	}
}

func TestProjectAddIDOverride(t *testing.T) {
	service := &fakeProjectService{addReport: projects.AddReport{
		Project:    sampleProject(),
		Adopted:    true,
		Registered: true,
	}}
	code, stdout, stderr := runProjectCLI(t,
		[]string{"project", "add", "Torio", "git@github.com:wzslr321/torio.git", "--id", "torio"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	want := projects.AddRequest{
		ID:          "torio",
		DisplayName: "Torio",
		Remote:      "git@github.com:wzslr321/torio.git",
	}
	if !reflect.DeepEqual(service.addReq, want) {
		t.Fatalf("add request = %+v, want %+v", service.addReq, want)
	}
	if !strings.Contains(stdout, "next: torio project enter torio") {
		t.Errorf("an attached project must hand off to the ordinary terminal: %q", stdout)
	}
}

func TestProjectAddJSONEnvelope(t *testing.T) {
	service := &fakeProjectService{addReport: projects.AddReport{
		Project:    sampleProject(),
		Cloned:     true,
		Registered: true,
		Notes:      []string{"checkout_retained"},
	}}
	code, stdout, stderr := runProjectCLI(t,
		[]string{"project", "add", "torio", "git@github.com:wzslr321/torio.git", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "project.add" {
		t.Fatalf("envelope ok/command = %v/%v", env["ok"], env["command"])
	}
	data, _ := env["data"].(map[string]any)
	if data["id"] != "torio" || data["path"] != claudecode.WorkspacePath+"/torio" {
		t.Fatalf("data id/path = %v/%v", data["id"], data["path"])
	}
	if data["cloned"] != true || data["registered"] != true {
		t.Fatalf("data outcome flags = %+v", data)
	}
	if data["next_step"] != "torio project enter torio" {
		t.Fatalf("data next_step = %v", data["next_step"])
	}
}

func TestProjectAddRequiresTwoArguments(t *testing.T) {
	code, _, _ := runProjectCLI(t, []string{"project", "add", "torio"}, &fakeProjectService{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

// --- list ---

func TestProjectListHuman(t *testing.T) {
	service := &fakeProjectService{listOut: []projects.Project{sampleProject()}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "list"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"torio", "Torio", claudecode.WorkspacePath + "/torio"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human list is missing %q: %q", want, stdout)
		}
	}
}

func TestProjectListEmptyReportsNextStep(t *testing.T) {
	code, stdout, stderr := runProjectCLI(t, []string{"project", "list"}, &fakeProjectService{})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "no projects registered") {
		t.Errorf("empty list must say so: %q", stdout)
	}
	if !strings.Contains(stdout, "next: torio project add") {
		t.Errorf("empty list must hand off to add: %q", stdout)
	}
}

func TestProjectListJSONEnvelope(t *testing.T) {
	service := &fakeProjectService{listOut: []projects.Project{sampleProject()}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "list", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["command"] != "project.list" {
		t.Fatalf("command = %v", env["command"])
	}
	data, _ := env["data"].(map[string]any)
	list, _ := data["projects"].([]any)
	if len(list) != 1 {
		t.Fatalf("projects = %v", data["projects"])
	}
	first, _ := list[0].(map[string]any)
	if first["id"] != "torio" || first["remote"] != "git@github.com:wzslr321/torio.git" {
		t.Fatalf("project entry = %+v", first)
	}
}

// --- show ---

func TestProjectShowJSONEnvelope(t *testing.T) {
	service := &fakeProjectService{showReport: projects.ShowReport{
		Project: sampleProject(),
		Checkout: projects.CheckoutStatus{
			PathExists: true, Directory: true, Repository: true, OriginMatches: true,
			FullClone: true, Clean: true, NoCredentialHelper: true, SharedPermissions: true,
			Owner: claudecode.User, Group: "torio-projects", Mode: "2770",
		},
		Issues: []string{},
	}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "show", "torio", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.showID != "torio" {
		t.Fatalf("show id = %q", service.showID)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["command"] != "project.show" {
		t.Fatalf("command = %v", env["command"])
	}
	data, _ := env["data"].(map[string]any)
	checkout, _ := data["checkout"].(map[string]any)
	if checkout["clean"] != true || checkout["origin_matches"] != true {
		t.Fatalf("checkout data = %+v", checkout)
	}
	if issues, _ := data["issues"].([]any); len(issues) != 0 {
		t.Fatalf("issues = %v", data["issues"])
	}
}

// TestProjectShowHumanReportsDriftAndItsNextStep locks the split `show` makes:
// drift a rerun of `add` reconciles (a missing checkout)
// hands off to `add`, and drift inside an existing working tree hands off to an
// operator session, because Torio will not reset or repoint a tree.
func TestProjectShowHumanReportsDriftAndItsNextStep(t *testing.T) {
	for _, tc := range []struct {
		name     string
		issues   []string
		wantNext string
	}{
		{"dirty worktree", []string{"worktree_dirty"}, "next: torio project enter torio"},
		// `show` resolved this project from the registry, so the remote is on
		// record and the one-argument form is the one that reconciles it. The
		// three-argument form would ask the operator to retype a remote Torio
		// already holds, and a mistyped one attaches a different repository.
		{"absent checkout", []string{"checkout_absent"}, "next: torio project add torio"},
		{"none", nil, "next: torio project enter torio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeProjectService{showReport: projects.ShowReport{
				Project:  sampleProject(),
				Checkout: projects.CheckoutStatus{PathExists: true, Directory: true, Repository: true},
				Issues:   tc.issues,
			}}
			code, stdout, stderr := runProjectCLI(t, []string{"project", "show", "torio"}, service)
			if code != int(ExitOK) {
				t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
			}
			for _, issue := range tc.issues {
				if !strings.Contains(stdout, issue) {
					t.Errorf("human show must name the issue %q: %q", issue, stdout)
				}
			}
			if !strings.Contains(stdout, tc.wantNext) {
				t.Errorf("next step = %q, want it to contain %q", stdout, tc.wantNext)
			}
		})
	}
}

// --- use ---

// --- remove ---

func TestProjectRemoveHumanStatesCheckoutIsRetained(t *testing.T) {
	service := &fakeProjectService{removeReport: projects.RemoveReport{
		Project:          sampleProject(),
		CheckoutRetained: true,
		CheckoutPath:     claudecode.WorkspacePath + "/torio",
		Notes:            []string{"checkout_retained"},
	}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "remove", "torio"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.removeID != "torio" {
		t.Fatalf("remove id = %q", service.removeID)
	}
	if !strings.Contains(stdout, "still exists") {
		t.Errorf("removal must state the checkout directory still exists: %q", stdout)
	}
	if !strings.Contains(stdout, claudecode.WorkspacePath+"/torio") {
		t.Errorf("removal must name the retained directory: %q", stdout)
	}
}

func TestProjectRemoveJSONEnvelope(t *testing.T) {
	service := &fakeProjectService{removeReport: projects.RemoveReport{
		Project:          sampleProject(),
		CheckoutRetained: true,
		CheckoutPath:     claudecode.WorkspacePath + "/torio",
		Notes:            []string{"checkout_retained"},
	}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "remove", "torio", "--json"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["command"] != "project.remove" {
		t.Fatalf("command = %v", env["command"])
	}
	data, _ := env["data"].(map[string]any)
	if data["checkout_retained"] != true || data["checkout_path"] != claudecode.WorkspacePath+"/torio" {
		t.Fatalf("data = %+v", data)
	}
}

// --- shell ---

// shellSession is a preflighted session for the sample project: every
// precondition proven, which is the only state the command opens a shell in.
func shellSession() projects.ShellSession {
	return projects.ShellSession{
		ShellSpec: projects.ShellSpec{
			Project:      sampleProject(),
			Group:        "torio-projects",
			Instance:     lima.InstanceName,
			OperatorUser: "testop",
		},
		Verified: []string{"vm_running", "checkout_present", "origin_matches", "operator_ssh_agent"},
	}
}

func enterSession() projects.EnterSession {
	return projects.EnterSession{
		EnterSpec: projects.EnterSpec{
			Project:      sampleProject(),
			Group:        "torio-projects",
			Instance:     lima.InstanceName,
			OperatorUser: "testop",
		},
		Verified: []string{"vm_running", "project_enter_helper", "checkout_present", "origin_matches"},
	}
}

func TestProjectEnterRejectsJSONBeforePreflight(t *testing.T) {
	service := &fakeProjectService{enterSession: enterSession()}
	runner := &fakeInteractiveRunner{}
	code, _, _ := runProjectCLI(t, []string{"project", "enter", "torio", "--json"}, service,
		func(a *app) { a.newInteractive = func() execx.InteractiveRunner { return runner } })
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if service.enterID != "" || len(runner.cmds) != 0 {
		t.Fatalf("rejected invocation preflighted %q or ran %+v", service.enterID, runner.cmds)
	}
}

func TestProjectEnterPreflightsThenRunsWithoutPushCapability(t *testing.T) {
	service := &fakeProjectService{enterSession: enterSession()}
	runner := &fakeInteractiveRunner{}
	want := execx.InteractiveCommand{Name: "ssh", Args: []string{"-a", "-t", "lima-torio", "enter-helper", claudecode.WorkspacePath + "/torio"}}
	var gotPath string

	code, stdout, stderr := runProjectCLI(t, []string{"project", "enter", "torio"}, service,
		func(a *app) {
			a.newInteractive = func() execx.InteractiveRunner { return runner }
			a.newEnterSpec = func(projectPath string) (execx.InteractiveCommand, error) {
				gotPath = projectPath
				return want, nil
			}
		})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.enterID != "torio" || gotPath != claudecode.WorkspacePath+"/torio" {
		t.Fatalf("preflight id = %q, enter spec path = %q", service.enterID, gotPath)
	}
	if len(runner.cmds) != 1 || !reflect.DeepEqual(runner.cmds[0], want) {
		t.Fatalf("interactive command = %+v, want exactly %+v", runner.cmds, want)
	}
	if !strings.Contains(stdout, "without SSH agent forwarding") || !strings.Contains(stdout, "session ended") {
		t.Errorf("ordinary session boundary is unclear: %q", stdout)
	}
}

// withShell wires both interactive seams: the runner that would spawn ssh, and
// the spec builder that reads host state a test must not depend on.
func withShell(runner execx.InteractiveRunner, cmd execx.InteractiveCommand) func(*app) {
	return func(a *app) {
		a.newInteractive = func() execx.InteractiveRunner { return runner }
		a.newShellSpec = func(string) (execx.InteractiveCommand, error) { return cmd, nil }
	}
}

func TestProjectShellRejectsJSON(t *testing.T) {
	service := &fakeProjectService{shellSession: shellSession()}
	runner := &fakeInteractiveRunner{}
	code, _, _ := runProjectCLI(t, []string{"project", "shell", "torio", "--json"}, service,
		func(a *app) { a.newInteractive = func() execx.InteractiveRunner { return runner } })
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if len(runner.cmds) != 0 {
		t.Fatalf("a rejected invocation must not open a session: %+v", runner.cmds)
	}
	if service.shellID != "" {
		t.Fatalf("a rejected invocation ran the preflight for %q", service.shellID)
	}
}

func TestProjectShellPreflightsThenRunsTheOperatorShellCommand(t *testing.T) {
	service := &fakeProjectService{
		shellSession: shellSession(),
	}
	runner := &fakeInteractiveRunner{}
	want := execx.InteractiveCommand{Name: "ssh", Args: []string{"-t", "lima-torio", "helper", claudecode.WorkspacePath + "/torio"}}
	var gotPath string
	code, stdout, stderr := runProjectCLI(t, []string{"project", "shell", "torio"}, service,
		func(a *app) {
			a.newInteractive = func() execx.InteractiveRunner { return runner }
			a.newShellSpec = func(projectPath string) (execx.InteractiveCommand, error) {
				gotPath = projectPath
				return want, nil
			}
		})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.shellID != "torio" {
		t.Fatalf("preflight id = %q", service.shellID)
	}
	if gotPath != claudecode.WorkspacePath+"/torio" {
		t.Fatalf("shell spec path = %q", gotPath)
	}
	if len(runner.cmds) != 1 || !reflect.DeepEqual(runner.cmds[0], want) {
		t.Fatalf("interactive command = %+v, want exactly %+v", runner.cmds, want)
	}
	// The operator is told, before the session opens, that the capability they
	// are about to hold is bounded by the session.
	if !strings.Contains(stdout, claudecode.WorkspacePath+"/torio") || !strings.Contains(stdout, "exit") {
		t.Errorf("the opening line does not state where the session lands and when the capability ends: %q", stdout)
	}
	if !strings.Contains(stdout, "session ended") {
		t.Errorf("the end of the session is not reported: %q", stdout)
	}
}

// Torio never sees the remote side of the session, so it must not describe it.
// The one thing an operator could reasonably misread as a Torio guarantee is a
// push, and the output says the opposite in as many words.
func TestProjectShellNeverClaimsAPushHappened(t *testing.T) {
	service := &fakeProjectService{shellSession: shellSession()}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "shell", "torio"}, service,
		withShell(&fakeInteractiveRunner{}, execx.InteractiveCommand{Name: "ssh"}))
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	out := stdout + stderr
	for _, forbidden := range []string{"push succeeded", "pushed successfully", "push complete", "changes are on"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("output claims a push: %q contains %q", out, forbidden)
		}
	}
	if !strings.Contains(stdout, "no claim about") {
		t.Errorf("output does not disclaim the push: %q", stdout)
	}
	// A zero-value check is a check that did not run, and says so.
}

func TestProjectShellChildExitIsExternal(t *testing.T) {
	service := &fakeProjectService{shellSession: shellSession()}
	runner := &fakeInteractiveRunner{err: &execx.ExitError{Code: 3}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "shell", "torio"}, service,
		withShell(runner, execx.InteractiveCommand{Name: "ssh"}))
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitExternal, stderr)
	}
	if !strings.Contains(stderr, "3") {
		t.Errorf("the child exit code is not reported: %q", stderr)
	}
	// A session that ended badly still forwarded an agent, so the end of the
	// session is still reported.
	if !strings.Contains(stdout, "session ended") {
		t.Errorf("a failed session skipped the post-session report: %q", stdout)
	}
}

// Every preflight failure the manager can report reaches the operator as the
// contract exit code for its kind, and none of them opens a session.
func TestProjectShellPreflightFailuresMapToExitCodesAndOpenNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind projects.ErrorKind
		want ExitCode
	}{
		{"unknown project", projects.KindConflict, ExitConflict},
		{"vm not running", projects.KindPrecondition, ExitPrecondition},
		{"no agent socket", projects.KindPrecondition, ExitPrecondition},
		{"agent holds no identity", projects.KindAuth, ExitPermission},
		{"origin drift", projects.KindVerification, ExitVerification},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeProjectService{shellErr: &projects.Error{
				Op: "shell", Kind: tc.kind, Err: errors.New(tc.name),
			}}
			runner := &fakeInteractiveRunner{}
			code, stdout, _ := runProjectCLI(t, []string{"project", "shell", "torio"}, service,
				withShell(runner, execx.InteractiveCommand{Name: "ssh"}))
			if code != int(tc.want) {
				t.Fatalf("exit = %d, want %d", code, tc.want)
			}
			if len(runner.cmds) != 0 {
				t.Fatalf("a failed preflight opened a session: %+v", runner.cmds)
			}
			if stdout != "" {
				t.Fatalf("a failed preflight reported a session: %q", stdout)
			}
		})
	}
}

// The interactive command carries the operator's forwarded agent socket and
// whatever else their environment holds. None of it is Torio's to print.
func TestProjectShellNeverEchoesTheSessionCommandOrEnvironment(t *testing.T) {
	service := &fakeProjectService{shellSession: shellSession()}
	cmd := execx.InteractiveCommand{
		Name: "ssh",
		Args: []string{"-A", "-o", "IdentityFile=" + knownShapeCanary},
		Env:  []string{"SSH_AUTH_SOCK=/tmp/" + knownShapeCanary},
	}
	for _, runner := range []*fakeInteractiveRunner{
		{},
		{err: &execx.ExitError{Code: 130}},
	} {
		_, stdout, stderr := runProjectCLI(t, []string{"project", "shell", "torio"}, service,
			withShell(runner, cmd))
		if strings.Contains(stdout+stderr, knownShapeCanary) {
			t.Errorf("output leaked the session command or environment: %q %q", stdout, stderr)
		}
	}
}

// --- error mapping ---

func TestProjectErrorKindsMapToContractExitCodes(t *testing.T) {
	for _, tc := range []struct {
		kind projects.ErrorKind
		want ExitCode
	}{
		{projects.KindInvalidConfig, ExitUsage},
		{projects.KindPrecondition, ExitPrecondition},
		{projects.KindAuth, ExitPermission},
		{projects.KindConflict, ExitConflict},
		{projects.KindVerification, ExitVerification},
		{projects.KindConfigWrite, ExitReconcile},
		{projects.KindGuestCommand, ExitExternal},
		{projects.KindGit, ExitExternal},
		{projects.KindRegistration, ExitExternal},
		{projects.KindTransport, ExitExternal},
		{projects.KindTimeout, ExitExternal},
		{projects.KindCancelled, ExitExternal},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			service := &fakeProjectService{showErr: &projects.Error{Op: "show", Kind: tc.kind}}
			code, stdout, _ := runProjectCLI(t, []string{"project", "show", "torio", "--json"}, service)
			if code != int(tc.want) {
				t.Fatalf("exit = %d, want %d", code, tc.want)
			}
			env := decodeOneEnvelope(t, stdout)
			if env["ok"] != false || env["command"] != "project.show" {
				t.Fatalf("error envelope ok/command = %v/%v", env["ok"], env["command"])
			}
			errObj, _ := env["error"].(map[string]any)
			if errObj["code"] != strings.ToUpper(string(tc.kind)) {
				t.Fatalf("error code = %v, want %v", errObj["code"], strings.ToUpper(string(tc.kind)))
			}
		})
	}
}

func TestProjectUnknownIDIsConflict(t *testing.T) {
	service := &fakeProjectService{showErr: &projects.Error{
		Op: "show", Kind: projects.KindConflict, Err: errors.New("project id is not registered: \"ghost\""),
	}}
	code, _, stderr := runProjectCLI(t, []string{"project", "show", "ghost"}, service)
	if code != int(ExitConflict) {
		t.Fatalf("exit = %d, want %d", code, ExitConflict)
	}
	if !strings.Contains(stderr, "not registered") {
		t.Errorf("stderr does not explain the unknown id: %q", stderr)
	}
}

func TestProjectNonProjectErrorIsInternal(t *testing.T) {
	service := &fakeProjectService{listErr: errors.New("boom")}
	code, _, _ := runProjectCLI(t, []string{"project", "list"}, service)
	if code != int(ExitInternal) {
		t.Fatalf("exit = %d, want %d", code, ExitInternal)
	}
}

// decodeProjectEnvelope parses the single envelope a --json run writes.
func decodeProjectEnvelope(t *testing.T, stdout string) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one envelope (%v): %q", err, stdout)
	}
	return env
}

// testDeployKey is the report a manager returns when it has provisioned a key
// the forge has not been told about yet.
func testDeployKey() *projects.DeployKey {
	return &projects.DeployKey{
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFake torio-deploy-demo",
		Host:      "github.com",
		KeyPath:   claudecode.Home + "/.ssh/torio/demo",
		Generated: true,
	}
}

// The public key is the reason an unreadable private remote is actionable, so a
// human run has to be able to see it and copy it. It belongs on stderr with the
// diagnostic, because stdout stays free of mixed content on a failing command.
func TestProjectAddPrintsTheDeployKeyOnStderr(t *testing.T) {
	key := testDeployKey()
	service := &fakeProjectService{
		addReport: projects.AddReport{DeployKey: key, Notes: []string{"deploy_key_generated"}},
		addErr: &projects.Error{
			Op:   "add",
			Kind: projects.KindAuth,
			Err:  errors.New("the guest cannot read the remote yet; add its public key to the repository on github.com as a deploy key with write access off, not as an account key, then run the same command again"),
		},
	}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "add", "demo", "git@github.com:owner/demo.git"}, service)

	if code != int(ExitPermission) {
		t.Fatalf("exit = %d, want %d", code, ExitPermission)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want it empty on a failing command", stdout)
	}
	for _, want := range []string{key.PublicKey, key.Host, key.KeyPath, "run the same command again"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	// The key has to survive as one selectable line; prose wrapped around it
	// would make copying it a manual edit.
	if !strings.Contains(stderr, "\n"+key.PublicKey+"\n") {
		t.Errorf("stderr = %q, want the public key alone on its line", stderr)
	}
}

// JSON mode carries the same facts in the error envelope. Nothing may be
// printed beside the envelope, because that is the whole contract of --json.
func TestProjectAddCarriesTheDeployKeyInTheErrorEnvelope(t *testing.T) {
	key := testDeployKey()
	service := &fakeProjectService{
		addReport: projects.AddReport{DeployKey: key, Notes: []string{"deploy_key_generated"}},
		addErr: &projects.Error{
			Op:   "add",
			Kind: projects.KindAuth,
			Err:  errors.New("the guest cannot read the remote yet"),
		},
	}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "add", "demo", "git@github.com:owner/demo.git", "--json"}, service)

	if code != int(ExitPermission) {
		t.Fatalf("exit = %d, want %d", code, ExitPermission)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want --json to write nothing beside the envelope", stderr)
	}
	env := decodeProjectEnvelope(t, stdout)
	if env.OK {
		t.Fatalf("envelope ok = true, want a failure: %s", stdout)
	}
	// The machine-readable half of the exit-7 contract. A caller branches on the
	// code, so it is pinned beside the exit status rather than derived from it.
	if env.Error.Code != "AUTH" {
		t.Errorf("error code = %q, want AUTH", env.Error.Code)
	}
	details, _ := env.Error.Details["deploy_key"].(map[string]any)
	if details == nil {
		t.Fatalf("error details = %#v, want a deploy_key object", env.Error.Details)
	}
	for field, want := range map[string]any{
		"public_key": key.PublicKey,
		"host":       key.Host,
		"key_path":   key.KeyPath,
		"generated":  true,
	} {
		if details[field] != want {
			t.Errorf("deploy_key.%s = %#v, want %#v", field, details[field], want)
		}
	}
	if notes, _ := env.Error.Details["notes"].(string); !strings.Contains(notes, "deploy_key_generated") {
		t.Errorf("notes = %q, want deploy_key_generated", notes)
	}
}

// Redaction is the last thing to touch an error path, and a public key looks
// enough like opaque material to be worth pinning: it must survive intact while
// a real secret shape beside it does not.
func TestProjectAddDeployKeySurvivesRedaction(t *testing.T) {
	key := testDeployKey()
	service := &fakeProjectService{
		addReport: projects.AddReport{DeployKey: key},
		addErr: &projects.Error{
			Op:   "add",
			Kind: projects.KindAuth,
			Err:  errors.New("the guest cannot read the remote yet, and " + knownShapeCanary + " must not survive this"),
		},
	}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "add", "demo", "git@github.com:owner/demo.git"}, service)

	if code != int(ExitPermission) {
		t.Fatalf("exit = %d, want %d", code, ExitPermission)
	}
	if !strings.Contains(stderr, key.PublicKey) {
		t.Errorf("redaction ate the public key: %q", stderr)
	}
	if strings.Contains(stdout+stderr, knownShapeCanary) {
		t.Errorf("output leaked a credential shape: %q %q", stdout, stderr)
	}
}

// TestProjectAddNeverEchoesACredentialShapedRemote proves the command layer does
// not print the operator's argv back when the manager refuses it: a remote that
// carries credential-shaped material must not reach stdout or stderr, in either
// output mode.
func TestProjectAddNeverEchoesACredentialShapedRemote(t *testing.T) {
	remote := "https://" + knownShapeCanary + "@github.com/o/r.git"
	for _, args := range [][]string{
		{"project", "add", "torio", remote},
		{"project", "add", "torio", remote, "--json"},
	} {
		service := &fakeProjectService{addErr: &projects.Error{
			Op:   "add",
			Kind: projects.KindInvalidConfig,
			Err:  errors.New("project \"torio\" remote: contains secret-shaped material; config must be non-secret"),
		}}
		code, stdout, stderr := runProjectCLI(t, args, service)
		if code != int(ExitUsage) {
			t.Fatalf("%v: exit = %d, want %d", args, code, ExitUsage)
		}
		if strings.Contains(stdout+stderr, knownShapeCanary) {
			t.Errorf("%v: output leaked the credential-shaped remote: %q %q", args, stdout, stderr)
		}
	}
}

// Correcting a remote is its own command because removing and re-adding is not
// a correction: it drops the record first and then stops on the checkouts other
// guests hold (ADR-0023).
func TestProjectSetRemoteCorrectsTheRecord(t *testing.T) {
	service := &fakeProjectService{
		setRemoteReport: projects.SetRemoteReport{
			Project:           sampleProject(),
			PreviousRemote:    "git@gh-torio:wzslr321/torio.git",
			CheckoutRepointed: true,
			Notes:             []string{"checkout_repointed"},
		},
	}

	code, stdout, stderr := runProjectCLI(t, []string{
		"project", "set-remote", "torio", "git@github.com:wzslr321/torio.git",
	}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.setRemoteID != "torio" {
		t.Errorf("corrected %q, want the id given", service.setRemoteID)
	}
	if got, want := service.setRemoteURL, "git@github.com:wzslr321/torio.git"; got != want {
		t.Errorf("corrected to %q, want %q", got, want)
	}
	if !strings.Contains(stdout, "git@github.com:wzslr321/torio.git") {
		t.Errorf("stdout = %q, want it to name the corrected remote", stdout)
	}
}

func TestProjectSetRemoteEmitsOneEnvelope(t *testing.T) {
	service := &fakeProjectService{
		setRemoteReport: projects.SetRemoteReport{
			Project:        sampleProject(),
			PreviousRemote: "git@gh-torio:wzslr321/torio.git",
			Notes:          []string{"checkout_absent"},
		},
	}

	code, stdout, _ := runProjectCLI(t, []string{
		"--json", "project", "set-remote", "torio", "git@github.com:wzslr321/torio.git",
	}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Remote            string   `json:"remote"`
			PreviousRemote    string   `json:"previous_remote"`
			CheckoutRepointed bool     `json:"checkout_repointed"`
			Notes             []string `json:"notes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v; got %q", err, stdout)
	}
	if !env.OK {
		t.Errorf("envelope ok = false, want true")
	}
	if got, want := env.Data.PreviousRemote, "git@gh-torio:wzslr321/torio.git"; got != want {
		t.Errorf("previous_remote = %q, want %q", got, want)
	}
	if env.Data.CheckoutRepointed {
		t.Error("checkout_repointed = true, want false when no checkout was there")
	}
}

func TestProjectSetRemoteRequiresBothArguments(t *testing.T) {
	for _, args := range [][]string{
		{"project", "set-remote"},
		{"project", "set-remote", "torio"},
	} {
		code, _, _ := runProjectCLI(t, args, &fakeProjectService{})
		if code != int(ExitUsage) {
			t.Errorf("%v: exit = %d, want %d", args, code, ExitUsage)
		}
	}
}

// The registry is shared and the checkouts are not, so switching backend and
// opening a project finds nothing there. Materializing it is the step that was
// missing, and it is the same one-argument `add` the operator would have run:
// the remote comes from the record, so nothing is retyped and nothing new is
// attached (ADR-0024).
func TestProjectAgentMaterializesAnAbsentCheckoutThenOpens(t *testing.T) {
	service := &fakeProjectService{
		enterSession: enterSession(),
		enterErrOnce: &projects.Error{
			Op:     "enter",
			Kind:   projects.KindVerification,
			Issues: []string{"checkout_absent"},
			Err:    errors.New("the checkout for \"torio\" is not in a state a session can be opened in (checkout_absent)"),
		},
		listOut: []projects.Project{sampleProject()},
	}
	runner := &fakeInteractiveRunner{}

	code, stdout, stderr := runProjectCLI(t, []string{"--backend", "claude-code", "project", "agent", "torio"}, service,
		func(a *app) {
			a.newInteractive = func() execx.InteractiveRunner { return runner }
			a.newAgentSpec = func(string) (execx.InteractiveCommand, error) {
				return execx.InteractiveCommand{Name: "ssh"}, nil
			}
		})

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", code, stderr, stdout)
	}
	if got, want := service.addReq.ID, "torio"; got != want {
		t.Errorf("materialized %q, want %q", got, want)
	}
	if got, want := service.addReq.Remote, sampleProject().Remote; got != want {
		t.Errorf("materialized with remote %q, want the one on record %q", got, want)
	}
	if service.enterCalls != 2 {
		t.Errorf("preflight ran %d times, want it re-run after the checkout was made", service.enterCalls)
	}
	if len(runner.cmds) != 1 {
		t.Errorf("interactive sessions opened = %d, want 1", len(runner.cmds))
	}
	if !strings.Contains(stdout, "materializing") {
		t.Errorf("stdout = %q, want it to say the checkout is being made", stdout)
	}
}

// Any other drift is a working tree, and Torio does not clone over one. The
// session refuses exactly as it did before.
func TestProjectAgentDoesNotMaterializeOverDriftItMustNotTouch(t *testing.T) {
	service := &fakeProjectService{
		enterErr: &projects.Error{
			Op:     "enter",
			Kind:   projects.KindVerification,
			Issues: []string{"origin_mismatch", "worktree_dirty"},
			Err:    errors.New("the checkout for \"torio\" is not in a state a session can be opened in"),
		},
	}
	runner := &fakeInteractiveRunner{}

	code, _, _ := runProjectCLI(t, []string{"--backend", "claude-code", "project", "agent", "torio"}, service,
		func(a *app) { a.newInteractive = func() execx.InteractiveRunner { return runner } })

	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d", code, ExitVerification)
	}
	if service.addReq.ID != "" {
		t.Error("a checkout Torio must not touch was cloned over")
	}
	if len(runner.cmds) != 0 {
		t.Error("a session was opened on a drifted checkout")
	}
}

// Materializing reaches a remote, so it can fail closed on an authorization the
// operator still has to give. The deploy key is what makes that actionable, and
// it is printed here exactly as `project add` prints it.
func TestProjectAgentReportsTheDeployKeyWhenMaterializingFails(t *testing.T) {
	service := &fakeProjectService{
		enterErrOnce: &projects.Error{
			Op:     "enter",
			Kind:   projects.KindVerification,
			Issues: []string{"checkout_absent"},
			Err:    errors.New("checkout_absent"),
		},
		listOut: []projects.Project{sampleProject()},
		addErr:  &projects.Error{Op: "add", Kind: projects.KindAuth, Err: errors.New("the guest cannot read the remote yet")},
		addReport: projects.AddReport{DeployKey: &projects.DeployKey{
			PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample torio-deploy-torio",
			Host:      "github.com",
		}},
	}
	runner := &fakeInteractiveRunner{}

	code, _, stderr := runProjectCLI(t, []string{"--backend", "claude-code", "project", "agent", "torio"}, service,
		func(a *app) { a.newInteractive = func() execx.InteractiveRunner { return runner } })

	if code != int(ExitPermission) {
		t.Fatalf("exit = %d, want %d", code, ExitPermission)
	}
	// The key goes to stderr, where `project add` puts it: stdout carries the
	// envelope, and a human block on it would corrupt one.
	if !strings.Contains(stderr, "ssh-ed25519") {
		t.Errorf("stderr = %q, want the deploy key to authorize", stderr)
	}
	if len(runner.cmds) != 0 {
		t.Error("a session was opened after materializing failed")
	}
}

// Cloning is minutes of work and the operator did not choose to start it: they
// asked to open a project. Bounding the clone by the ordinary per-operation
// timeout makes the first open of any sizeable repository fail on the clock,
// so the materialization gets the policy maximum instead (ADR-0024).
func TestMaterializingGetsTheLongBoundRatherThanTheOperationTimeout(t *testing.T) {
	var addDeadline time.Time
	service := &fakeProjectService{
		enterSession: enterSession(),
		enterErrOnce: &projects.Error{
			Op:     "enter",
			Kind:   projects.KindVerification,
			Issues: []string{"checkout_absent"},
			Err:    errors.New("checkout_absent"),
		},
		listOut: []projects.Project{sampleProject()},
		onAdd: func(ctx context.Context) {
			addDeadline, _ = ctx.Deadline()
		},
	}
	runner := &fakeInteractiveRunner{}

	code, _, stderr := runProjectCLI(t,
		[]string{"--timeout", "5s", "--backend", "claude-code", "project", "agent", "torio"}, service,
		func(a *app) {
			a.newInteractive = func() execx.InteractiveRunner { return runner }
			a.newAgentSpec = func(string) (execx.InteractiveCommand, error) {
				return execx.InteractiveCommand{Name: "ssh"}, nil
			}
		})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if addDeadline.IsZero() {
		t.Fatal("the materialization ran with no deadline at all")
	}
	// Comfortably past the 5s the invocation asked for, and no further than the
	// policy maximum allows.
	if remaining := time.Until(addDeadline); remaining <= time.Minute {
		t.Errorf("materialization deadline is %s away, want the long bound rather than the 5s operation timeout", remaining)
	}
}

// `--local` is the explicit decision to make a project that has no remote
// (ADR-0027). It reads nothing off the registry: an id that is not on record is
// the ordinary case here, and treating an absent record as an error would make
// the flag unusable for the only thing it is for.
func TestProjectAddLocalRequestsAProjectWithNoRemote(t *testing.T) {
	service := &fakeProjectService{addReport: projects.AddReport{
		Project:     projects.Project{ID: "notes", DisplayName: "notes", Path: claudecode.WorkspacePath + "/notes"},
		Initialized: true,
		Registered:  true,
	}}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "add", "notes", "--local"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	want := projects.AddRequest{ID: "notes", DisplayName: "notes", Local: true}
	if !reflect.DeepEqual(service.addReq, want) {
		t.Fatalf("add request = %+v, want %+v", service.addReq, want)
	}
	if !strings.Contains(stdout, "initialized") {
		t.Errorf("output does not report what was made: %q", stdout)
	}
	if !strings.Contains(stdout, "(none — local project)") {
		t.Errorf("output does not say the project has no remote: %q", stdout)
	}
}

// A bundle attaches a repository that is not on record yet — that is how one
// arrives — so an unknown id is not the usage error it is for a bare add.
func TestProjectAddFromBundleCarriesTheHostPath(t *testing.T) {
	service := &fakeProjectService{
		listErr: errors.New("registry unreadable"),
		addReport: projects.AddReport{
			Project:    projects.Project{ID: "marketing", DisplayName: "marketing"},
			Cloned:     true,
			Registered: true,
		},
	}
	code, _, stderr := runProjectCLI(t,
		[]string{"project", "add", "marketing", "--from-bundle", "/tmp/marketing.bundle"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	want := projects.AddRequest{ID: "marketing", DisplayName: "marketing", BundlePath: "/tmp/marketing.bundle"}
	if !reflect.DeepEqual(service.addReq, want) {
		t.Fatalf("add request = %+v, want %+v", service.addReq, want)
	}
}

// A project with a remote on record is cloned from the remote. Attaching a
// bundle over it would put a second copy of the same repository under one id.
func TestProjectAddFromBundleRefusesAProjectThatHasARemote(t *testing.T) {
	service := &fakeProjectService{listOut: []projects.Project{
		{ID: "torio", DisplayName: "torio", Remote: "git@github.com:wzslr321/torio.git"},
	}}
	code, _, stderr := runProjectCLI(t,
		[]string{"project", "add", "torio", "--from-bundle", "/tmp/torio.bundle"}, service)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want usage; stderr=%q", code, stderr)
	}
	if service.addReq.ID != "" {
		t.Errorf("a refused invocation still reached the manager: %+v", service.addReq)
	}
}

// Promoting a local project stops on the one act a human has to perform, and
// the key that makes it actionable is printed where `add` prints it.
func TestProjectSetRemotePrintsTheDeployKeyOnStderr(t *testing.T) {
	service := &fakeProjectService{
		setRemoteReport: projects.SetRemoteReport{
			Project:   projects.Project{ID: "notes", Remote: "git@github.com:you/notes.git"},
			DeployKey: &projects.DeployKey{PublicKey: "ssh-ed25519 AAAAC3Fake torio-deploy-notes", Host: "github.com", Generated: true},
		},
		setRemoteErr: &projects.Error{Op: "set_remote", Kind: projects.KindAuth, Err: errors.New("the guest cannot read the remote yet")},
	}
	code, stdout, stderr := runProjectCLI(t,
		[]string{"project", "set-remote", "notes", "git@github.com:you/notes.git"}, service)
	if code != int(ExitPermission) {
		t.Fatalf("exit = %d, want %d", code, int(ExitPermission))
	}
	if !strings.Contains(stderr, "ssh-ed25519 AAAAC3Fake torio-deploy-notes") {
		t.Errorf("the key an operator must authorize is not on stderr: %q", stderr)
	}
	if strings.Contains(stdout, "ssh-ed25519") {
		t.Errorf("the key reached stdout, where a JSON envelope lives: %q", stdout)
	}
}

// A promotion that finished says what it did: the record gained a remote it
// never had, rather than having one corrected.
func TestProjectSetRemoteReportsAPromotionAsAnAttachment(t *testing.T) {
	service := &fakeProjectService{setRemoteReport: projects.SetRemoteReport{
		Project:           projects.Project{ID: "notes", Remote: "git@github.com:you/notes.git"},
		PreviousRemote:    "",
		CheckoutRepointed: true,
		Notes:             []string{"remote_attached"},
	}}
	code, stdout, stderr := runProjectCLI(t,
		[]string{"project", "set-remote", "notes", "git@github.com:you/notes.git"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "remote attached") {
		t.Errorf("output does not distinguish an attachment from a correction: %q", stdout)
	}
	if !strings.Contains(stdout, "(none — the project was local)") {
		t.Errorf("output does not say what the record held before: %q", stdout)
	}
}

// Opening a local project on a guest that does not hold it cannot be answered
// by materializing: there is no remote to clone from. The refusal is the
// manager's, and the announcement of a clone that is not going to happen must
// not precede it.
func TestProjectEnterOnALocalProjectSaysWhyItCannotMaterialize(t *testing.T) {
	service := &fakeProjectService{
		listOut: []projects.Project{{ID: "notes", DisplayName: "notes"}},
		enterErr: &projects.Error{
			Op: "enter", Kind: projects.KindVerification,
			Err:    errors.New("checkout_absent"),
			Issues: []string{"checkout_absent"},
		},
		addErr: &projects.Error{
			Op: "add", Kind: projects.KindPrecondition,
			Err: errors.New("project \"notes\" is local: it has no remote on record, so there is nothing to clone it from here. " +
				"Carry it in with `torio project add notes --from-bundle <file>`, or give it a remote with " +
				"`torio project set-remote notes <remote>` so every guest can reach it"),
		},
	}
	code, stdout, stderr := runProjectCLI(t, []string{"project", "enter", "notes"}, service)
	if code == int(ExitOK) {
		t.Fatalf("a local project opened on a guest that does not hold it; stdout=%q", stdout)
	}
	if strings.Contains(stdout, "materializing it from the remote on record") {
		t.Errorf("a clone that cannot happen was announced: %q", stdout)
	}
	for _, want := range []string{"--from-bundle", "set-remote"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to name %q", stderr, want)
		}
	}
}

// A reconciliation says what moved in each direction and where the host
// repository is, because that directory is where an operator resolves anything
// it refused to touch.
func TestProjectSyncReportsWhatMovedEachWay(t *testing.T) {
	service := &fakeProjectService{
		syncReport: projects.SyncReport{
			Project:  projects.Project{ID: "prezka", DisplayName: "Prezka", Path: claudecode.WorkspacePath + "/prezka"},
			HubPath:  "/Users/op/.local/share/torio/projects/prezka.git",
			ToHub:    []projects.RefMove{{Ref: "heads/main", Commits: 3}},
			ToGuest:  []projects.RefMove{{Ref: "heads/topic", Commits: 1}},
			Diverged: []string{"heads/spike"},
		},
	}

	code, stdout, stderr := runProjectCLI(t, []string{"project", "sync", "prezka"}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.syncID != "prezka" {
		t.Errorf("reconciled %q, want the id given", service.syncID)
	}
	for _, want := range []string{"heads/main", "heads/topic", "heads/spike", "prezka.git"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to name %q", stdout, want)
		}
	}
}

// A ref left alone has to be answerable, and the answer is Git in the host
// repository. The next step names it rather than describing it.
func TestProjectSyncPointsAtTheHostRepositoryWhenARefDiverged(t *testing.T) {
	service := &fakeProjectService{
		syncReport: projects.SyncReport{
			Project:  projects.Project{ID: "prezka"},
			HubPath:  "/Users/op/.local/share/torio/projects/prezka.git",
			Diverged: []string{"heads/main"},
		},
	}

	_, stdout, _ := runProjectCLI(t, []string{"project", "sync", "prezka"}, service)

	if !strings.Contains(stdout, "git -C /Users/op/.local/share/torio/projects/prezka.git") {
		t.Errorf("stdout = %q, want the command that resolves a divergence", stdout)
	}
}

func TestProjectSyncEmitsOneEnvelope(t *testing.T) {
	service := &fakeProjectService{
		syncReport: projects.SyncReport{
			Project:    projects.Project{ID: "prezka"},
			HubPath:    "/Users/op/.local/share/torio/projects/prezka.git",
			HubCreated: true,
			ToHub:      []projects.RefMove{{Ref: "heads/main", Commits: 3}},
			HeldBack:   []string{"heads/main"},
			Notes:      []string{"no_history_yet"},
		},
	}

	code, stdout, _ := runProjectCLI(t, []string{"--json", "project", "sync", "prezka"}, service)

	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			HubPath    string `json:"hub_path"`
			HubCreated bool   `json:"hub_created"`
			ToHub      []struct {
				Ref     string `json:"ref"`
				Commits int    `json:"commits"`
			} `json:"carried_to_host"`
			HeldBack []string `json:"held_back"`
			Notes    []string `json:"notes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v; got %q", err, stdout)
	}
	if !env.OK || !env.Data.HubCreated {
		t.Errorf("envelope = %#v, want a successful creation reported", env)
	}
	if len(env.Data.ToHub) != 1 || env.Data.ToHub[0].Ref != "heads/main" || env.Data.ToHub[0].Commits != 3 {
		t.Errorf("carried_to_host = %#v, want heads/main with 3 commits", env.Data.ToHub)
	}
	if len(env.Data.HeldBack) != 1 {
		t.Errorf("held_back = %v, want the ref that was not written", env.Data.HeldBack)
	}
}

func TestProjectSyncRequiresAnID(t *testing.T) {
	code, _, _ := runProjectCLI(t, []string{"project", "sync"}, &fakeProjectService{})
	if code != int(ExitUsage) {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
}

// A ref the tree would not take is the headline, not a footnote under one
// saying nothing moved. Observed on a real box, where a held-back branch was
// reported under "already level with the host".
func TestProjectSyncDoesNotCallAHeldBackRefLevel(t *testing.T) {
	service := &fakeProjectService{
		syncReport: projects.SyncReport{
			Project:  projects.Project{ID: "prezka"},
			HubPath:  "/Users/op/.local/share/torio/projects/prezka.git",
			HeldBack: []string{"heads/main"},
		},
	}

	_, stdout, _ := runProjectCLI(t, []string{"project", "sync", "prezka"}, service)

	if strings.Contains(stdout, "already level with the host") {
		t.Errorf("stdout = %q, want the held-back ref to decide the headline", stdout)
	}
	if !strings.Contains(stdout, "next: torio project enter prezka") {
		t.Errorf("stdout = %q, want the session that settles it named", stdout)
	}
}
