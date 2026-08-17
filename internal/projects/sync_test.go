package projects

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
)

// localProject is the record ADR-0027 made possible and this decision moves: a
// project with no remote, held by the guest that made it.
func localProject() config.Project {
	return config.Project{ID: testID, DisplayName: testName}
}

// localFake is a bootstrap-verified guest holding a compliant local checkout:
// a repository with no origin, which is what agreement means for a project
// with no remote.
func localFake() *fakeGuest {
	f := attachedFake()
	f.remote = ""
	f.origin = ""
	f.branch = "main"
	f.sync = &fakeSyncState{
		refs:     map[string]string{"heads/main": "aaaa"},
		hubRefs:  map[string]string{},
		ancestor: map[string]bool{},
		counts:   map[string]string{},
	}
	return f
}

// syncManager wires a manager whose host repositories live in a temporary
// directory and whose host Git is scripted.
func syncManager(t *testing.T, g *fakeGuest, r *fakeRegistry, host *fakeHostGit) *Manager {
	t.Helper()
	m := New(g, r, lima.BootstrapOptions{OperatorUser: testOwner})
	m.hostGit = host
	m.hubRoot = t.TempDir()
	return m
}

// existingHub makes the host repository look like one an earlier sync left, so
// a test can drive the direction that carries history back.
func existingHub(t *testing.T, m *Manager) string {
	t.Helper()
	hub, err := m.hubPath(testID)
	if err != nil {
		t.Fatalf("hubPath() error = %v", err)
	}
	if err := os.MkdirAll(hub, 0o700); err != nil {
		t.Fatalf("mkdir hub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hub, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write hub HEAD: %v", err)
	}
	return hub
}

// A remote is already where the boxes of a project meet. Reconciling one
// through the host as well would be a second meeting point, and the two would
// disagree the first time a push landed between syncs.
func TestSyncRefusesAProjectThatHasARemote(t *testing.T) {
	g := attachedFake()
	m := syncManager(t, g, registryWith(testProject()), newFakeHostGit())

	_, err := m.Sync(context.Background(), testID)
	if err == nil {
		t.Fatal("Sync() error = nil, want a refusal for a project that has a remote")
	}
	assertKind(t, err, KindPrecondition)
	if g.saw("bundle create") {
		t.Error("a project with a remote was bundled for the host anyway")
	}
}

// The first sync has no host repository to reconcile with, so it makes one and
// writes every ref the guest holds into it.
func TestSyncCreatesTheHostRepositoryFromTheBoxThatHoldsTheProject(t *testing.T) {
	g := localFake()
	host := newFakeHostGit()
	host.mirror = map[string]string{"heads/main": "aaaa"}
	host.counts = map[string]string{"aaaa": "7"}
	m := syncManager(t, g, registryWith(localProject()), host)

	report, err := m.Sync(context.Background(), testID)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if !report.HubCreated {
		t.Errorf("report = %#v, want the host repository reported as created", report)
	}
	if !host.initialized {
		t.Errorf("the host repository was never initialized: %v", host.calls)
	}
	if !host.saw("init --bare") {
		t.Errorf("the host repository is not bare: %v", host.calls)
	}
	if !g.sync.bundled || !g.sync.carriedOut {
		t.Errorf("the guest history was not bundled and carried: %#v", g.sync)
	}
	if !slices.Contains(host.updated, "refs/heads/main aaaa") {
		t.Errorf("host updates = %v, want refs/heads/main written", host.updated)
	}
	if len(report.ToHub) != 1 || report.ToHub[0].Ref != "heads/main" || report.ToHub[0].Commits != 7 {
		t.Errorf("report.ToHub = %#v, want heads/main with 7 commits", report.ToHub)
	}
	if report.HubPath == "" || !strings.HasSuffix(report.HubPath, testID+".git") {
		t.Errorf("report.HubPath = %q, want the derived host repository", report.HubPath)
	}
	// A host repository made from this box's own bundle already holds every ref
	// it had, so nothing is carried back to prove it.
	if len(report.ToGuest) != 0 || g.sync.fetched {
		t.Errorf("the first sync carried a whole repository back into the box it came from: %#v", report.ToGuest)
	}
}

// The host repository is where a second box gets the project from, so what it
// holds has to come back as well.
func TestSyncCarriesAFastForwardBackToTheGuest(t *testing.T) {
	g := localFake()
	g.branch = "release"
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	// The host holds one branch the guest has never seen and one the guest is
	// behind on. Neither is the branch the checkout stands on, so both move
	// with an ordinary ref write.
	host.refs = map[string]string{"heads/main": "bbbb", "heads/topic": "cccc"}
	host.mirror = map[string]string{"heads/main": "aaaa"}
	host.ancestor = map[string]bool{"aaaa bbbb": true}
	g.sync.hubRefs = map[string]string{"heads/main": "bbbb", "heads/topic": "cccc"}
	g.sync.ancestor = map[string]bool{"aaaa bbbb": true}
	g.sync.counts = map[string]string{"aaaa..bbbb": "2", "cccc": "9"}

	report, err := m.Sync(context.Background(), testID)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if !g.sync.fetched {
		t.Errorf("the guest never read the carried host bundle: %#v", g.sync)
	}
	want := []string{"refs/heads/main bbbb", "refs/heads/topic cccc"}
	if !slices.Equal(g.sync.updated, want) {
		t.Errorf("guest updates = %v, want %v", g.sync.updated, want)
	}
	if len(report.ToGuest) != 2 {
		t.Fatalf("report.ToGuest = %#v, want both refs carried back", report.ToGuest)
	}
	if report.ToGuest[0].Ref != "heads/main" || report.ToGuest[0].Commits != 2 {
		t.Errorf("report.ToGuest[0] = %#v, want heads/main with 2 commits", report.ToGuest[0])
	}
}

// A ref that moved on both sides is a decision somebody made. Torio names it
// and stops: merging it would be Torio writing history into a repository it
// does not own.
func TestSyncLeavesADivergedRefAlone(t *testing.T) {
	g := localFake()
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	host.refs = map[string]string{"heads/main": "bbbb"}
	host.mirror = map[string]string{"heads/main": "aaaa"}
	g.sync.hubRefs = map[string]string{"heads/main": "bbbb"}
	// No ancestry either way: the two histories parted.

	report, err := m.Sync(context.Background(), testID)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if len(host.updated) != 0 {
		t.Errorf("host updates = %v, want a diverged ref left alone", host.updated)
	}
	if len(g.sync.updated) != 0 || len(g.sync.merged) != 0 {
		t.Errorf("guest writes = %v/%v, want a diverged ref left alone", g.sync.updated, g.sync.merged)
	}
	if !slices.Contains(report.Diverged, "heads/main") {
		t.Errorf("report.Diverged = %v, want heads/main named", report.Diverged)
	}
}

// The one working-tree write a sync makes is a fast-forward of the branch the
// checkout stands on, and Git refuses that where work in the tree would be
// written over. The refusal is the ref being held back, not a failed sync.
func TestSyncHoldsBackTheCheckedOutBranchGitRefusesToMove(t *testing.T) {
	g := localFake()
	g.dirty = true
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	host.refs = map[string]string{"heads/main": "bbbb"}
	host.mirror = map[string]string{"heads/main": "aaaa"}
	host.ancestor = map[string]bool{"aaaa bbbb": true}
	g.sync.hubRefs = map[string]string{"heads/main": "bbbb"}
	g.sync.ancestor = map[string]bool{"aaaa bbbb": true}
	g.sync.mergeBlocked = true

	report, err := m.Sync(context.Background(), testID)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if len(g.sync.merged) != 0 || len(g.sync.updated) != 0 {
		t.Errorf("guest writes = %v/%v, want the checked-out branch held back", g.sync.updated, g.sync.merged)
	}
	if !slices.Contains(report.HeldBack, "heads/main") {
		t.Errorf("report.HeldBack = %v, want heads/main named", report.HeldBack)
	}
	// The direction that loses nothing still runs: the guest's own history is
	// carried out whether or not its tree is clean.
	if !g.sync.bundled {
		t.Error("a dirty tree stopped the guest history from being carried out")
	}
}

// The branch the checkout stands on cannot be moved with a ref write: the
// index and the tree would still be at the old commit.
func TestSyncFastForwardsTheCheckedOutBranchThroughTheWorktree(t *testing.T) {
	g := localFake()
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	host.refs = map[string]string{"heads/main": "bbbb"}
	host.mirror = map[string]string{"heads/main": "aaaa"}
	host.ancestor = map[string]bool{"aaaa bbbb": true}
	g.sync.hubRefs = map[string]string{"heads/main": "bbbb"}
	g.sync.ancestor = map[string]bool{"aaaa bbbb": true}

	if _, err := m.Sync(context.Background(), testID); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !slices.Contains(g.sync.merged, hubMirrorRef+"/heads/main") {
		t.Errorf("guest merges = %v, want the checked-out branch fast-forwarded", g.sync.merged)
	}
	if len(g.sync.updated) != 0 {
		t.Errorf("guest ref writes = %v, want the checked-out branch left to the merge", g.sync.updated)
	}
}

// A project made with --local has no commit until somebody makes one. There is
// nothing to carry and nothing to make a host repository out of.
func TestSyncCarriesNothingFromARepositoryWithNoHistory(t *testing.T) {
	g := localFake()
	g.sync.refs = map[string]string{}
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)

	report, err := m.Sync(context.Background(), testID)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if host.initialized {
		t.Error("a host repository was made for a project with no history")
	}
	if g.sync.bundled {
		t.Error("an empty repository was bundled")
	}
	if !slices.Contains(report.Notes, "no_history_yet") {
		t.Errorf("report.Notes = %v, want no_history_yet", report.Notes)
	}
}

// Reconciling needs something on this box to reconcile. A registry entry alone
// is not it, and the refusal names the command that makes the checkout.
func TestSyncRefusesWhenThisBoxHoldsNoCheckout(t *testing.T) {
	g := localFake()
	g.pathExists = false
	g.isRepo = false
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)

	_, err := m.Sync(context.Background(), testID)
	if err == nil {
		t.Fatal("Sync() error = nil, want a refusal with no checkout to carry")
	}
	assertKind(t, err, KindPrecondition)
	if !strings.Contains(err.Error(), "torio project add "+testID) {
		t.Errorf("error = %v, want the command that materializes the checkout", err)
	}
}

// The host path is derived from the id, on the machine that needs it, and goes
// nowhere near the registry: every record has to keep meaning the same thing on
// every machine (ADR-0023).
func TestHubPathIsDerivedAndContained(t *testing.T) {
	m := syncManager(t, localFake(), registryWith(localProject()), newFakeHostGit())
	hub, err := m.hubPath(testID)
	if err != nil {
		t.Fatalf("hubPath() error = %v", err)
	}
	if filepath.Dir(hub) != m.hubRoot || filepath.Base(hub) != testID+".git" {
		t.Errorf("hubPath() = %q, want %q", hub, filepath.Join(m.hubRoot, testID+".git"))
	}
	for _, id := range []string{"../escape", "a/b", ".", ""} {
		if _, err := m.hubPath(id); err == nil {
			t.Errorf("hubPath(%q) error = nil, want containment refused", id)
		}
	}
}

// planRefUpdates is the whole decision this record makes, so it is proven on
// its own: a ref moves forward or it does not move.
func TestPlanRefUpdatesMovesOnlyForward(t *testing.T) {
	mine := map[string]string{"heads/main": "aaaa", "heads/kept": "dddd", "tags/v1": "1111"}
	theirs := map[string]string{
		"heads/main":  "bbbb", // fast-forward
		"heads/kept":  "dddd", // identical
		"heads/new":   "eeee", // absent here
		"tags/v1":     "2222", // a tag that differs is a divergence
		"heads/apart": "3333",
	}
	ancestor := map[string]bool{"aaaa bbbb": true}
	plans, diverged, err := planRefUpdates(context.Background(), mine, theirs,
		func(_ context.Context, old, next string) (bool, error) {
			return ancestor[old+" "+next], nil
		})
	if err != nil {
		t.Fatalf("planRefUpdates() error = %v", err)
	}

	var moved []string
	for _, p := range plans {
		moved = append(moved, p.Ref)
	}
	if !slices.Equal(moved, []string{"heads/apart", "heads/main", "heads/new"}) {
		t.Errorf("planned = %v, want the fast-forward and the two new refs", moved)
	}
	if !slices.Equal(diverged, []string{"tags/v1"}) {
		t.Errorf("diverged = %v, want only the tag that differs", diverged)
	}
	for _, p := range plans {
		if p.Ref == "heads/new" && p.From != "" {
			t.Errorf("a ref that is not here yet reported a previous value: %#v", p)
		}
	}
}

// The host repository is the third source a checkout can be made from, after
// the remote on record and a carried bundle (ADR-0024, ADR-0029). It is what
// makes a local project openable on a box that has never held it.
func TestAddMaterializesALocalProjectFromTheHostRepository(t *testing.T) {
	g := localFake()
	g.pathExists = false
	g.isRepo = false
	host := newFakeHostGit()
	host.refs = map[string]string{"heads/main": "aaaa"}
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	report, err := m.Add(context.Background(), AddRequest{ID: testID, DisplayName: testName})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !report.Cloned || !report.Registered {
		t.Errorf("report = %#v, want a checkout made and the project registered", report)
	}
	if !slices.Contains(report.Notes, "attached_from_host") {
		t.Errorf("report.Notes = %v, want attached_from_host", report.Notes)
	}
	if !host.bundled {
		t.Errorf("the host repository was never bundled: %v", host.calls)
	}
	// What arrives is a repository with no origin, because the project has no
	// remote and the record says so.
	if g.origin != "" {
		t.Errorf("checkout origin = %q, want a local project to have none", g.origin)
	}
}

// With no host repository yet there is nothing on this machine to make the
// checkout from, and the refusal has to name every way of getting one.
func TestAddRefusesALocalProjectWithNothingToMakeItFrom(t *testing.T) {
	g := localFake()
	g.pathExists = false
	g.isRepo = false
	m := syncManager(t, g, registryWith(localProject()), newFakeHostGit())

	_, err := m.Add(context.Background(), AddRequest{ID: testID, DisplayName: testName})
	if err == nil {
		t.Fatal("Add() error = nil, want a refusal with no source to attach from")
	}
	assertKind(t, err, KindPrecondition)
	for _, want := range []string{"torio project sync", "--from-bundle", "set-remote"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
}

// A rerun on the box that holds the project verifies what is there and keeps
// it. Nothing is cloned over a working tree.
func TestAddAdoptsTheLocalCheckoutThatIsAlreadyThere(t *testing.T) {
	g := localFake()
	host := newFakeHostGit()
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	report, err := m.Add(context.Background(), AddRequest{ID: testID, DisplayName: testName})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !report.Adopted || report.Cloned {
		t.Errorf("report = %#v, want the existing checkout adopted", report)
	}
	if host.bundled {
		t.Error("a checkout that was already there was replaced from the host repository")
	}
}

// The side that has nothing to carry is the ordinary state of one direction of
// every reconciliation that moved something in the other. Reporting it as a
// divergence named a ref nobody had to resolve. Found by running a sync against
// a real box, where a commit made on the host came back and the same branch was
// reported as carried and as diverged in one run.
func TestPlanRefUpdatesTreatsABehindSideAsNothingToDo(t *testing.T) {
	mine := map[string]string{"heads/main": "bbbb"}
	theirs := map[string]string{"heads/main": "aaaa"}
	ancestor := map[string]bool{"aaaa bbbb": true}

	plans, diverged, err := planRefUpdates(context.Background(), mine, theirs,
		func(_ context.Context, old, next string) (bool, error) {
			return ancestor[old+" "+next], nil
		})
	if err != nil {
		t.Fatalf("planRefUpdates() error = %v", err)
	}
	if len(plans) != 0 {
		t.Errorf("planned = %#v, want nothing to write for a side that is behind", plans)
	}
	if len(diverged) != 0 {
		t.Errorf("diverged = %v, want a behind side reported as nothing at all", diverged)
	}
}

// A bare repository is initialized with HEAD naming a branch it has not got,
// and refs written by name never change that. A clone reads HEAD to decide what
// to check out, so the host repository has to name a branch it holds. Found by
// materializing a project on a real box: the checkout that arrived held no ref
// at all.
func TestSyncPointsTheHostRepositoryAtABranchItHolds(t *testing.T) {
	g := localFake()
	host := newFakeHostGit()
	host.mirror = map[string]string{"heads/main": "aaaa", "heads/topic": "cccc"}
	m := syncManager(t, g, registryWith(localProject()), host)

	if _, err := m.Sync(context.Background(), testID); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if host.headPointedAt != "refs/heads/main" {
		t.Errorf("host HEAD = %q, want the conventional default branch it holds", host.headPointedAt)
	}
}

// The bundle a checkout is materialized from carries HEAD, because that is what
// decides which branch the clone lands on.
func TestMaterializingBundlesTheHostRepositoryWithItsHEAD(t *testing.T) {
	g := localFake()
	g.pathExists = false
	g.isRepo = false
	host := newFakeHostGit()
	host.refs = map[string]string{"heads/main": "aaaa"}
	host.headResolves = true
	m := syncManager(t, g, registryWith(localProject()), host)
	existingHub(t, m)

	if _, err := m.Add(context.Background(), AddRequest{ID: testID, DisplayName: testName}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !host.saw("bundle create") || !host.saw("HEAD --branches --tags") {
		t.Errorf("host calls = %v, want a bundle carrying HEAD", host.calls)
	}
}
