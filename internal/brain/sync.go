package brain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

// The hub vault is the one copy every box agrees through (ADR-0025). It sits
// under the operator's data directory rather than the config root: it is
// content, not configuration, it is the thing an operator backs up, and a
// config directory that suddenly held a Markdown corpus would be a surprise to
// every tool that reads one.
const (
	hubDirName   = "torio"
	hubBrainDir  = "brain"
	hubVaultName = "vault"
	// hubBranch is the one branch both sides carry. A vault is a working set,
	// not a project with topic branches, and naming it here keeps the fetch
	// refspecs on both sides free of guesswork.
	hubBranch = "main"
	// syncCommitMessage is what an unsaved guest vault is committed under. It is
	// fixed: a message derived from the files would put vault content into a
	// place this package promises never to put it.
	syncCommitMessage = "torio brain sync"
	// syncCleanupTimeout bounds the staging removal that runs after the work is
	// done, including after a cancellation.
	syncCleanupTimeout = 30 * time.Second
)

// syncStagingPath is where the guest writes its bundle and reads the hub's. It
// is a directory of its own so the transport can carry it whole, and it lives
// under the identity's home for the same reason every other staging directory
// does: it belongs to the identity whose vault it holds.
func (m *Manager) syncStagingPath() string { return m.identity().Home + "/.torio-brain-sync-staging" }

// HubVault is the host path of the canonical vault.
func (m *Manager) HubVault() string {
	if m.hubRoot != "" {
		return filepath.Join(m.hubRoot, hubVaultName)
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, hubDirName, hubBrainDir, hubVaultName)
}

// SyncReport is what one reconciliation did, in counts. It carries no file
// name, no path inside either vault, and no line of any note: the transport
// invariant is that payload content never reaches stdout, logs or evidence, and
// a report is all three.
type SyncReport struct {
	// HubPath is where the operator resolves a conflict, and is the one path
	// worth naming: it is theirs, on their own machine.
	HubPath string
	// HubCreated reports that this run made the hub vault, which happens once.
	HubCreated bool
	// Snapshotted reports that the guest vault had unsaved work and was
	// committed before anything was carried.
	Snapshotted bool
	// ToHub and ToGuest are how many commits moved each way.
	ToHub   int
	ToGuest int
	// ConflictOutbound and ConflictInbound report a merge that could not be
	// made automatically. The direction is stopped and left exactly as it was.
	ConflictOutbound bool
	ConflictInbound  bool
	Notes            []string
}

// Conflicted reports whether either direction stopped on a merge.
func (r SyncReport) Conflicted() bool { return r.ConflictOutbound || r.ConflictInbound }

// Sync reconciles the bound guest's vault with the hub, both ways.
//
// The shape is deliberately the dullest one that works: each side writes a Git
// bundle of what it has, the transport carries the file, and the other side
// fetches from it and merges. A bundle is a file, so neither vault ends up with
// a network remote and the rule that one would be drift is untouched
// (ADR-0025).
//
// Outbound runs first. A vault the operator has just written into is the side
// with something to lose, so it is the side that is captured before anything
// else happens.
func (m *Manager) Sync(ctx context.Context) (report SyncReport, retErr error) {
	const op = "sync"
	report.HubPath = m.HubVault()
	if report.HubPath == "" {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("no host data directory could be resolved for the hub vault")}
	}

	if err := m.requireBootstrapVerified(ctx, op); err != nil {
		return report, err
	}
	status, err := m.inspectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	switch status.State {
	case StateInitialized:
	case StateUninitialized:
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("this guest has no Brain to reconcile; run `torio brain init` first")}
	default:
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("this guest's Brain has drift; run `torio brain status` and resolve it before reconciling")}
	}

	hostRoot, err := os.MkdirTemp("", "torio-brain-sync-")
	if err != nil {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host staging could not be created")}
	}
	defer func() {
		_ = os.RemoveAll(hostRoot)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncCleanupTimeout)
		defer cancel()
		_ = m.mustRun(cleanupCtx, op, KindGuestCommand, "clear the sync staging",
			guestexec.RootExec("rm", "-rf", "--", m.syncStagingPath()))
	}()

	snapshotted, err := m.snapshotGuestVault(ctx, op, status)
	if err != nil {
		return report, err
	}
	report.Snapshotted = snapshotted

	if err := m.syncOutbound(ctx, op, hostRoot, &report); err != nil {
		return report, err
	}
	if err := m.syncInbound(ctx, op, hostRoot, &report); err != nil {
		return report, err
	}
	return report, nil
}

// snapshotGuestVault commits whatever the agent left unsaved.
//
// A vault is edited by an agent that has no notion of committing, so "dirty" is
// its ordinary resting state rather than an unfinished operation. Carrying only
// committed work would mean carrying almost nothing.
func (m *Manager) snapshotGuestVault(ctx context.Context, op string, status StatusReport) (bool, error) {
	if status.GitState != GitDirty {
		return false, nil
	}
	if err := m.mustRun(ctx, op, KindGit, "stage the vault",
		m.vaultGit("add", "-A", "--", ".")); err != nil {
		return false, err
	}
	// The identity is supplied per invocation, exactly as the first commit
	// supplies it. The vault belongs to an agent that has no Git identity
	// configured, and a commit without one exits 128 rather than asking.
	if err := m.mustRun(ctx, op, KindGit, "commit the vault",
		m.vaultGit("-c", "user.name=torio", "-c", "user.email=torio@localhost",
			"commit", "-q", "-m", syncCommitMessage)); err != nil {
		return false, err
	}
	return true, nil
}

// syncOutbound carries the guest's history to the hub.
func (m *Manager) syncOutbound(ctx context.Context, op, hostRoot string, report *SyncReport) error {
	// The staging directory is shared between two identities, and that is what
	// decides its ownership. `limactl copy` reads and writes as the guest login
	// identity, while the bundles are written and read by the agent identity, so
	// the directory belongs to the transport and is group-writable to the shared
	// group both are in. Made 0700 and agent-owned, as it first was, the
	// transport cannot even traverse it, which is the failure this shape exists
	// to avoid: found by running a sync against a real box.
	transportUser, err := m.guestSessionUser(ctx, op)
	if err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "prepare the sync staging",
		guestexec.RootExec("install", "-d", "-o", transportUser, "-g", lima.TorioProjectsGroup, "-m", "2770",
			m.syncStagingPath())); err != nil {
		return err
	}
	guestBundle := m.syncStagingPath() + "/guest.bundle"
	if err := m.mustRun(ctx, op, KindGit, "bundle the vault",
		m.vaultGit("bundle", "create", guestBundle, hubBranch)); err != nil {
		return err
	}
	hostStaging := filepath.Join(hostRoot, "from-guest")
	if err := os.Mkdir(hostStaging, 0o700); err != nil {
		return &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host staging could not be created")}
	}
	if err := m.guest.CopyFromGuest(ctx, m.syncStagingPath(), hostStaging, m.identity().Home); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: fmt.Errorf("the vault could not be carried out of the guest")}
	}
	hostBundle := filepath.Join(hostStaging, "guest.bundle")
	if _, err := os.Stat(hostBundle); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: fmt.Errorf("the vault did not arrive on the host")}
	}
	// `git bundle verify` reads the repository it is run in, to decide whether
	// the prerequisites are satisfiable, so it can only run once there is a hub
	// to run it in. On the first sync there is not, and the clone below refuses
	// a bundle it cannot read anyway. Found by running the first sync against a
	// real box, where a verify outside a repository failed on every attempt.
	created, err := m.ensureHubVault(ctx, op, hostBundle)
	if err != nil {
		return err
	}
	report.HubCreated = created
	if created {
		// A hub made from this guest's own bundle already holds every commit
		// it had, so there is nothing left to merge in this direction.
		return nil
	}

	hub := m.HubVault()
	if err := m.mustRunHost(ctx, op, "verify the carried vault", hub, "bundle", "verify", hostBundle); err != nil {
		return err
	}
	if err := m.mustRunHost(ctx, op, "read the carried vault", hub,
		"fetch", "--quiet", hostBundle, hubBranch+":"+m.replicaRef()); err != nil {
		return err
	}
	ahead, err := m.countHost(ctx, op, hub, "rev-list", "--count", hubBranch+".."+m.replicaRef())
	if err != nil {
		return err
	}
	if ahead == 0 {
		return nil
	}
	merged, err := m.mergeHost(ctx, op, hub, m.replicaRef())
	if err != nil {
		return err
	}
	if !merged {
		report.ConflictOutbound = true
		report.Notes = append(report.Notes, "conflict_to_hub")
		return nil
	}
	report.ToHub = ahead
	return nil
}

// syncInbound carries the hub's history back into the guest.
func (m *Manager) syncInbound(ctx context.Context, op, hostRoot string, report *SyncReport) error {
	hub := m.HubVault()
	hostStaging := filepath.Join(hostRoot, "to-guest")
	if err := os.Mkdir(hostStaging, 0o700); err != nil {
		return &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host staging could not be created")}
	}
	hostBundle := filepath.Join(hostStaging, "hub.bundle")
	if err := m.mustRunHost(ctx, op, "bundle the hub vault", hub,
		"bundle", "create", hostBundle, hubBranch); err != nil {
		return err
	}
	if err := m.guest.CopyToGuest(ctx, hostStaging, m.syncStagingPath(), m.identity().Home); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: fmt.Errorf("the hub vault could not be carried into the guest")}
	}
	// The transport copies the host staging directory's own mode onto the guest
	// one, and the host side is private to the operator, so what arrives is a
	// directory the agent identity cannot open. The shared mode is put back
	// before anything reads through it. Found by running a sync against a real
	// box, where the guest could not open a directory it had written its own
	// bundle into moments earlier.
	if err := m.mustRun(ctx, op, KindGuestCommand, "restore the sync staging",
		guestexec.RootExec("chmod", "2770", m.syncStagingPath())); err != nil {
		return err
	}
	guestBundle := m.syncStagingPath() + "/hub.bundle"
	if err := m.mustRun(ctx, op, KindGit, "verify the carried hub vault",
		m.vaultGit("bundle", "verify", guestBundle)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "read the carried hub vault",
		m.vaultGit("fetch", "--quiet", guestBundle, hubBranch+":"+hubRef)); err != nil {
		return err
	}
	behind, err := m.countGuest(ctx, op, "rev-list", "--count", hubBranch+".."+hubRef)
	if err != nil {
		return err
	}
	if behind == 0 {
		return nil
	}
	merged, err := m.mergeGuest(ctx, op, hubRef)
	if err != nil {
		return err
	}
	if !merged {
		report.ConflictInbound = true
		report.Notes = append(report.Notes, "conflict_to_guest")
		return nil
	}
	report.ToGuest = behind
	return nil
}

// hubRef is where a guest keeps the last hub history it saw. It is a ref under
// refs/torio/, outside refs/heads and refs/remotes, so nothing about it looks
// like a configured remote to any tool that inspects the repository.
const hubRef = "refs/torio/hub"

// replicaRef is where the hub keeps the last history it saw from this instance.
// One ref per instance, because the hub reconciles with several.
func (m *Manager) replicaRef() string { return "refs/torio/" + m.bootstrapInstance() }

func (m *Manager) bootstrapInstance() string {
	name := m.identity().Name
	if name == "" {
		return "replica"
	}
	return name
}

// ensureHubVault makes the hub on the first sync, from the bundle that just
// arrived. Cloning from the bundle rather than initializing an empty repository
// is what gives the hub the guest's history instead of an unrelated root.
func (m *Manager) ensureHubVault(ctx context.Context, op, hostBundle string) (bool, error) {
	hub := m.HubVault()
	if _, err := os.Stat(filepath.Join(hub, ".git")); err == nil {
		return false, nil
	}
	// The directory is made here, private, and cloned into. Letting the clone
	// create it would leave it at whatever the process umask allows for as long
	// as the clone takes, and the vault is the operator's private notes.
	if err := os.MkdirAll(hub, 0o700); err != nil {
		return false, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("the host vault directory could not be created")}
	}
	if err := m.mustRunHost(ctx, op, "create the hub vault", "",
		"clone", "--quiet", "--branch", hubBranch, hostBundle, hub); err != nil {
		return false, err
	}
	// A clone from a bundle records the bundle's path as `origin`. The bundle is
	// a file in a staging directory that is about to be removed, and a vault
	// carrying a remote is drift by the rule this decision deliberately keeps,
	// so it goes.
	if err := m.mustRunHost(ctx, op, "detach the hub vault", hub, "remote", "remove", "origin"); err != nil {
		return false, err
	}
	return true, nil
}

// mergeHost and mergeGuest report whether the merge was made. A merge that
// cannot be made automatically is aborted, leaving that side exactly as it was,
// and is reported rather than raised: the other direction is still worth
// carrying, and the operator resolves this one with Git in the hub vault.
func (m *Manager) mergeHost(ctx context.Context, op, hub, ref string) (bool, error) {
	res, err := m.runHost(ctx, op, hub, "-c", "user.name=torio", "-c", "user.email=torio@localhost",
		"merge", "--no-edit", "--allow-unrelated-histories", ref)
	if err != nil {
		return false, err
	}
	if res.ExitCode == 0 {
		return true, nil
	}
	if err := m.mustRunHost(ctx, op, "abandon the merge", hub, "merge", "--abort"); err != nil {
		return false, err
	}
	return false, nil
}

func (m *Manager) mergeGuest(ctx context.Context, op, ref string) (bool, error) {
	res, err := m.run(ctx, op, m.vaultGit("-c", "user.name=torio", "-c", "user.email=torio@localhost",
		"merge", "--no-edit", "--allow-unrelated-histories", ref))
	if err != nil {
		return false, err
	}
	if res.ExitCode == 0 {
		return true, nil
	}
	if err := m.mustRun(ctx, op, KindGit, "abandon the merge", m.vaultGit("merge", "--abort")); err != nil {
		return false, err
	}
	return false, nil
}

// vaultGit runs Git in the guest vault as the identity that owns it.
func (m *Manager) vaultGit(args ...string) []string {
	return guestexec.UserExecAs(m.agentUser(), append([]string{"git", "-C", m.vault()}, args...)...)
}

func (m *Manager) countGuest(ctx context.Context, op string, args ...string) (int, error) {
	res, err := m.run(ctx, op, m.vaultGit(args...))
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, commandError(op, KindGit, "count commits", res.ExitCode)
	}
	return parseCount(op, res)
}

func (m *Manager) countHost(ctx context.Context, op, dir string, args ...string) (int, error) {
	res, err := m.runHost(ctx, op, dir, args...)
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, commandError(op, KindGit, "count commits", res.ExitCode)
	}
	return parseCount(op, res)
}

func parseCount(op string, res execx.Result) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if err != nil || n < 0 {
		return 0, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("a commit count could not be read")}
	}
	return n, nil
}

// runHost runs one Git command on the host, optionally inside dir.
func (m *Manager) runHost(ctx context.Context, op, dir string, args ...string) (execx.Result, error) {
	argv := []string{"git"}
	if dir != "" {
		argv = append(argv, "-C", dir)
	}
	argv = append(argv, args...)
	res, err := m.hostGit.Run(ctx, argv)
	if err != nil {
		return execx.Result{}, &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("git could not be run on the host")}
	}
	return res, nil
}

func (m *Manager) mustRunHost(ctx context.Context, op, action, dir string, args ...string) error {
	res, err := m.runHost(ctx, op, dir, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return commandError(op, KindGit, action, res.ExitCode)
	}
	return nil
}

// hostExecRunner is the production host boundary: a typed adapter around one
// external command, with the exit code captured and the output bounded, and no
// shell anywhere.
type hostExecRunner struct{}

func (hostExecRunner) Run(ctx context.Context, argv []string) (execx.Result, error) {
	runner := &execx.ExecRunner{}
	return runner.Run(ctx, execx.Command{Name: argv[0], Args: argv[1:]})
}

// describeHubDistance measures the bound replica against the last hub history
// it saw. It never fails the status: a box that has never reconciled has no ref
// to measure against, which is an answer rather than a fault.
func (m *Manager) describeHubDistance(ctx context.Context, op string, report *StatusReport) {
	known, err := m.run(ctx, op, m.vaultGit("rev-parse", "--verify", "--quiet", hubRef))
	if err != nil || known.ExitCode != 0 {
		return
	}
	report.HubRefKnown = true
	if ahead, err := m.countGuest(ctx, op, "rev-list", "--count", hubRef+".."+hubBranch); err == nil {
		report.AheadOfHub = ahead
	}
	if behind, err := m.countGuest(ctx, op, "rev-list", "--count", hubBranch+".."+hubRef); err == nil {
		report.BehindHub = behind
	}
}
