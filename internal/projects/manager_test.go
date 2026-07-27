package projects

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
)

func newTestManager(g Guest, r Registry) *Manager {
	return New(g, r, lima.BootstrapOptions{OperatorUser: testOwner})
}

func addRequest() AddRequest {
	return AddRequest{ID: testID, DisplayName: testName, Remote: testRemote}
}

func TestAddClonesAbsentPathAndRegistersProject(t *testing.T) {
	g := readyFake()
	r := emptyRegistry()

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !report.Cloned || report.Adopted {
		t.Fatalf("report = %#v, want a fresh clone", report)
	}
	if report.Project.Path != testPath {
		t.Fatalf("derived path = %q, want %q", report.Project.Path, testPath)
	}
	for _, fragment := range []string{
		"env GIT_TERMINAL_PROMPT=0",
		"git ls-remote -- " + testRemote,
		"git clone -- " + testRemote + " " + testPath,
		"chown -R hermes:torio-projects -- " + testPath,
		"find " + testPath + " -type d -exec chmod g+s",
		"sudo -n -u hermes -- git config --global --add safe.directory " + testPath,
		"sudo -n -u " + testOwner + " -- git config --global --add safe.directory " + testPath,
		"hermes project create " + testName + " " + testPath + " --slug " + testID,
	} {
		if !g.saw(fragment) {
			t.Errorf("missing typed guest argv containing %q", fragment)
		}
	}
	if g.saw("--depth") || g.saw("--recurse-submodules") || g.saw("sh -c") {
		t.Errorf("add used a shallow clone, submodules or a shell: %v", g.calls)
	}
	if len(r.saved) != 1 || len(r.saved[0].Projects) != 1 || r.saved[0].Projects[0].ID != testID {
		t.Fatalf("persisted registry = %#v, want exactly one entry for %q", r.saved, testID)
	}
}

// allowedGuestPrograms is every executable this package may invoke on the
// guest. A shell is deliberately absent: an argv is either one of these
// programs or it is a bug.
var allowedGuestPrograms = map[string]bool{
	"true": true, "test": true, "stat": true, "find": true,
	"chown": true, "chmod": true, "git": true, "hermes": true, "env": true,
}

// Nothing this package runs may reach the guest through a shell, and nothing it
// runs may put the hermes service identity into the docker group.
func TestAddUsesOnlyExactArgvAndNeverTouchesTheDockerGroup(t *testing.T) {
	g := readyFake()
	req := addRequest()
	req.Use = true

	if _, err := newTestManager(g, emptyRegistry()).Add(context.Background(), req); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if len(g.calls) == 0 {
		t.Fatal("no guest commands ran")
	}
	for _, call := range g.calls {
		joined := strings.Join(call.argv, " ")
		if strings.Contains(joined, "docker") {
			t.Errorf("guest argv mentions docker: %q", joined)
		}
		program, ok := programOf(call.argv)
		if !ok {
			t.Errorf("guest argv is not a sudo-delimited command: %q", joined)
			continue
		}
		if !allowedGuestPrograms[program] {
			t.Errorf("guest argv runs unexpected program %q: %q", program, joined)
		}
	}
}

// programOf returns the executable a `sudo -n [-u user] -- <program> …` argv
// actually runs.
func programOf(argv []string) (string, bool) {
	for i, token := range argv {
		if token == "--" && i+1 < len(argv) {
			return argv[i+1], true
		}
	}
	return "", false
}

// A public HTTPS remote and a private SSH remote take the same path: the same
// noninteractive preflight, the same full clone, the same verification. Only
// whether the guest can already read the remote differs.
func TestAddClonesAPublicHTTPSRemote(t *testing.T) {
	const publicRemote = "https://github.com/owner/demo.git"
	g := readyFake()
	g.remote = publicRemote
	req := addRequest()
	req.Remote = publicRemote
	r := emptyRegistry()

	report, err := newTestManager(g, r).Add(context.Background(), req)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !report.Cloned {
		t.Fatalf("report = %#v, want a fresh clone", report)
	}
	if !g.saw("git ls-remote -- "+publicRemote) || !g.saw("git clone -- "+publicRemote+" "+testPath) {
		t.Fatalf("public remote did not take the noninteractive path: %v", g.calls)
	}
	if len(r.saved) != 1 || r.saved[0].Projects[0].Remote != publicRemote {
		t.Fatalf("persisted registry = %#v", r.saved)
	}
}

func TestAddPreflightsRemoteBeforeCloning(t *testing.T) {
	g := readyFake()
	g.remoteReadable = false
	r := emptyRegistry()

	_, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindAuth)
	if g.saw("git clone") {
		t.Fatalf("clone ran after a failed preflight: %v", g.calls)
	}
	if len(r.saved) != 0 {
		t.Fatalf("config was written after a failed preflight: %#v", r.saved)
	}
}

// The preflight failure carries the guest's own stderr, which is exactly where a
// credential would show up. None of it may reach the error or the report.
func TestAddNeverEchoesRemoteFailureOutput(t *testing.T) {
	g := readyFake()
	g.remoteReadable = false

	report, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
	if err == nil {
		t.Fatal("Add() error = nil, want an auth failure")
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("error echoed guest output carrying a secret: %v", err)
	}
	for _, note := range report.Notes {
		if strings.Contains(note, testSecret) {
			t.Fatalf("report note echoed guest output carrying a secret: %q", note)
		}
	}
}

func TestAddIsIdempotentForAnAlreadyAttachedProject(t *testing.T) {
	g := attachedFake()
	r := registryWith(testProject())

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if report.Cloned || !report.Adopted || !report.Registered {
		t.Fatalf("report = %#v, want an adopted, already-registered project", report)
	}
	if g.saw("git clone") || g.saw("hermes project create") {
		t.Fatalf("rerun mutated an attached project: %v", g.calls)
	}
	if n := g.count("--add safe.directory"); n != 0 {
		t.Fatalf("rerun appended %d safe.directory entries, want 0", n)
	}
	if len(r.saved) != 0 {
		t.Fatalf("rerun rewrote config: %#v", r.saved)
	}
}

func TestAddAdoptsCompliantCheckoutThatIsNotRegisteredYet(t *testing.T) {
	g := attachedFake()
	g.hermesPresent = false
	g.hermesPrimary = ""
	r := emptyRegistry()

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if report.Cloned || !report.Adopted || !report.HermesCreated {
		t.Fatalf("report = %#v, want an adopted checkout with a fresh registration", report)
	}
	if g.saw("git clone") {
		t.Fatalf("an existing checkout was re-cloned: %v", g.calls)
	}
	if len(r.saved) != 1 {
		t.Fatalf("persisted registry writes = %d, want 1", len(r.saved))
	}
}

func TestAddRefusesConflictingCheckouts(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*fakeGuest)
		wantAny []string
	}{
		{"dirty worktree", func(f *fakeGuest) { f.dirty = true }, []string{"uncommitted"}},
		{"wrong origin", func(f *fakeGuest) { f.origin = "git@github.com:someone/other.git" }, []string{"origin"}},
		{"not a repository", func(f *fakeGuest) { f.isRepo = false }, []string{"Git repository"}},
		{"nested repository", func(f *fakeGuest) { f.topLevel = "/home/hermes/projects" }, []string{"Git repository"}},
		{"shallow clone", func(f *fakeGuest) { f.shallow = true }, []string{"shallow"}},
		{"credential helper", func(f *fakeGuest) { f.credHelper = true }, []string{"credential helper"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := attachedFake()
			g.hermesPresent = false
			tc.mutate(g)
			r := emptyRegistry()

			_, err := newTestManager(g, r).Add(context.Background(), addRequest())
			assertKind(t, err, KindConflict)
			for _, want := range tc.wantAny {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to mention %q", err, want)
				}
			}
			if g.saw("chown") || g.saw("chmod") || g.saw("hermes project create") {
				t.Fatalf("a conflicting checkout was mutated: %v", g.calls)
			}
			if g.saw("rm ") || g.saw("git reset") || g.saw("git clean") {
				t.Fatalf("a conflicting checkout was reset, cleaned or deleted: %v", g.calls)
			}
			if len(r.saved) != 0 {
				t.Fatalf("config was written for a conflicting checkout: %#v", r.saved)
			}
		})
	}
}

func TestAddRefusesSymlinkedWorkspacePath(t *testing.T) {
	g := readyFake()
	g.pathSymlink = true

	_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
	assertKind(t, err, KindConflict)
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %v, want it to name the symlink", err)
	}
	if g.saw("git -C " + testPath) {
		t.Fatalf("Git ran inside a symlinked workspace path: %v", g.calls)
	}
}

func TestAddRefusesWorkspacePathThatIsNotADirectory(t *testing.T) {
	g := readyFake()
	g.pathExists = true
	g.pathIsFile = true

	_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
	assertKind(t, err, KindConflict)
	if g.saw("git -C " + testPath) {
		t.Fatalf("Git ran inside a non-directory workspace path: %v", g.calls)
	}
}

func TestAddRejectsProjectIDsThatEscapeTheWorkspaceRoot(t *testing.T) {
	for _, id := range []string{"../etc", "a/b", "..", ".", "", "Demo", "-flag"} {
		t.Run(id, func(t *testing.T) {
			g := readyFake()
			req := addRequest()
			req.ID = id

			_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), req)
			assertKind(t, err, KindInvalidConfig)
			if len(g.calls) != 0 {
				t.Fatalf("a rejected project id reached the guest: %v", g.calls)
			}
		})
	}
}

func TestDerivePathContainsEveryIDUnderTheWorkspaceRoot(t *testing.T) {
	for _, id := range []string{"../etc", "a/b", "..", ".", "", "a/../../b"} {
		if _, err := derivePath(id); err == nil {
			t.Errorf("derivePath(%q) error = nil, want a containment failure", id)
		}
	}
	got, err := derivePath(testID)
	if err != nil || got != testPath {
		t.Fatalf("derivePath(%q) = %q, %v; want %q, nil", testID, got, err, testPath)
	}
}

func TestAddRejectsRemotesCarryingCredentials(t *testing.T) {
	g := readyFake()
	req := addRequest()
	req.Remote = "https://user:" + testSecret + "@github.com/owner/demo.git"

	_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), req)
	assertKind(t, err, KindInvalidConfig)
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("error echoed the rejected credential: %v", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("a rejected remote reached the guest: %v", g.calls)
	}
}

func TestAddRejectsDisplayNamesThatWouldBeReadAsFlags(t *testing.T) {
	g := readyFake()
	req := addRequest()
	req.DisplayName = "--slug"

	_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), req)
	assertKind(t, err, KindInvalidConfig)
	if len(g.calls) != 0 {
		t.Fatalf("a rejected display name reached the guest: %v", g.calls)
	}
}

func TestAddRefusesDuplicateRemoteWithoutAnExplicitDecision(t *testing.T) {
	g := readyFake()
	r := registryWith(config.Project{ID: "other", DisplayName: "Other", Remote: testRemote})

	_, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindConflict)
	if !errors.Is(err, config.ErrDuplicateRemote) {
		t.Fatalf("error = %v, want it to wrap config.ErrDuplicateRemote", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("a duplicate remote reached the guest: %v", g.calls)
	}
}

func TestAddAcceptsDuplicateRemoteWhenExplicitlyAllowed(t *testing.T) {
	g := readyFake()
	r := registryWith(config.Project{ID: "other", DisplayName: "Other", Remote: testRemote})
	req := addRequest()
	req.AllowDuplicateRemote = true

	if _, err := newTestManager(g, r).Add(context.Background(), req); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if len(r.saved) != 1 || len(r.saved[0].Projects) != 2 {
		t.Fatalf("persisted registry = %#v, want both projects", r.saved)
	}
}

func TestAddRefusesAnIDRegisteredWithDifferentDetails(t *testing.T) {
	g := readyFake()
	r := registryWith(config.Project{ID: testID, DisplayName: testName, Remote: "git@github.com:owner/other.git"})

	_, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindConflict)
	if len(g.calls) != 0 {
		t.Fatalf("a conflicting registry entry reached the guest: %v", g.calls)
	}
}

func TestAddRequiresABootstrapVerifiedVM(t *testing.T) {
	g := readyFake()
	g.bootstrapErr = &lima.Error{Op: "bootstrap", Kind: lima.KindNotRunning}

	_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
	assertKind(t, err, KindPrecondition)
	if len(g.calls) != 0 {
		t.Fatalf("guest commands ran without a verified bootstrap: %v", g.calls)
	}
}

func TestAddRequiresAConfiguredOperatorIdentity(t *testing.T) {
	g := readyFake()

	_, err := New(g, emptyRegistry()).Add(context.Background(), addRequest())
	assertKind(t, err, KindPrecondition)
	if len(g.calls) != 0 {
		t.Fatalf("guest commands ran without an operator identity: %v", g.calls)
	}
}

// A Hermes project holding our slug but pointing elsewhere must never be
// adopted, repointed or duplicated — and `create` would silently make a
// `<slug>-2` project instead of failing.
func TestAddRefusesAForeignHermesProjectHoldingTheSlug(t *testing.T) {
	g := readyFake()
	g.hermesPresent = true
	g.hermesPrimary = "/home/hermes/projects/somewhere-else"
	r := emptyRegistry()

	_, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindRegistration)
	if g.saw("hermes project create") {
		t.Fatalf("a conflicting slug was duplicated: %v", g.calls)
	}
	if len(r.saved) != 0 {
		t.Fatalf("config was written despite a registration conflict: %#v", r.saved)
	}
}

// `hermes project create` exits 0 even when it does nothing, so the only proof
// of registration is re-reading the project afterwards.
func TestAddDetectsRegistrationFailureThatExitsZero(t *testing.T) {
	g := readyFake()
	g.hermesCreateNoop = true
	r := emptyRegistry()

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindRegistration)
	if !report.Cloned {
		t.Fatalf("report = %#v, want the clone recorded", report)
	}
	if g.saw("rm ") || g.saw("rm -rf") {
		t.Fatalf("a failed registration deleted the checkout: %v", g.calls)
	}
	if len(r.saved) != 0 {
		t.Fatalf("config was written despite a failed registration: %#v", r.saved)
	}
}

func TestAddFailsClosedWhenTheHermesProjectCLIIsBroken(t *testing.T) {
	g := readyFake()
	g.hermesShowExit = 2
	r := emptyRegistry()

	_, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindRegistration)
	if g.saw("hermes project create") {
		t.Fatalf("a project was created while its state was unknown: %v", g.calls)
	}
	if len(r.saved) != 0 {
		t.Fatalf("config was written while the Hermes state was unknown: %#v", r.saved)
	}
}

func TestAddConfigWriteFailureKeepsTheCheckoutAndRollsBackRegistration(t *testing.T) {
	g := readyFake()
	r := emptyRegistry()
	r.saveErr = errors.New("read-only file system")

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindConfigWrite)
	if report.Registered {
		t.Fatalf("report claims a registered project after a failed config write: %#v", report)
	}
	if g.saw("rm ") || g.saw("rm -rf") || g.saw("git reset") || g.saw("git clean") {
		t.Fatalf("a failed config write destroyed guest state: %v", g.calls)
	}
	for _, want := range []string{"checkout_retained", "hermes_project_archived", "rerun_finishes"} {
		if !slices.Contains(report.Notes, want) {
			t.Errorf("notes = %v, want %q", report.Notes, want)
		}
	}
	if !g.hermesArchived {
		t.Fatalf("the Hermes project this run created was left active")
	}
}

// The state a failed config write leaves behind must be one a rerun completes:
// the checkout is still compliant and the archived Hermes project is restored.
func TestAddRerunFinishesAfterAConfigWriteFailure(t *testing.T) {
	g := readyFake()
	r := emptyRegistry()
	r.saveErr = errors.New("read-only file system")
	m := newTestManager(g, r)

	if _, err := m.Add(context.Background(), addRequest()); err == nil {
		t.Fatal("Add() error = nil, want a config write failure")
	}
	r.saveErr = nil

	report, err := m.Add(context.Background(), addRequest())
	if err != nil {
		t.Fatalf("rerun Add() error = %v", err)
	}
	if !report.Adopted || !report.HermesRestored || !report.Registered {
		t.Fatalf("report = %#v, want an adopted checkout with a restored registration", report)
	}
	if g.count("git clone") != 1 {
		t.Fatalf("clone count = %d, want the first run's clone reused", g.count("git clone"))
	}
	if len(r.saved) != 1 || len(r.saved[0].Projects) != 1 {
		t.Fatalf("persisted registry = %#v, want one entry after the rerun", r.saved)
	}
}

func TestAddActivatesTheProjectOnlyWhenAsked(t *testing.T) {
	g := readyFake()
	req := addRequest()
	req.Use = true

	report, err := newTestManager(g, emptyRegistry()).Add(context.Background(), req)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !report.Activated || !g.saw("hermes project use "+testID) {
		t.Fatalf("report = %#v, calls = %v; want the project activated", report, g.calls)
	}

	plain := readyFake()
	if _, err := newTestManager(plain, emptyRegistry()).Add(context.Background(), addRequest()); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if plain.saw("hermes project use") {
		t.Fatalf("a plain add activated the project: %v", plain.calls)
	}
}

func TestAddFailsClosedOnTruncatedGuestOutput(t *testing.T) {
	g := readyFake()
	g.truncateOn = "hermes project show"

	_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
	assertKind(t, err, KindVerification)
}

func TestGuestTransportFailuresAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"timeout", context.DeadlineExceeded, KindTimeout},
		{"cancelled", context.Canceled, KindCancelled},
		{"transport", errors.New("limactl not found"), KindTransport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := readyFake()
			g.transportErr = tc.err

			_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
			assertKind(t, err, tc.want)
		})
	}
}

func TestRemoveArchivesFirstAndKeepsTheCheckout(t *testing.T) {
	g := attachedFake()
	r := registryWith(testProject())

	report, err := newTestManager(g, r).Remove(context.Background(), testID)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !report.HermesArchived || !report.CheckoutRetained || report.CheckoutPath != testPath {
		t.Fatalf("report = %#v, want an archived project and a retained checkout", report)
	}
	if !slices.Contains(report.Notes, "checkout_retained") {
		t.Fatalf("notes = %v, want checkout_retained stated explicitly", report.Notes)
	}
	if g.saw("rm ") || g.saw("rm -rf") || g.saw("git clean") {
		t.Fatalf("remove deleted guest state: %v", g.calls)
	}
	if !g.saw("hermes project archive " + testID) {
		t.Fatalf("the Hermes project was not archived: %v", g.calls)
	}
	if len(r.saved) != 1 || len(r.saved[0].Projects) != 0 {
		t.Fatalf("persisted registry = %#v, want the entry gone", r.saved)
	}
}

func TestRemoveIsIdempotentWhenTheHermesProjectIsAlreadyGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*fakeGuest)
		check  func(*testing.T, RemoveReport)
	}{
		{"already archived", func(f *fakeGuest) { f.hermesArchived = true }, func(t *testing.T, r RemoveReport) {
			if !r.HermesAlreadyArchived {
				t.Fatalf("report = %#v, want HermesAlreadyArchived", r)
			}
		}},
		{"absent", func(f *fakeGuest) { f.hermesPresent = false }, func(t *testing.T, r RemoveReport) {
			if !r.HermesAbsent {
				t.Fatalf("report = %#v, want HermesAbsent", r)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := attachedFake()
			tc.mutate(g)
			r := registryWith(testProject())

			report, err := newTestManager(g, r).Remove(context.Background(), testID)
			if err != nil {
				t.Fatalf("Remove() error = %v", err)
			}
			tc.check(t, report)
			if len(r.saved) != 1 || len(r.saved[0].Projects) != 0 {
				t.Fatalf("persisted registry = %#v, want the entry gone", r.saved)
			}
		})
	}
}

// An interrupted removal always stops with the config entry still present,
// because the entry is dropped only after the archive succeeded. A rerun then
// finds an already-archived project and finishes the write.
func TestRemoveRerunFinishesAnInterruptedRemoval(t *testing.T) {
	g := attachedFake()
	r := registryWith(testProject())
	r.saveErr = errors.New("read-only file system")
	m := newTestManager(g, r)

	report, err := m.Remove(context.Background(), testID)
	assertKind(t, err, KindConfigWrite)
	if !report.HermesArchived || !slices.Contains(report.Notes, "config_entry_retained") {
		t.Fatalf("report = %#v, want an archived project and a retained config entry", report)
	}
	if _, found := findProject(r.file, testID); !found {
		t.Fatalf("config entry was dropped despite the failed write")
	}
	r.saveErr = nil

	rerun, err := m.Remove(context.Background(), testID)
	if err != nil {
		t.Fatalf("rerun Remove() error = %v", err)
	}
	if !rerun.HermesAlreadyArchived {
		t.Fatalf("report = %#v, want the archive recognised as already done", rerun)
	}
	if _, found := findProject(r.file, testID); found {
		t.Fatalf("config entry survived the completed removal")
	}
}

func TestRemoveRefusesAForeignHermesProject(t *testing.T) {
	g := attachedFake()
	g.hermesPrimary = "/home/hermes/projects/somewhere-else"
	r := registryWith(testProject())

	_, err := newTestManager(g, r).Remove(context.Background(), testID)
	assertKind(t, err, KindConflict)
	if g.saw("hermes project archive") {
		t.Fatalf("a foreign Hermes project was archived: %v", g.calls)
	}
	if len(r.saved) != 0 {
		t.Fatalf("config was written despite the conflict: %#v", r.saved)
	}
}

func TestRemoveRejectsAnUnregisteredProject(t *testing.T) {
	g := attachedFake()

	_, err := newTestManager(g, emptyRegistry()).Remove(context.Background(), testID)
	assertKind(t, err, KindConflict)
	if !errors.Is(err, config.ErrProjectNotFound) {
		t.Fatalf("error = %v, want it to wrap config.ErrProjectNotFound", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("an unregistered project reached the guest: %v", g.calls)
	}
}

func TestListReportsDerivedPathsSortedByID(t *testing.T) {
	r := registryWith(
		config.Project{ID: "zeta", DisplayName: "Zeta", Remote: "git@github.com:owner/zeta.git"},
		testProject(),
	)

	got, err := newTestManager(readyFake(), r).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	want := []Project{
		{ID: testID, DisplayName: testName, Remote: testRemote, Path: testPath},
		{ID: "zeta", DisplayName: "Zeta", Remote: "git@github.com:owner/zeta.git", Path: lima.HermesWorkspacePath + "/zeta"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestListRunsNoGuestCommand(t *testing.T) {
	g := readyFake()
	if _, err := newTestManager(g, registryWith(testProject())).List(); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("List() reached the guest: %v", g.calls)
	}
}

func TestShowReportsDriftWithoutRepairingIt(t *testing.T) {
	g := attachedFake()
	g.dirty = true
	g.hermesArchived = true
	r := registryWith(testProject())

	report, err := newTestManager(g, r).Show(context.Background(), testID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if report.Checkout.Clean || !report.Checkout.Repository || !report.Checkout.OriginMatches {
		t.Fatalf("checkout = %#v, want a dirty but otherwise intact repository", report.Checkout)
	}
	for _, want := range []string{"worktree_dirty", "hermes_project_archived"} {
		if !slices.Contains(report.Issues, want) {
			t.Errorf("issues = %v, want %q", report.Issues, want)
		}
	}
	if g.saw("chown") || g.saw("git clean") || g.saw("hermes project restore") {
		t.Fatalf("Show() repaired drift: %v", g.calls)
	}
}

func TestShowReportsAnAbsentCheckout(t *testing.T) {
	g := readyFake()
	r := registryWith(testProject())

	report, err := newTestManager(g, r).Show(context.Background(), testID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if report.Checkout.PathExists {
		t.Fatalf("checkout = %#v, want an absent path", report.Checkout)
	}
	for _, want := range []string{"checkout_absent", "hermes_project_absent"} {
		if !slices.Contains(report.Issues, want) {
			t.Errorf("issues = %v, want %q", report.Issues, want)
		}
	}
}

// Unrecognized `hermes project show` output is unverifiable state. It must fail
// closed rather than be interpreted generously.
func TestHermesProjectOutputThatCannotBeParsedFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		show string
	}{
		{"two primary paths", testID + "  [p_0123abcd]\n  primary: " + testPath + "\n  primary: /elsewhere\n"},
		{"no primary path", testID + "  [p_0123abcd]\n  name:    Demo\n"},
		{"another slug", "other  [p_0123abcd]\n  primary: " + testPath + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := readyFake()
			g.hermesShowOutput = tc.show

			_, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest())
			assertKind(t, err, KindVerification)
			if g.saw("hermes project create") {
				t.Fatalf("a project was created from unparseable state: %v", g.calls)
			}
		})
	}
}

func TestShowRejectsAnUnregisteredProject(t *testing.T) {
	_, err := newTestManager(readyFake(), emptyRegistry()).Show(context.Background(), testID)
	assertKind(t, err, KindConflict)
}

func TestUseRequiresARegisteredHermesProject(t *testing.T) {
	g := attachedFake()
	g.hermesArchived = true
	r := registryWith(testProject())

	_, err := newTestManager(g, r).Use(context.Background(), testID)
	assertKind(t, err, KindRegistration)
	if g.saw("hermes project use") {
		t.Fatalf("an unregistered project was activated: %v", g.calls)
	}
}

func TestUseActivatesTheProject(t *testing.T) {
	g := attachedFake()
	r := registryWith(testProject())

	report, err := newTestManager(g, r).Use(context.Background(), testID)
	if err != nil {
		t.Fatalf("Use() error = %v", err)
	}
	if report.Project.Path != testPath || !g.saw("hermes project use "+testID) {
		t.Fatalf("report = %#v, calls = %v", report, g.calls)
	}
}

// `hermes project use` also exits 0 when the slug does not resolve, so success
// is read from the line it prints.
func TestUseFailsWhenActivationIsNotConfirmed(t *testing.T) {
	g := attachedFake()
	g.useSilent = true
	r := registryWith(testProject())

	_, err := newTestManager(g, r).Use(context.Background(), testID)
	assertKind(t, err, KindRegistration)
}

func TestShellSpecReturnsDataAndExecutesNothing(t *testing.T) {
	g := attachedFake()
	r := registryWith(testProject())

	spec, err := newTestManager(g, r).ShellSpec(testID)
	if err != nil {
		t.Fatalf("ShellSpec() error = %v", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("ShellSpec() reached the guest: %v", g.calls)
	}
	if spec.Project.Path != testPath || spec.Group != sharedGroup ||
		spec.Instance != lima.InstanceName || spec.OperatorUser != testOwner {
		t.Fatalf("spec = %#v", spec)
	}
	for _, want := range []string{"vm_running", "checkout_present", "origin_matches", "operator_ssh_agent"} {
		if !slices.Contains(spec.Preconditions, want) {
			t.Errorf("preconditions = %v, want %q", spec.Preconditions, want)
		}
	}
}

func TestShellSpecRejectsAnUnregisteredProject(t *testing.T) {
	g := readyFake()

	_, err := newTestManager(g, emptyRegistry()).ShellSpec(testID)
	assertKind(t, err, KindConflict)
	if len(g.calls) != 0 {
		t.Fatalf("ShellSpec() reached the guest: %v", g.calls)
	}
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want kind %q", want)
	}
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("error type = %T, want *projects.Error: %v", err, err)
	}
	if got.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", got.Kind, want, err)
	}
}
