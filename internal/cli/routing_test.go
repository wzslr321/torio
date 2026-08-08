package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
)

// routingHome builds an XDG config home and returns it. Instance documents are
// written into it by the tests that need one; the point of most of these is
// that a derived instance works before any document exists.
func routingHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "torio"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", home)
	return home
}

func writeInstanceDoc(t *testing.T, home, instance, body string) {
	t.Helper()
	dir := filepath.Join(home, "torio")
	if instance != "" {
		dir = filepath.Join(dir, "instances", instance)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// runRouted dispatches args and reports the instance and backend the invocation
// settled on. It captures them from the project wiring because that is the seam
// every routed value has to reach: an instance the adapter never sees and a
// backend the manager never gets are not routing, they are a variable.
func runRouted(t *testing.T, args []string, service projectService) (code int, instance string, backendName string, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	a := &app{
		stdout:             &out,
		stderr:             &errBuf,
		build:              testBuild(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newProjects: func(_ *lima.Adapter, opts lima.BootstrapOptions) projectService {
			instance = lima.InstanceName
			if opts.Backend != nil {
				backendName = opts.Backend.Identity().Name
			}
			return service
		},
	}
	code = runWithApp(context.Background(), a, args)
	return code, instance, backendName, errBuf.String()
}

// TestBackendFlagRoutesToItsOwnInstance is the change in one assertion. The
// operator names the agent; the box that runs it follows, with no document to
// write first and no environment variable to remember.
func TestBackendFlagRoutesToItsOwnInstance(t *testing.T) {
	routingHome(t)
	code, instance, backendName, stderr := runRouted(t,
		[]string{"project", "list", "--json", "--backend", "claude-code"},
		&fakeProjectService{listOut: []projects.Project{sampleProject()}})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if want := config.InstancePrefix + "claude-code"; instance != want {
		t.Errorf("instance = %q, want %q", instance, want)
	}
	if backendName != "claude-code" {
		t.Errorf("backend = %q, want claude-code", backendName)
	}
}

// The default backend keeps the instance an existing box already has. Deriving
// a new name for it would strand every VM created before this flag existed.
func TestNoBackendFlagKeepsTheDefaultInstance(t *testing.T) {
	routingHome(t)
	code, instance, backendName, stderr := runRouted(t,
		[]string{"project", "list", "--json"},
		&fakeProjectService{listOut: []projects.Project{sampleProject()}})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if instance != config.DefaultInstance {
		t.Errorf("instance = %q, want %q", instance, config.DefaultInstance)
	}
	if backendName != backend.DefaultName {
		t.Errorf("backend = %q, want %q", backendName, backend.DefaultName)
	}
}

// --backend hermes resolves to the same box as no flag at all, so the two
// spellings of "my daily instance" cannot drift into two instances.
func TestTheDefaultBackendNamedExplicitlyIsTheSameInstance(t *testing.T) {
	routingHome(t)
	_, instance, _, _ := runRouted(t,
		[]string{"project", "list", "--json", "--backend", backend.DefaultName},
		&fakeProjectService{listOut: []projects.Project{sampleProject()}})
	if instance != config.DefaultInstance {
		t.Errorf("instance = %q, want %q", instance, config.DefaultInstance)
	}
}

// TestTheEnvironmentStillNamesTheBoxDirectly pins that TORIO_INSTANCE keeps
// working for a box whose name Torio did not derive — a test VM, or a second
// box running the same backend. The backend then comes from what that instance
// declares, exactly as before.
func TestTheEnvironmentStillNamesTheBoxDirectly(t *testing.T) {
	home := routingHome(t)
	writeInstanceDoc(t, home, "torio-ci-claude", `{"schema_version":"3","backend":"claude-code","projects":[]}`)
	t.Setenv(config.InstanceEnvKey, "torio-ci-claude")

	code, instance, backendName, stderr := runRouted(t,
		[]string{"project", "list", "--json"},
		&fakeProjectService{listOut: []projects.Project{sampleProject()}})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if instance != "torio-ci-claude" {
		t.Errorf("instance = %q, want the one the environment named", instance)
	}
	if backendName != "claude-code" {
		t.Errorf("backend = %q, want claude-code", backendName)
	}
}

// TestAFlagCannotRedirectANamedInstance is the fail-closed case. TORIO_INSTANCE
// points at a provisioned guest; a --backend naming a different agent would
// have every later command drive that guest as an identity it was not built
// for. Silently letting the environment win would be just as wrong — the
// operator asked two questions with different answers and has to be told.
func TestAFlagCannotRedirectANamedInstance(t *testing.T) {
	home := routingHome(t)
	writeInstanceDoc(t, home, "", `{"schema_version":"3","backend":"hermes","projects":[]}`)
	t.Setenv(config.InstanceEnvKey, config.DefaultInstance)

	code, _, _, stderr := runRouted(t,
		[]string{"project", "list", "--json", "--backend", "claude-code"},
		&fakeProjectService{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr)
	}
}

// A legacy document declares no backend, which means the default one. Comparing
// it as "unset" rather than as the default would read --backend claude-code
// against the Hermes box as a match and drive it as the wrong agent.
func TestAnAbsentDeclarationIsComparedAsTheDefaultBackend(t *testing.T) {
	home := routingHome(t)
	writeInstanceDoc(t, home, "", `{"schema_version":"2","projects":[]}`)
	t.Setenv(config.InstanceEnvKey, config.DefaultInstance)

	code, _, _, stderr := runRouted(t,
		[]string{"project", "list", "--json", "--backend", "claude-code"},
		&fakeProjectService{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr)
	}
}

// The other half of the same rule, and the one that fails open rather than
// closed if it is missed: a document that declares nothing means the default
// backend, so naming that backend explicitly against a legacy box must be a
// match. Comparing an absent declaration as "unset" would reject the one
// spelling every pre-existing instance answers to.
func TestNamingTheDefaultBackendMatchesALegacyDeclaration(t *testing.T) {
	home := routingHome(t)
	writeInstanceDoc(t, home, "", `{"schema_version":"2","projects":[]}`)
	t.Setenv(config.InstanceEnvKey, config.DefaultInstance)

	code, _, backendName, stderr := runRouted(t,
		[]string{"project", "list", "--json", "--backend", backend.DefaultName},
		&fakeProjectService{listOut: []projects.Project{sampleProject()}})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if backendName != backend.DefaultName {
		t.Errorf("backend = %q, want %q", backendName, backend.DefaultName)
	}
}

// A derived instance has no document until `vm init` writes one, so the flag is
// the declaration there. Falling back to the default backend because the
// document is missing would run Hermes commands against the box the operator
// asked to build for another agent.
func TestADerivedInstanceTakesItsBackendFromTheFlag(t *testing.T) {
	routingHome(t)
	code, _, backendName, stderr := runRouted(t,
		[]string{"project", "list", "--json", "--backend", "claude-code"},
		&fakeProjectService{listOut: []projects.Project{sampleProject()}})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if backendName != "claude-code" {
		t.Errorf("backend = %q, want claude-code", backendName)
	}
}

// An unknown backend is refused before an instance is derived for it. Deriving
// one would create a config directory, and eventually a VM name, for an agent
// this build cannot run.
func TestAnUnknownBackendIsRefusedBeforeAnythingIsDerived(t *testing.T) {
	home := routingHome(t)
	code, _, _, stderr := runRouted(t,
		[]string{"project", "list", "--json", "--backend", "nosuchagent"},
		&fakeProjectService{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, ExitUsage, stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "torio", "instances", config.InstancePrefix+"nosuchagent")); err == nil {
		t.Error("a config directory was derived for a backend this build cannot run")
	}
}

// TestAddWithoutARemoteUsesTheOneOnRecord pins the materialize form. A project
// exists once in the registry and once per backend on disk, so attaching it to
// a second guest must not be an invitation to retype a remote — a typo there
// would put a different repository behind a name that already means something.
func TestAddWithoutARemoteUsesTheOneOnRecord(t *testing.T) {
	routingHome(t)
	registered := projects.Project{
		ID:          "demo",
		DisplayName: "Demo",
		Remote:      "git@github.com:wzslr321/demo.git",
		Path:        "/home/claude/projects/demo",
	}
	service := &fakeProjectService{
		listOut:   []projects.Project{registered},
		addReport: projects.AddReport{Project: registered, Cloned: true, Registered: true},
	}
	code, _, _, stderr := runRouted(t,
		[]string{"project", "add", "demo", "--backend", "claude-code"}, service)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if service.addReq.Remote != registered.Remote {
		t.Errorf("add remote = %q, want the recorded %q", service.addReq.Remote, registered.Remote)
	}
	if service.addReq.ID != "demo" || service.addReq.DisplayName != "Demo" {
		t.Errorf("add request = %+v, want the recorded identity", service.addReq)
	}
}

// An id with nothing on record cannot be completed from anywhere, so it is a
// usage error naming the missing argument rather than an attach that invents a
// remote.
func TestAddWithoutARemoteRefusesAnUnregisteredID(t *testing.T) {
	routingHome(t)
	service := &fakeProjectService{}
	code, _, _, _ := runRouted(t, []string{"project", "add", "nothing-here"}, service)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if service.addReq.ID != "" {
		t.Errorf("an unregistered id reached the manager: %+v", service.addReq)
	}
}

// TestAddNeverPointsAtACommandTheBackendCannotRun pins the last of the
// declared-absent-registry family. `use` selects the active project in the
// backend's registry; a backend that keeps none answers NO_REGISTRY, so naming
// it as the next step sends the operator to an error to learn something the
// command that printed it already knew.
func TestAddNeverPointsAtACommandTheBackendCannotRun(t *testing.T) {
	routingHome(t)
	attached := projects.Project{
		ID:          "demo",
		DisplayName: "Demo",
		Remote:      "git@github.com:wzslr321/demo.git",
		Path:        "/home/claude/projects/demo",
	}
	service := &fakeProjectService{addReport: projects.AddReport{Project: attached, Cloned: true, Registered: true}}
	add := func(backendName string) string {
		t.Helper()
		var out, errBuf bytes.Buffer
		a := &app{
			stdout:             &out,
			stderr:             &errBuf,
			build:              testBuild(),
			lookupOperatorUser: func() (string, error) { return "testop", nil },
			newProjects:        func(*lima.Adapter, lima.BootstrapOptions) projectService { return service },
		}
		args := []string{"project", "add", "demo", attached.Remote, "--backend", backendName}
		if code := runWithApp(context.Background(), a, args); code != int(ExitOK) {
			t.Fatalf("%s: exit = %d, want 0; stderr=%q", backendName, code, errBuf.String())
		}
		return out.String()
	}

	if got := add("claude-code"); strings.Contains(got, "project use") {
		t.Errorf("add pointed at `project use` on a backend that declares no registry: %q", got)
	} else if !strings.Contains(got, "project enter") {
		t.Errorf("add gave no usable next step: %q", got)
	}
	// The backend that does keep a registry keeps the step that drives it, so
	// this is a branch on what the backend declares rather than a step dropped
	// for everyone.
	if got := add(backend.DefaultName); !strings.Contains(got, "project use") {
		t.Errorf("add dropped `project use` on a backend that keeps a registry: %q", got)
	}
}

// TestShowReportsNoRegistryRatherThanAnAbsentOne finishes the family the
// declared-absent registry belongs to. `show` reported a `hermes` block of
// all-falses and printed "hermes: absent" on a Claude Code box — naming a
// backend that is not running there and reporting its registration as missing.
// An object full of falses reads as "the registration is gone", which is a
// different statement from "there is nowhere to register".
func TestShowReportsNoRegistryRatherThanAnAbsentOne(t *testing.T) {
	routingHome(t)
	service := &fakeProjectService{showReport: projects.ShowReport{
		Project: projects.Project{
			ID: "demo", DisplayName: "Demo",
			Remote: "git@github.com:wzslr321/demo.git",
			Path:   "/home/claude/projects/demo",
		},
	}}
	show := func(backendName string, jsonOut bool) string {
		t.Helper()
		var out, errBuf bytes.Buffer
		a := &app{
			stdout:             &out,
			stderr:             &errBuf,
			build:              testBuild(),
			lookupOperatorUser: func() (string, error) { return "testop", nil },
			newProjects:        func(*lima.Adapter, lima.BootstrapOptions) projectService { return service },
		}
		args := []string{"project", "show", "demo", "--backend", backendName}
		if jsonOut {
			args = append(args, "--json")
		}
		if code := runWithApp(context.Background(), a, args); code != int(ExitOK) {
			t.Fatalf("%s: exit = %d, want 0; stderr=%q", backendName, code, errBuf.String())
		}
		return out.String()
	}

	envelope := show("claude-code", true)
	if strings.Contains(envelope, `"hermes"`) {
		t.Errorf("the envelope describes a registration the backend cannot have: %s", envelope)
	}
	if !strings.Contains(envelope, `"registry_declared":false`) {
		t.Errorf("the envelope does not say the backend declares no registry: %s", envelope)
	}
	human := show("claude-code", false)
	if strings.Contains(human, "hermes:") {
		t.Errorf("human output labels a line after a backend that is not running there: %q", human)
	}
	if !strings.Contains(human, `none declared by backend "claude-code"`) {
		t.Errorf("human output does not name which backend declares no registry: %q", human)
	}

	// The backend that does keep one still reports it both ways, so this is a
	// branch on the declaration rather than a block dropped for everyone.
	envelope = show(backend.DefaultName, true)
	if !strings.Contains(envelope, `"hermes"`) || !strings.Contains(envelope, `"registry_declared":true`) {
		t.Errorf("a backend with a registry lost its registration block: %s", envelope)
	}
}

// TestRemoveClaimsNoArchivalItDidNotPerform is the worst of the family: the
// default branch of the human message asserts that a Hermes project was
// archived, so on a backend with no registry `remove` reported an action Torio
// had not taken.
func TestRemoveClaimsNoArchivalItDidNotPerform(t *testing.T) {
	routingHome(t)
	service := &fakeProjectService{removeReport: projects.RemoveReport{
		Project:          projects.Project{ID: "demo", DisplayName: "Demo", Remote: "git@github.com:wzslr321/demo.git"},
		CheckoutRetained: true,
		CheckoutPath:     "/home/claude/projects/demo",
	}}
	var out, errBuf bytes.Buffer
	a := &app{
		stdout:             &out,
		stderr:             &errBuf,
		build:              testBuild(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
		newProjects:        func(*lima.Adapter, lima.BootstrapOptions) projectService { return service },
	}
	if code := runWithApp(context.Background(), a,
		[]string{"project", "remove", "demo", "--backend", "claude-code"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errBuf.String())
	}
	if strings.Contains(out.String(), "archived") && !strings.Contains(out.String(), "no registry to archive it from") {
		t.Errorf("remove claimed an archival on a backend with no registry: %q", out.String())
	}
	if !strings.Contains(out.String(), "still exists on the VM") {
		t.Errorf("remove stopped saying the checkout is retained: %q", out.String())
	}
}

// TestTheRegistryFollowsTheRoutedInstance pins the wiring the shared registry
// depends on: the manager's registry must resolve the instance this invocation
// settled on, not re-derive one from the environment. A registry that
// disagreed would read a different document than the command it serves.
func TestTheRegistryFollowsTheRoutedInstance(t *testing.T) {
	home := routingHome(t)
	if err := config.WriteRegistry(filepath.Join(home, "torio", "projects.json"),
		[]config.Project{{ID: "demo", DisplayName: "Demo", Remote: "git@github.com:wzslr321/demo.git"}}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	var out, errBuf bytes.Buffer
	a := &app{
		stdout:             &out,
		stderr:             &errBuf,
		build:              testBuild(),
		lookupOperatorUser: func() (string, error) { return "testop", nil },
	}
	if code := runWithApp(context.Background(), a,
		[]string{"project", "list", "--json", "--backend", "claude-code"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), `"demo"`) {
		t.Fatalf("the routed instance does not see the shared registry: %s", out.String())
	}
	// The path is the other half: the same project, in the workspace the
	// selected backend owns rather than the one that attached it.
	if !strings.Contains(out.String(), "/home/claude/projects/demo") {
		t.Fatalf("the project did not derive the selected backend's workspace: %s", out.String())
	}
}

// TestSwitchingBackendsKeepsAnUnmigratedRegistry drives the same case end to
// end, through the real registry rather than a fake. An installation that has
// not migrated has its projects in the default instance's document, and the
// first `--backend` invocation must still see them — this is the state every
// existing installation is in the first time it runs this build.
func TestSwitchingBackendsKeepsAnUnmigratedRegistry(t *testing.T) {
	home := routingHome(t)
	writeInstanceDoc(t, home, "", `{"schema_version":"3","backend":"hermes","projects":[`+
		`{"id":"demo","display_name":"Demo","remote":"git@github.com:wzslr321/demo.git"}]}`)

	for _, args := range [][]string{
		{"project", "list", "--json"},
		{"project", "list", "--json", "--backend", "claude-code"},
	} {
		var out, errBuf bytes.Buffer
		a := &app{
			stdout:             &out,
			stderr:             &errBuf,
			build:              testBuild(),
			lookupOperatorUser: func() (string, error) { return "testop", nil },
		}
		if code := runWithApp(context.Background(), a, args); code != int(ExitOK) {
			t.Fatalf("%v: exit = %d, want 0; stderr=%q", args, code, errBuf.String())
		}
		if !strings.Contains(out.String(), `"demo"`) {
			t.Fatalf("%v does not see the unmigrated registry: %s", args, out.String())
		}
	}
}
