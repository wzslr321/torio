package brain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// fakeHostGit records the Git commands run against the hub vault and answers
// them from a script, so the reconciliation can be driven whole without a Git
// binary or a directory to write in.
type fakeHostGit struct {
	calls []string
	// counts answers `rev-list --count` in order.
	counts []string
	// conflictOn makes the merge whose argv contains this string exit non-zero,
	// which is how Git reports a merge it could not make.
	conflictOn string
	// missing makes `bundle verify` fail, standing for a file that did not
	// arrive or did not survive the trip.
	verifyFails bool
}

func (f *fakeHostGit) Run(_ context.Context, argv []string) (execx.Result, error) {
	joined := strings.Join(argv, " ")
	f.calls = append(f.calls, joined)
	switch {
	case strings.Contains(joined, "bundle verify"):
		if f.verifyFails {
			return execx.Result{ExitCode: 1}, nil
		}
	case strings.Contains(joined, "rev-list --count"):
		out := "0"
		if len(f.counts) > 0 {
			out, f.counts = f.counts[0], f.counts[1:]
		}
		return execx.Result{ExitCode: 0, Stdout: []byte(out + "\n")}, nil
	case f.conflictOn != "" && strings.Contains(joined, "merge") && strings.Contains(joined, f.conflictOn):
		return execx.Result{ExitCode: 1}, nil
	}
	return execx.Result{ExitCode: 0}, nil
}

func (f *fakeHostGit) saw(needle string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

// syncManager wires a manager whose hub vault is a temporary directory and
// whose host Git is scripted.
func syncManager(t *testing.T, g *fakeGuest, host *fakeHostGit) *Manager {
	t.Helper()
	g.carriedBundle = "guest.bundle"
	m := New(g, lima.BootstrapOptions{OperatorUser: "operator"})
	m.hostGit = host
	m.hubRoot = t.TempDir()
	return m
}

// The first sync has no hub to merge into, so it makes one from the bundle the
// guest just wrote. Cloning from that bundle rather than initializing an empty
// repository is what gives the hub the guest's history instead of an unrelated
// root (ADR-0025).
func TestSyncCreatesTheHubFromTheFirstGuestItSees(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{}
	m := syncManager(t, g, host)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if !report.HubCreated {
		t.Errorf("report = %#v, want the hub reported as created", report)
	}
	if !host.saw("clone --quiet --branch main") {
		t.Errorf("the hub was not cloned from the carried bundle: %v", host.calls)
	}
	// A clone from a bundle records the bundle path as origin, and a vault
	// carrying a remote is drift by the rule this decision keeps.
	if !host.saw("remote remove origin") {
		t.Errorf("the hub kept a remote pointing at the bundle: %v", host.calls)
	}
	if report.HubPath != filepath.Join(m.hubRoot, "vault") {
		t.Errorf("hub path = %q, want the resolved vault", report.HubPath)
	}
}

// A vault is edited by an agent that never commits, so unsaved work is its
// ordinary resting state. Carrying only committed work would carry almost
// nothing.
func TestSyncCommitsUnsavedGuestWorkBeforeCarryingIt(t *testing.T) {
	g := initializedFake()
	g.gitDirty = true
	host := &fakeHostGit{}
	m := syncManager(t, g, host)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if !report.Snapshotted {
		t.Error("unsaved work was not committed before the vault was carried")
	}
	if !g.saw("commit -q -m " + syncCommitMessage) {
		t.Errorf("no commit was made in the guest vault: %v", g.calls)
	}
	// The message is fixed. One derived from the files would put vault content
	// into an argv, which is the one place this package promises not to put it.
	if !g.saw(syncCommitMessage) {
		t.Errorf("the snapshot did not use the fixed message: %v", g.calls)
	}
}

// A clean vault has nothing to capture, so nothing is committed.
func TestSyncCommitsNothingWhenTheGuestVaultIsClean(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{}
	m := syncManager(t, g, host)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if report.Snapshotted {
		t.Error("a clean vault was committed anyway")
	}
	if g.saw("commit -q -m " + syncCommitMessage) {
		t.Errorf("a clean vault was committed: %v", g.calls)
	}
}

// With a hub already there, both directions carry, and the counts are what the
// operator is told: how much moved, never what moved.
func TestSyncCarriesBothDirectionsAgainstAnExistingHub(t *testing.T) {
	g := initializedFake()
	g.behindHub = "5"
	host := &fakeHostGit{counts: []string{"2"}}
	m := syncManager(t, g, host)
	mustExistingHub(t, m)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if report.HubCreated {
		t.Error("an existing hub was reported as created")
	}
	if report.ToHub != 2 {
		t.Errorf("commits to the hub = %d, want 2", report.ToHub)
	}
	if report.ToGuest != 5 {
		t.Errorf("commits to the guest = %d, want 5", report.ToGuest)
	}
	if !host.saw("fetch --quiet") {
		t.Errorf("the hub never read the carried bundle: %v", host.calls)
	}
	if !g.saw("fetch --quiet") {
		t.Errorf("the guest never read the carried hub bundle: %v", g.calls)
	}
	// The refs both sides keep live outside refs/heads and refs/remotes, so
	// nothing about them reads as a configured remote.
	if !host.saw("refs/torio/") || !g.saw(hubRef) {
		t.Errorf("the exchange did not use the torio refs: host=%v", host.calls)
	}
}

// Nothing to carry is the common case once two boxes agree, and it must not
// merge, commit or report movement.
func TestSyncCarriesNothingWhenBothSidesAgree(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{counts: []string{"0", "0"}}
	m := syncManager(t, g, host)
	mustExistingHub(t, m)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if report.ToHub != 0 || report.ToGuest != 0 || report.Conflicted() {
		t.Errorf("report = %#v, want nothing carried and no conflict", report)
	}
	if host.saw("merge --no-edit") {
		t.Errorf("a merge ran with nothing to merge: %v", host.calls)
	}
}

// A merge that cannot be made automatically stops that direction and leaves it
// exactly as it was. It is reported rather than raised: the other direction is
// still worth carrying, and the operator resolves this one with Git.
func TestSyncStopsOneDirectionOnAConflictAndLeavesItUntouched(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{counts: []string{"3"}, conflictOn: "refs/torio/"}
	m := syncManager(t, g, host)
	mustExistingHub(t, m)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() reported a conflict as a failure: %v", err)
	}

	if !report.ConflictOutbound {
		t.Errorf("report = %#v, want the outbound conflict reported", report)
	}
	if !report.Conflicted() {
		t.Error("Conflicted() = false with a conflicting direction")
	}
	if report.ToHub != 0 {
		t.Errorf("commits to the hub = %d, want none carried through a conflict", report.ToHub)
	}
	if !host.saw("merge --abort") {
		t.Errorf("the conflicting merge was left in place: %v", host.calls)
	}
	// The hub path is the one path worth naming: it is where the operator
	// resolves this, on their own machine.
	if report.HubPath == "" {
		t.Error("the report does not say where the conflict is resolved")
	}
}

// A guest with no Brain has nothing to reconcile, and a drifted one must be
// understood before anything is carried into or out of it.
func TestSyncRefusesAGuestThatIsNotReady(t *testing.T) {
	for _, tc := range []struct {
		name  string
		guest func() *fakeGuest
		want  string
	}{
		{"uninitialized", readyFake, "brain init"},
		{"drift", func() *fakeGuest {
			f := initializedFake()
			f.scaffold = false
			return f
		}, "drift"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &fakeHostGit{}
			m := syncManager(t, tc.guest(), host)

			_, err := m.Sync(context.Background())
			if err == nil {
				t.Fatal("Sync() ran against a guest that is not ready")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err.Error(), tc.want)
			}
			if len(host.calls) != 0 {
				t.Errorf("the hub was touched anyway: %v", host.calls)
			}
		})
	}
}

// A bundle that did not survive the trip is a transport failure, not a vault
// to merge from.
func TestSyncRefusesABundleThatDoesNotVerify(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{verifyFails: true}
	m := syncManager(t, g, host)
	mustExistingHub(t, m)

	if _, err := m.Sync(context.Background()); err == nil {
		t.Fatal("Sync() accepted a bundle that did not verify")
	}
	if host.saw("fetch --quiet") {
		t.Errorf("an unverified bundle was read into the hub: %v", host.calls)
	}
}

// `git bundle verify` reads the repository it runs in, so it cannot run before
// there is a hub to run it in. The first sync therefore verifies nothing on the
// host and lets the clone refuse a bundle it cannot read, which is what a real
// first sync proved: a verify outside a repository fails every time.
func TestFirstSyncDoesNotVerifyOutsideARepository(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{}
	m := syncManager(t, g, host)

	if _, err := m.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	for _, call := range host.calls {
		if strings.Contains(call, "bundle verify") && !strings.Contains(call, "-C ") {
			t.Errorf("a bundle was verified outside a repository: %q", call)
		}
	}
}

// The report is counts and one host path. No vault content, no file name from
// inside either vault, ever reaches it: a report is stdout, logs and evidence
// all at once, and the transport invariant covers all three.
func TestSyncReportNamesNothingFromInsideAVault(t *testing.T) {
	g := initializedFake()
	g.behindHub = "1"
	host := &fakeHostGit{counts: []string{"2"}}
	m := syncManager(t, g, host)
	mustExistingHub(t, m)

	report, err := m.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	for _, note := range report.Notes {
		if strings.Contains(note, Path) || strings.Contains(note, ".md") {
			t.Errorf("a note names something from inside a vault: %q", note)
		}
	}
	if strings.Contains(report.HubPath, ".md") {
		t.Errorf("the hub path names a file: %q", report.HubPath)
	}
}

// mustExistingHub puts a hub vault where the manager will look for one, so a
// test can exercise the merge path rather than the creation path.
func mustExistingHub(t *testing.T, m *Manager) {
	t.Helper()
	git := filepath.Join(m.HubVault(), ".git")
	if err := os.MkdirAll(git, 0o700); err != nil {
		t.Fatalf("staging a hub vault failed: %v", err)
	}
}

// A replica that has moved since the last reconciliation is a fact about this
// box, not drift in it: the vault is healthy, it is simply not level with the
// rest yet. Saying so is what makes "sync is an operator action" honest
// (ADR-0025).
func TestStatusReportsHowFarTheReplicaIsFromTheHub(t *testing.T) {
	g := initializedFake()
	g.aheadOfHub = "4"
	g.behindHub = "1"
	g.hubRefKnown = true
	m := syncManager(t, g, &fakeHostGit{})

	report, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if !report.HubRefKnown {
		t.Fatal("a replica that has reconciled before was reported as never having done so")
	}
	if report.AheadOfHub != 4 || report.BehindHub != 1 {
		t.Errorf("ahead=%d behind=%d, want 4 and 1", report.AheadOfHub, report.BehindHub)
	}
	// Being out of step is not drift. A vault that is healthy and simply not
	// level must not be reported as broken, or every sync would look like a
	// repair.
	if report.State != StateInitialized {
		t.Errorf("state = %q, want %q", report.State, StateInitialized)
	}
}

// A box that has never reconciled has no hub ref to measure against, and says
// that rather than reporting zeroes that would read as agreement.
func TestStatusSaysWhenAReplicaHasNeverReconciled(t *testing.T) {
	g := initializedFake()
	m := syncManager(t, g, &fakeHostGit{})

	report, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if report.HubRefKnown {
		t.Error("a box that never reconciled was reported as having a hub to compare with")
	}
	if report.AheadOfHub != 0 || report.BehindHub != 0 {
		t.Errorf("ahead=%d behind=%d, want both zero when there is nothing to compare with", report.AheadOfHub, report.BehindHub)
	}
}

// The transport writes the host staging directory's own mode onto the guest
// one. The host side is private to the operator, so what lands is a directory
// the agent identity cannot open, and the next thing to run is the agent
// reading the bundle through it. The shared mode has to be put back first: a
// real sync failed here with the guest unable to open a directory it had
// written its own bundle into moments earlier.
func TestSyncRestoresTheSharedStagingModeAfterCarryingIn(t *testing.T) {
	g := initializedFake()
	host := &fakeHostGit{counts: []string{"0"}}
	m := syncManager(t, g, host)
	mustExistingHub(t, m)

	if _, err := m.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !g.syncStagingShared {
		t.Errorf("the staging directory was left at the host's private mode: %v", g.calls)
	}
}
