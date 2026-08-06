package projects

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

func newTestManager(g Guest, r Registry) *Manager {
	m := New(g, r, lima.BootstrapOptions{OperatorUser: testOwner})
	// A host agent that is present and holds a key: the only shape that lets a
	// preflight reach its last check. Every other shape is set per test.
	m.agent = &fakeAgent{socket: testAgentSocket, identities: 1}
	return m
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
		"git ls-remote -- " + testRemote + " HEAD",
		"git clone --quiet -- " + testRemote + " " + testPath,
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
	if !g.saw("git ls-remote -- "+publicRemote) || !g.saw("git clone --quiet -- "+publicRemote+" "+testPath) {
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

	_, err := New(g, emptyRegistry(), lima.BootstrapOptions{}).Add(context.Background(), addRequest())
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

// A broken CLI is one that cannot answer at all. `show` alone can no longer
// prove that: Hermes 0.19.1 exits non-zero for an unknown project, which is the
// most ordinary state there is. `list` failing is what remains.
func TestAddFailsClosedWhenTheHermesProjectCLIIsBroken(t *testing.T) {
	g := readyFake()
	g.hermesShowExit = 2
	g.hermesListExit = 2
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

// The two commands disagreeing is the case that must stay closed: `list` names
// the slug, so the project exists, but `show` will not describe it — and `show`
// is the only source of the primary path. Creating here would ask Hermes for a
// slug it already holds, which it answers by silently inventing another one.
func TestAddFailsClosedWhenListNamesASlugShowWillNotDescribe(t *testing.T) {
	g := readyFake()
	g.hermesPresent = true // so `list` names the slug
	g.hermesShowExit = 2   // but `show` cannot describe it
	r := emptyRegistry()

	_, err := newTestManager(g, r).Add(context.Background(), addRequest())
	assertKind(t, err, KindRegistration)
	if g.saw("hermes project create") {
		t.Fatalf("a project was created over a slug Hermes already lists: %v", g.calls)
	}
	if len(r.saved) != 0 {
		t.Fatalf("config was written while the Hermes state was unknown: %#v", r.saved)
	}
}

// Hermes 0.19.1 exits non-zero for an unknown project where 0.19.0 exited 0.
// Reading that as a broken CLI made the most ordinary path in the product —
// adding the first project to a fresh VM — fail closed on a guest that was
// working perfectly. `list` is what answers the existence question now.
func TestAddRegistersWhenShowExitsNonZeroForAnUnknownProject(t *testing.T) {
	g := readyFake()
	g.hermesUnknownShowExit = 1
	r := emptyRegistry()

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !report.HermesCreated {
		t.Fatalf("report = %#v, want the Hermes project recorded as created", report)
	}
	if !g.saw("hermes project create") {
		t.Fatalf("the project was never registered: %v", g.calls)
	}
	if len(r.saved) != 1 {
		t.Fatalf("config entries = %d, want 1", len(r.saved))
	}
}

// The fake defaults to the version the product pins (0.19.1). A guest may still
// be running 0.19.0, whose `show` exits 0 for an unknown project, so both
// spellings of "no such project" stay covered rather than one replacing the
// other.
func TestAddRegistersWhenShowExitsZeroForAnUnknownProject(t *testing.T) {
	g := readyFake()
	g.hermesUnknownShowExit = 0
	r := emptyRegistry()

	report, err := newTestManager(g, r).Add(context.Background(), addRequest())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !report.HermesCreated {
		t.Fatalf("report = %#v, want the Hermes project recorded as created", report)
	}
	if len(r.saved) != 1 {
		t.Fatalf("config entries = %d, want 1", len(r.saved))
	}
}

func TestHermesProjectListedMatchesTheSlugColumnOnly(t *testing.T) {
	const listing = "  demo                     Demo  [1 folder(s)]\n" +
		"  other                    demo  [2 folder(s)]\n"

	if !hermesProjectListed(listing, "demo") {
		t.Error("the listed slug was not found")
	}
	if hermesProjectListed(listing, "absent") {
		t.Error("an absent slug was reported as listed")
	}
	// "demo" is the *name* of the second project. A substring search would let
	// one project answer an existence question about another.
	if hermesProjectListed("  other                    demo  [2 folder(s)]\n", "demo") {
		t.Error("a project name answered for a different project's slug")
	}
	if hermesProjectListed("No projects yet. Create one with `hermes project create <name>`.\n", "demo") {
		t.Error("the empty-listing sentence was read as a project")
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

// --- operator shell preflight ---

// shellFake is a guest that satisfies every guest-side shell precondition: a
// bootstrap-verified VM holding a compliant checkout of the registered remote.
func shellFake() *fakeGuest {
	f := attachedFake()
	f.mode = "2775"
	return f
}

func TestEnterPreflightVerifiesTheWorkspaceWithoutInspectingTheHostAgent(t *testing.T) {
	g := shellFake()
	agent := &fakeAgent{err: errors.New("agent must not be inspected")}
	m := newTestManager(g, registryWith(testProject()))
	m.agent = agent

	session, err := m.EnterPreflight(context.Background(), testID)
	if err != nil {
		t.Fatalf("EnterPreflight() error = %v", err)
	}
	if session.Project.Path != testPath || session.Group != sharedGroup ||
		session.Instance != lima.InstanceName || session.OperatorUser != testOwner {
		t.Fatalf("session = %#v", session)
	}
	if !slices.Equal(session.Verified, enterPreconditions) {
		t.Errorf("verified = %v, want %v", session.Verified, enterPreconditions)
	}
	if agent.calls != 0 {
		t.Errorf("ordinary project enter queried the host agent %d times, want 0", agent.calls)
	}
}

func TestShellPreflightVerifiesEveryPreconditionAndOpensNothing(t *testing.T) {
	g := shellFake()
	agent := &fakeAgent{socket: testAgentSocket, identities: 2}
	m := newTestManager(g, registryWith(testProject()))
	m.agent = agent

	session, err := m.ShellPreflight(context.Background(), testID)
	if err != nil {
		t.Fatalf("ShellPreflight() error = %v", err)
	}
	if session.Project.Path != testPath || session.Group != sharedGroup ||
		session.Instance != lima.InstanceName || session.OperatorUser != testOwner {
		t.Fatalf("session = %#v", session)
	}
	for _, want := range shellPreconditions {
		if !slices.Contains(session.Verified, want) {
			t.Errorf("verified = %v, want it to contain %q", session.Verified, want)
		}
	}
	if agent.calls != 1 {
		t.Errorf("agent queried %d times, want exactly 1", agent.calls)
	}
}

// The preflight proves a session can be opened. It must never prove it by
// pushing: a push is the operator's act, it mutates a remote, and Torio has no
// credential of its own to do it with.
func TestShellPreflightNeverTestsThePush(t *testing.T) {
	g := shellFake()

	if _, err := newTestManager(g, registryWith(testProject())).ShellPreflight(context.Background(), testID); err != nil {
		t.Fatalf("ShellPreflight() error = %v", err)
	}
	for _, call := range g.calls {
		joined := strings.Join(call.argv, " ")
		for _, forbidden := range []string{"push", "ssh -A", "ForwardAgent", "sh -c", "ssh-add"} {
			if strings.Contains(joined, forbidden) {
				t.Errorf("preflight argv %q contains %q", joined, forbidden)
			}
		}
	}
}

func TestShellPreflightRejectsAnUnregisteredProject(t *testing.T) {
	g := readyFake()
	agent := &fakeAgent{socket: testAgentSocket, identities: 1}
	m := newTestManager(g, emptyRegistry())
	m.agent = agent

	_, err := m.ShellPreflight(context.Background(), testID)
	assertKind(t, err, KindConflict)
	if len(g.calls) != 0 || agent.calls != 0 {
		t.Fatalf("an unknown project reached the guest (%v) or the agent (%d)", g.calls, agent.calls)
	}
}

// A VM that is not Running and a guest missing the operator shell helper are
// both bootstrap verification failures. The preflight reuses that one gate
// rather than re-deriving a weaker check, and passes its reason through so the
// operator learns which invariant failed.
func TestShellPreflightRequiresABootstrapVerifiedGuest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
	}{
		{"vm not running", `instance "torio" is stopped; run ` + "`torio vm start`" + ` first`},
		{"helper missing", "operator_shell_helper: no operator shell helper at /usr/local/bin/torio-project-shell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := shellFake()
			g.bootstrapErr = errors.New(tc.reason)

			_, err := newTestManager(g, registryWith(testProject())).ShellPreflight(context.Background(), testID)
			assertKind(t, err, KindPrecondition)
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error = %v, want it to name the failed bootstrap check", err)
			}
		})
	}
}

// The registry says the project is attached. Every way the guest can disagree
// is a postcondition that no longer holds, and the message names the stable
// marker instead of leaving the operator to guess.
func TestShellPreflightRefusesADriftedCheckout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*fakeGuest)
		marker string
	}{
		{"absent", func(f *fakeGuest) { f.pathExists = false; f.isRepo = false }, "checkout_absent"},
		{"symlink", func(f *fakeGuest) { f.pathSymlink = true }, "path_is_symlink"},
		{"not a repository", func(f *fakeGuest) { f.isRepo = false }, "not_a_git_repository"},
		{"origin drift", func(f *fakeGuest) { f.origin = "git@github.com:owner/other.git" }, "origin_mismatch"},
		{"shared permissions", func(f *fakeGuest) { f.mode = "755"; f.group = lima.HermesUser }, "shared_permissions_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := shellFake()
			tc.mutate(g)
			agent := &fakeAgent{socket: testAgentSocket, identities: 1}
			m := newTestManager(g, registryWith(testProject()))
			m.agent = agent

			_, err := m.ShellPreflight(context.Background(), testID)
			assertKind(t, err, KindVerification)
			if !strings.Contains(err.Error(), tc.marker) {
				t.Errorf("error = %v, want it to name %q", err, tc.marker)
			}
		})
	}
}

// No agent socket is an environment the operator has not started yet; an agent
// holding nothing is access nobody has granted. They are different remedies, so
// they are different kinds.
func TestShellPreflightRequiresAnAgentThatHoldsAnIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent *fakeAgent
		want  ErrorKind
	}{
		{"no socket", &fakeAgent{socket: "", identities: 1}, KindPrecondition},
		{"blank socket", &fakeAgent{socket: "   ", identities: 1}, KindPrecondition},
		{"no identity", &fakeAgent{socket: testAgentSocket}, KindAuth},
		{"unreachable agent", &fakeAgent{socket: testAgentSocket, err: errors.New("dial: no such file")}, KindPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(shellFake(), registryWith(testProject()))
			m.agent = tc.agent

			_, err := m.ShellPreflight(context.Background(), testID)
			assertKind(t, err, tc.want)
		})
	}
}

// A failing agent probe carries whatever the agent printed. That output is the
// one place key material could appear, so the preflight reports the check that
// failed and drops the cause entirely.
func TestShellPreflightNeverSurfacesAgentOutput(t *testing.T) {
	m := newTestManager(shellFake(), registryWith(testProject()))
	m.agent = &fakeAgent{socket: testAgentSocket, err: errors.New("256 SHA256:" + testSecret + " op@mac (ED25519)")}

	_, err := m.ShellPreflight(context.Background(), testID)
	assertKind(t, err, KindPrecondition)
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("preflight error repeated agent output: %v", err)
	}
}

// --- host SSH agent probe ---

// fakeRunner is the host command double for the agent probe. It records the
// exact command and replays one canned result.
type fakeRunner struct {
	cmds []execx.Command
	res  execx.Result
	err  error
}

func (f *fakeRunner) Run(_ context.Context, cmd execx.Command) (execx.Result, error) {
	f.cmds = append(f.cmds, cmd)
	return f.res, f.err
}

// agentListOutput is the shape `ssh-add -l` prints: fingerprints and comments.
// It is not key material, and it still must never leave the probe.
const agentListOutput = "256 SHA256:" + testSecret + " op@mac (ED25519)\n" +
	"3072 SHA256:" + testSecret + "-2 op@mac (RSA)\n"

func TestHostSSHAgentCountsIdentitiesWithoutReturningThem(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  execx.Result
		want int
	}{
		{"two identities", execx.Result{ExitCode: 0, Stdout: []byte(agentListOutput)}, 2},
		{"one identity", execx.Result{ExitCode: 0, Stdout: []byte("256 SHA256:x op@mac (ED25519)\n")}, 1},
		{"agent holds none", execx.Result{ExitCode: 1, Stdout: []byte("The agent has no identities.\n")}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{res: tc.res}
			got, err := hostSSHAgent{runner: runner}.Identities(context.Background())
			if err != nil {
				t.Fatalf("Identities() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Identities() = %d, want %d", got, tc.want)
			}
			if len(runner.cmds) != 1 {
				t.Fatalf("commands = %v, want exactly one", runner.cmds)
			}
			cmd := runner.cmds[0]
			// -l lists fingerprints; -L would list the public keys themselves.
			if cmd.Name != "ssh-add" || !slices.Equal(cmd.Args, []string{"-l"}) {
				t.Fatalf("command = %s %v, want `ssh-add -l`", cmd.Name, cmd.Args)
			}
			if cmd.Timeout <= 0 {
				t.Error("the agent probe must be bounded; a wedged agent socket blocks forever")
			}
		})
	}
}

// An agent that cannot be reached is not an agent holding nothing: the first is
// a broken environment, the second is a key nobody added.
func TestHostSSHAgentFailsClosedWhenItCannotQuery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		res    execx.Result
		runErr error
	}{
		{"cannot connect", execx.Result{ExitCode: 2, Stderr: []byte("Error connecting to agent")}, nil},
		{"probe did not run", execx.Result{ExitCode: -1}, errors.New("exec: ssh-add not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hostSSHAgent{runner: &fakeRunner{res: tc.res, err: tc.runErr}}.Identities(context.Background())
			if err == nil {
				t.Fatal("Identities() error = nil, want a failure")
			}
		})
	}
}

func TestHostSSHAgentNeverRepeatsAgentOutput(t *testing.T) {
	runner := &fakeRunner{res: execx.Result{ExitCode: 2, Stdout: []byte(agentListOutput), Stderr: []byte(agentListOutput)}}

	_, err := hostSSHAgent{runner: runner}.Identities(context.Background())
	if err == nil {
		t.Fatal("Identities() error = nil, want a failure")
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("the probe repeated agent output: %v", err)
	}
}

func TestHostSSHAgentReadsTheSocketFromTheEnvironment(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", testAgentSocket)
	if got := (hostSSHAgent{}).Socket(); got != testAgentSocket {
		t.Fatalf("Socket() = %q, want %q", got, testAgentSocket)
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	if got := (hostSSHAgent{}).Socket(); got != "" {
		t.Fatalf("Socket() = %q, want empty", got)
	}
}

// --- post-session service environment check ---

func TestCheckServiceEnvReportsACleanServiceEnvironment(t *testing.T) {
	g := shellFake()

	check, err := newTestManager(g, registryWith(testProject())).CheckServiceEnv(context.Background())
	if err != nil {
		t.Fatalf("CheckServiceEnv() error = %v", err)
	}
	if !check.Checked || check.AgentSocketPresent {
		t.Fatalf("check = %#v", check)
	}
	if !g.saw("systemctl --user show hermes-serve.service --property=Environment") {
		t.Fatalf("the unit environment was not read: %v", g.calls)
	}
	// Read-only: the check must never write, restart or reload anything.
	for _, call := range g.calls {
		joined := strings.Join(call.argv, " ")
		for _, forbidden := range []string{"set-environment", "restart", "daemon-reload", "start", "stop"} {
			if strings.Contains(joined, forbidden) {
				t.Errorf("service env check argv %q contains %q", joined, forbidden)
			}
		}
	}
}

func TestCheckServiceEnvFailsWhenTheServiceCarriesAForwardedAgent(t *testing.T) {
	g := shellFake()
	g.serviceEnvironment = "HERMES_HOME=/home/hermes/.hermes SSH_AUTH_SOCK=/tmp/agent.42/s"

	check, err := newTestManager(g, registryWith(testProject())).CheckServiceEnv(context.Background())
	assertKind(t, err, KindVerification)
	if !check.Checked || !check.AgentSocketPresent {
		t.Fatalf("check = %#v", check)
	}
	if strings.Contains(err.Error(), "/tmp/agent.42/s") {
		t.Fatalf("the check echoed the service environment: %v", err)
	}
}

// A guest with no backend unit has nothing to leak into. That is not a verdict
// of "clean" and not a failure either — it is a check that did not run.
func TestCheckServiceEnvReportsNotCheckedWhenTheUnitIsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fakeGuest)
	}{
		{"no unit", func(f *fakeGuest) { f.setFailure("systemctl --user show", 1) }},
		{"no hermes uid", func(f *fakeGuest) { f.setFailure("id -u hermes", 1) }},
		{"unparseable uid", func(f *fakeGuest) { f.hermesUID = "nobody" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := shellFake()
			tc.setup(g)

			check, err := newTestManager(g, registryWith(testProject())).CheckServiceEnv(context.Background())
			if err != nil {
				t.Fatalf("CheckServiceEnv() error = %v, want nil", err)
			}
			if check.Checked || check.AgentSocketPresent {
				t.Fatalf("check = %#v", check)
			}
		})
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

// Neither command that talks to the remote may narrate. Torio retains at most
// execx.DefaultMaxOutputPerStream per stream and treats truncation as a
// verification failure, so a command that reports progress or dumps every ref
// can fail on the size of the repository rather than on anything being wrong.
//
// The preflight is the observed case: an unqualified `git ls-remote` against a
// busy upstream answered with 4.7 MiB, because GitHub advertises refs/pull/*.
// Attaching that repository failed with "bounded guest output was truncated"
// while the remote was perfectly readable. Restricting the query to HEAD kept
// the same proof — a readable remote exits 0, an unreadable one exits 128 —
// and brought the answer down to one line.
//
// The clone is the latent sibling, fixed alongside it: progress output grows
// with repository size and network time, and nothing here reads it either.
func TestRemoteCommandsCannotTruncateTheirOwnOutput(t *testing.T) {
	g := readyFake()
	if _, err := newTestManager(g, emptyRegistry()).Add(context.Background(), addRequest()); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	find := func(needle string) string {
		for _, c := range g.calls {
			if joined := strings.Join(c.argv, " "); strings.Contains(joined, needle) {
				return joined
			}
		}
		return ""
	}

	preflight := find("git ls-remote")
	if preflight == "" {
		t.Fatal("no remote preflight was issued")
	}
	if !strings.HasSuffix(preflight, " HEAD") {
		t.Errorf("preflight asks for every ref the server advertises:\n%s", preflight)
	}
	// --exit-code would call an empty repository unreadable: it has no HEAD,
	// yet the guest can read it fine.
	if strings.Contains(preflight, "--exit-code") {
		t.Errorf("preflight uses --exit-code, which fails an empty repository:\n%s", preflight)
	}

	clone := find("git clone")
	if clone == "" {
		t.Fatal("no clone was issued")
	}
	if !strings.Contains(clone, "--quiet") {
		t.Errorf("clone is not quiet:\n%s", clone)
	}
	// Positions are measured inside the git invocation: the sudo prefix carries
	// a "--" of its own, and comparing against that one would pass for the
	// wrong reason.
	gitPart := clone[strings.Index(clone, "git clone"):]
	if strings.Index(gitPart, "--quiet") > strings.Index(gitPart, " -- ") {
		t.Errorf("--quiet must precede git's -- separator, or it reads as a path:\n%s", gitPart)
	}
}
