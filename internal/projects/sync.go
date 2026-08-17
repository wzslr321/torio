package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

// A project with no remote meets its other boxes in a bare repository on the
// host (ADR-0029). It sits beside the Brain's hub vault, under the operator's
// data directory rather than the config root, for the same reason that one
// does: it is content, it is the thing an operator backs up, and a config
// directory holding repository history would surprise every tool that reads
// one.
const (
	hubDataDirName     = "torio"
	hubProjectsDirName = "projects"
	// hubRepoSuffix marks the host copy as bare, which it is. A working tree on
	// the host could hold uncommitted state of its own, and then the side that
	// exists to be neutral would be a side that can conflict.
	hubRepoSuffix = ".git"
	// syncStagingDirName is where the guest writes its bundle and reads the
	// host's. It is a directory of its own so the transport can carry it whole,
	// and it lives under the identity's home because it holds that identity's
	// repository.
	syncStagingDirName = ".torio-project-sync-staging"
	guestBundleName    = "guest.bundle"
	hubBundleName      = "hub.bundle"
	// hubMirrorRef is where a guest keeps the last host history it saw. It is
	// under refs/torio/, outside refs/heads, refs/tags and refs/remotes, so
	// nothing about it looks like a configured remote to any tool that inspects
	// the repository.
	hubMirrorRef = "refs/torio/hub"
	// syncCleanupTimeout bounds the staging removal that runs after the work is
	// done, including after a cancellation.
	syncCleanupTimeout = 30 * time.Second
	// refListFormat is one argv token. `for-each-ref` is asked for the two
	// fields the decision needs and nothing else: no subject, no author, no
	// message, so no repository content reaches this package.
	refListFormat = "--format=%(refname) %(objectname)"
)

// HostRunner is the typed host-command boundary. Only Git runs through it, only
// against a host repository this package derived, and its output is read for
// refs and counts rather than for content.
type HostRunner interface {
	Run(ctx context.Context, argv []string) (execx.Result, error)
}

// hostExecRunner is the production host boundary: one external command through
// the typed runner, with the exit code captured and the output bounded, and no
// shell anywhere.
type hostExecRunner struct{}

func (hostExecRunner) Run(ctx context.Context, argv []string) (execx.Result, error) {
	runner := &execx.ExecRunner{}
	return runner.Run(ctx, execx.Command{Name: argv[0], Args: argv[1:]})
}

func (m *Manager) syncStagingPath() string { return m.identity().Home + "/" + syncStagingDirName }

// hubMirrorNamespace is where the host repository keeps the last history it saw
// from this box. One namespace per backend, because the host repository
// reconciles with several.
func (m *Manager) hubMirrorNamespace() string {
	name := m.identity().Name
	if name == "" {
		name = "replica"
	}
	return "refs/torio/" + name
}

// hubRootPath is the directory holding one bare repository per local project.
func (m *Manager) hubRootPath() (string, error) {
	if m.hubRoot != "" {
		return m.hubRoot, nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("no host data directory could be resolved for local projects")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, hubDataDirName, hubProjectsDirName), nil
}

// hubPath derives the host repository for one project id, and refuses anything
// that would not land exactly one level under the root.
//
// The derivation is the point. No host path is ever recorded, so the registry
// keeps meaning the same thing on every machine (ADR-0023); the path is worked
// out from the id on the machine that needs it, the way a workspace path is.
func (m *Manager) hubPath(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return "", errInvalidID
	}
	root, err := m.hubRootPath()
	if err != nil {
		return "", err
	}
	name := id + hubRepoSuffix
	p := filepath.Join(root, name)
	if filepath.Dir(p) != root || filepath.Base(p) != name {
		return "", errInvalidID
	}
	return p, nil
}

// hubExists reports whether a host repository has been made. A bare repository
// always has a HEAD, and asking for the one file it cannot be without beats
// treating any directory of that name as one.
func hubExists(hub string) bool {
	_, err := os.Stat(filepath.Join(hub, "HEAD"))
	return err == nil
}

// Sync reconciles a local project's checkout with the host repository, both
// ways (ADR-0029).
//
// The shape is the Brain's, because the transport problem is the same: each
// side writes a Git bundle, the one-shot copy carries the file, and the other
// side reads refs out of it. What is deliberately not the Brain's is what
// happens next. A vault is one corpus and can be merged; a project is a working
// tree whose branches diverge because somebody decided they should, so a ref
// moves only where the value the other side holds is an ancestor of the one
// arriving, and everything else is named and left alone.
func (m *Manager) Sync(ctx context.Context, id string) (report SyncReport, retErr error) {
	const op = "sync"

	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return report, err
	}
	report.Project = view(entry, workspace)
	if entry.Remote != "" {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf(
			"project %q has a remote on record, so its boxes already meet there; "+
				"fetch and push through the remote instead", id)}
	}
	hub, err := m.hubPath(id)
	if err != nil {
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	report.HubPath = hub

	if err := m.requirePrepared(ctx, op); err != nil {
		return report, err
	}
	checkout, err := m.inspectCheckout(ctx, op, workspace, entry.Remote)
	if err != nil {
		return report, err
	}
	switch {
	case !checkout.PathExists || checkout.Symlink || !checkout.Directory || !checkout.Repository:
		return report, &Error{Op: op, Kind: KindPrecondition, Issues: checkout.issues(), Err: fmt.Errorf(
			"there is no checkout of %q on this box to reconcile; make it with `torio project add %s`", id, id)}
	case checkout.HasOrigin:
		// The record says the project is local and the tree says it is not.
		// Carrying history for a tree that disagrees with the record would be
		// reconciling two different claims about what this project is.
		return report, &Error{Op: op, Kind: KindConflict, Issues: checkout.issues(), Err: fmt.Errorf(
			"the checkout of %q has an origin the record does not, so it is not the local project on record", id)}
	}

	hostRoot, err := os.MkdirTemp("", "torio-project-sync-")
	if err != nil {
		return report, &Error{Op: op, Kind: KindTransport, Err: errors.New("private host staging could not be created")}
	}
	defer func() {
		_ = os.RemoveAll(hostRoot)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncCleanupTimeout)
		defer cancel()
		_, _ = m.run(cleanupCtx, op, guestexec.RootExec("rm", "-rf", "--", m.syncStagingPath()))
	}()
	if err := m.prepareSyncStaging(ctx, op); err != nil {
		return report, err
	}

	history, err := m.guestHasHistory(ctx, op, workspace)
	if err != nil {
		return report, err
	}
	if history {
		if err := m.syncOutbound(ctx, op, hostRoot, workspace, hub, &report); err != nil {
			return report, err
		}
	} else {
		// A project made with --local has no commit until somebody makes one,
		// and `git bundle create` refuses to write an empty bundle. There is
		// nothing to carry and nothing to make a host repository out of.
		report.Notes = append(report.Notes, "no_history_yet")
	}
	if report.HubCreated {
		// A host repository made from this box's own bundle already holds every
		// ref this box had, so there is nothing to carry back and no reason to
		// copy a whole repository in to prove it.
		return report, nil
	}
	if !hubExists(hub) {
		return report, nil
	}
	return report, m.syncInbound(ctx, op, hostRoot, workspace, hub, &report)
}

// prepareSyncStaging makes the guest directory the transport can write into.
//
// `limactl copy` runs as the guest login identity and never as the agent, so
// staging owned by the agent at 0700 cannot receive anything. The directory
// belongs to the transport, with the shared group set so the agent can read and
// write what lands there (ADR-0025).
func (m *Manager) prepareSyncStaging(ctx context.Context, op string) error {
	staging := m.syncStagingPath()
	if err := m.mustRun(ctx, op, KindGuestCommand, "clear the sync staging",
		guestexec.RootExec("rm", "-rf", "--", staging)); err != nil {
		return err
	}
	transportUser, err := m.guestSessionUser(ctx, op)
	if err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "create the sync staging",
		guestexec.RootExec("install", "-d", "-o", transportUser, "-g", lima.TorioProjectsGroup, "-m", "2770", staging))
}

// guestHasHistory reports whether the checkout holds any ref at all.
func (m *Manager) guestHasHistory(ctx context.Context, op, workspace string) (bool, error) {
	res, err := m.run(ctx, op, m.workspaceGit(workspace, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads", "refs/tags"))
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		return false, commandError(op, KindGit, "list the checkout's refs", res.ExitCode)
	}
	return len(strings.TrimSpace(string(res.Stdout))) > 0, nil
}

// syncOutbound carries the guest's refs to the host repository.
func (m *Manager) syncOutbound(ctx context.Context, op, hostRoot, workspace, hub string, report *SyncReport) error {
	staging := m.syncStagingPath()
	if err := m.mustRun(ctx, op, KindGit, "bundle the checkout",
		m.workspaceGit(workspace, "bundle", "create", staging+"/"+guestBundleName, "--branches", "--tags")); err != nil {
		return err
	}
	hostStaging := filepath.Join(hostRoot, "from-guest")
	if err := os.Mkdir(hostStaging, 0o700); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: errors.New("private host staging could not be created")}
	}
	if err := m.guest.CopyFromGuest(ctx, staging, hostStaging, m.identity().Home); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: errors.New("the checkout's history could not be carried out of the guest")}
	}
	hostBundle := filepath.Join(hostStaging, guestBundleName)
	if _, err := os.Stat(hostBundle); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: errors.New("the checkout's history did not arrive on the host")}
	}

	created, err := m.ensureHubRepository(ctx, op, hub)
	if err != nil {
		return err
	}
	report.HubCreated = created

	mirror := m.hubMirrorNamespace()
	// Forced, into a namespace of this box's own, and pruned: what the mirror
	// holds is a record of what this box last carried, not a second opinion
	// about what the host repository should contain. The decision below is what
	// writes anything a reader of the host repository would see.
	if err := m.mustRunHost(ctx, op, "read the carried history", hub,
		"fetch", "--quiet", "--prune", "--no-tags", hostBundle,
		"+refs/heads/*:"+mirror+"/heads/*", "+refs/tags/*:"+mirror+"/tags/*"); err != nil {
		return err
	}
	ours, err := m.listHostRefs(ctx, op, hub, "refs/", "refs/heads", "refs/tags")
	if err != nil {
		return err
	}
	theirs, err := m.listHostRefs(ctx, op, hub, mirror+"/", mirror)
	if err != nil {
		return err
	}
	plans, diverged, err := planRefUpdates(ctx, ours, theirs, func(ctx context.Context, old, next string) (bool, error) {
		return m.hostAncestor(ctx, op, hub, old, next)
	})
	if err != nil {
		return err
	}
	report.Diverged = appendNames(report.Diverged, diverged...)
	for _, plan := range plans {
		if err := m.mustRunHost(ctx, op, "write a ref in the host repository", hub,
			"update-ref", "refs/"+plan.Ref, plan.Target); err != nil {
			return err
		}
		n, err := m.countHost(ctx, op, hub, plan.commitRange())
		if err != nil {
			return err
		}
		report.ToHub = append(report.ToHub, RefMove{Ref: plan.Ref, Commits: n})
	}
	return m.ensureHubHead(ctx, op, hub)
}

// ensureHubHead points the host repository at a branch it actually has.
//
// A bare repository is initialized with HEAD naming a branch that does not
// exist yet, and the refs above are written by name, so nothing else ever makes
// HEAD resolve. It has to: a clone reads HEAD to decide which branch to check
// out, and from a bundle carrying none it checks out nothing at all and creates
// no local branch. Found by materializing a project on a real box, where the
// arriving checkout held no ref whatsoever.
func (m *Manager) ensureHubHead(ctx context.Context, op, hub string) error {
	res, err := m.runHost(ctx, op, hub, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	refs, err := m.listHostRefs(ctx, op, hub, "refs/", "refs/heads")
	if err != nil {
		return err
	}
	branch := preferredBranch(refs)
	if branch == "" {
		return nil
	}
	return m.mustRunHost(ctx, op, "point the host repository at a branch", hub,
		"symbolic-ref", "HEAD", "refs/"+branch)
}

// preferredBranch picks the branch a clone should land on. The conventional
// default names are tried first and the rest is alphabetical, because the
// alternative is a repository whose HEAD depends on which ref happened to be
// written last.
func preferredBranch(refs map[string]string) string {
	for _, want := range []string{"heads/main", "heads/master"} {
		if _, ok := refs[want]; ok {
			return want
		}
	}
	for _, name := range sortedRefNames(refs) {
		if strings.HasPrefix(name, "heads/") {
			return name
		}
	}
	return ""
}

// syncInbound carries the host repository's refs back into the guest.
func (m *Manager) syncInbound(ctx context.Context, op, hostRoot, workspace, hub string, report *SyncReport) error {
	hostRefs, err := m.listHostRefs(ctx, op, hub, "refs/", "refs/heads", "refs/tags")
	if err != nil {
		return err
	}
	if len(hostRefs) == 0 {
		return nil
	}
	hostStaging := filepath.Join(hostRoot, "to-guest")
	if err := os.Mkdir(hostStaging, 0o700); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: errors.New("private host staging could not be created")}
	}
	hostBundle := filepath.Join(hostStaging, hubBundleName)
	if err := m.mustRunHost(ctx, op, "bundle the host repository", hub,
		"bundle", "create", hostBundle, "--branches", "--tags"); err != nil {
		return err
	}
	staging := m.syncStagingPath()
	if err := m.guest.CopyToGuest(ctx, hostStaging, staging, m.identity().Home); err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: errors.New("the host repository could not be carried into the guest")}
	}
	// The transport copies the host staging directory's own mode onto the guest
	// one, and the host side is private to the operator, so what arrives is a
	// directory the agent identity cannot open. The shared mode is put back
	// before anything reads through it (ADR-0025).
	if err := m.mustRun(ctx, op, KindGuestCommand, "restore the sync staging",
		guestexec.RootExec("chmod", "2770", staging)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "read the carried host repository",
		m.workspaceGit(workspace, "fetch", "--quiet", "--prune", "--no-tags", staging+"/"+hubBundleName,
			"+refs/heads/*:"+hubMirrorRef+"/heads/*", "+refs/tags/*:"+hubMirrorRef+"/tags/*")); err != nil {
		return err
	}

	mine, err := m.listGuestRefs(ctx, op, workspace, "refs/", "refs/heads", "refs/tags")
	if err != nil {
		return err
	}
	theirs, err := m.listGuestRefs(ctx, op, workspace, hubMirrorRef+"/", hubMirrorRef)
	if err != nil {
		return err
	}
	plans, diverged, err := planRefUpdates(ctx, mine, theirs, func(ctx context.Context, old, next string) (bool, error) {
		return m.guestAncestor(ctx, op, workspace, old, next)
	})
	if err != nil {
		return err
	}
	report.Diverged = appendNames(report.Diverged, diverged...)

	branch, err := m.checkedOutBranch(ctx, op, workspace)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		switch {
		case branch != "" && plan.Ref == "heads/"+branch:
			// A ref write would leave the index and the tree at the old commit,
			// so the branch the checkout stands on moves through the worktree.
			//
			// Whether it may move is Git's own answer rather than a rule
			// reimplemented here: `merge --ff-only` writes the tree only where
			// nothing in it would be written over, and refuses otherwise. A
			// refusal is the ref being held back, named so the operator can
			// commit or stash and run the same command again. Nothing is
			// forced, and nothing is stashed on their behalf.
			moved, err := m.fastForwardCheckout(ctx, op, workspace, plan.Ref)
			if err != nil {
				return err
			}
			if !moved {
				report.HeldBack = appendNames(report.HeldBack, plan.Ref)
				continue
			}
		default:
			if err := m.mustRun(ctx, op, KindGit, "write a ref in the checkout",
				m.workspaceGit(workspace, "update-ref", "refs/"+plan.Ref, plan.Target)); err != nil {
				return err
			}
		}
		n, err := m.countGuest(ctx, op, workspace, plan.commitRange())
		if err != nil {
			return err
		}
		report.ToGuest = append(report.ToGuest, RefMove{Ref: plan.Ref, Commits: n})
	}
	return nil
}

// fastForwardCheckout moves the branch the checkout stands on, and reports
// whether it moved. A merge that would write over work in the tree exits
// non-zero, which is an outcome rather than a fault: the other refs are still
// worth carrying, and the operator settles this one.
func (m *Manager) fastForwardCheckout(ctx context.Context, op, workspace, ref string) (bool, error) {
	res, err := m.run(ctx, op, m.workspaceGit(workspace, "merge", "--ff-only", hubMirrorRef+"/"+ref))
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// ensureHubRepository makes the host repository on the first sync.
//
// It is initialized empty rather than cloned from the bundle that just arrived,
// because the refs are written by the decision below and not by a transport:
// one place decides what the host repository holds, and it is the same place on
// the first sync as on every later one.
func (m *Manager) ensureHubRepository(ctx context.Context, op, hub string) (bool, error) {
	if hubExists(hub) {
		return false, nil
	}
	// Made here, private, and initialized into. Letting Git create it would
	// leave it at whatever the process umask allows for as long as the init
	// takes, and it holds the operator's own repository.
	if err := os.MkdirAll(hub, 0o700); err != nil {
		return false, &Error{Op: op, Kind: KindPrecondition, Err: errors.New("the host repository directory could not be created")}
	}
	if err := m.mustRunHost(ctx, op, "create the host repository", "",
		"init", "--bare", "--quiet", "--initial-branch=main", hub); err != nil {
		return false, err
	}
	return true, nil
}

// refPlan is one ref that may move forward, with what it is moving from so the
// report can say by how much.
type refPlan struct {
	// Ref is the name without the leading refs/, as the report keys it.
	Ref string
	// Target is where the ref is going, From where it is now. From is empty for
	// a ref this side has not got.
	Target string
	From   string
}

// commitRange is what `rev-list --count` is asked for: the distance covered, or
// the whole history of a ref arriving for the first time.
func (p refPlan) commitRange() string {
	if p.From == "" {
		return p.Target
	}
	return p.From + ".." + p.Target
}

// planRefUpdates decides what moves. It is the whole of ADR-0029's rule, and it
// is pure: the ancestry question is asked through a function so the decision can
// be proven without a repository.
//
// A ref moves when this side has not got it, or when what this side holds is an
// ancestor of what is arriving. A ref that is already equal is not a move. Every
// other case, a branch that grew on both sides or a tag pointing somewhere else,
// is a divergence: it is named and nothing is written.
func planRefUpdates(ctx context.Context, mine, theirs map[string]string,
	isAncestor func(ctx context.Context, old, next string) (bool, error)) ([]refPlan, []string, error) {
	var plans []refPlan
	var diverged []string
	for _, ref := range sortedRefNames(theirs) {
		target := theirs[ref]
		current, held := mine[ref]
		switch {
		case !held:
			plans = append(plans, refPlan{Ref: ref, Target: target})
		case current == target:
		default:
			forward, err := isAncestor(ctx, current, target)
			if err != nil {
				return nil, nil, err
			}
			if forward {
				plans = append(plans, refPlan{Ref: ref, Target: target, From: current})
				continue
			}
			// Not a fast-forward is not yet a divergence. The other side may
			// simply be behind, which is the ordinary state of the direction
			// that has nothing to carry: this side already contains what it
			// holds, and the other direction of the same reconciliation is
			// what moves it. Reporting that as diverged named a ref nobody had
			// to resolve, on every sync that carried anything back. Found by
			// running a reconciliation against a real box.
			behind, err := isAncestor(ctx, target, current)
			if err != nil {
				return nil, nil, err
			}
			if behind {
				continue
			}
			diverged = append(diverged, ref)
		}
	}
	return plans, diverged, nil
}

func sortedRefNames(refs map[string]string) []string {
	out := make([]string, 0, len(refs))
	for name := range refs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// appendNames adds names that are not there yet, keeping the result sorted so a
// report reads the same way whichever direction found a ref first.
func appendNames(existing []string, names ...string) []string {
	for _, name := range names {
		if !slices.Contains(existing, name) {
			existing = append(existing, name)
		}
	}
	slices.Sort(existing)
	return existing
}

// workspaceGit runs Git in the checkout as the identity that owns it.
func (m *Manager) workspaceGit(workspace string, args ...string) []string {
	return guestexec.UserExecAs(m.identity().GuestUser, append([]string{"git", "-C", workspace}, args...)...)
}

// checkedOutBranch names the branch the checkout stands on, empty on a detached
// HEAD. A detached HEAD is not a failure: it is a checkout with no branch to
// hold back or fast-forward.
func (m *Manager) checkedOutBranch(ctx context.Context, op, workspace string) (string, error) {
	res, err := m.run(ctx, op, m.workspaceGit(workspace, "symbolic-ref", "--short", "HEAD"))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (m *Manager) listGuestRefs(ctx context.Context, op, workspace, prefix string, patterns ...string) (map[string]string, error) {
	res, err := m.run(ctx, op, m.workspaceGit(workspace, append([]string{"for-each-ref", refListFormat}, patterns...)...))
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, commandError(op, KindGit, "list the checkout's refs", res.ExitCode)
	}
	return parseRefs(op, res.Stdout, prefix)
}

func (m *Manager) listHostRefs(ctx context.Context, op, hub, prefix string, patterns ...string) (map[string]string, error) {
	res, err := m.runHost(ctx, op, hub, append([]string{"for-each-ref", refListFormat}, patterns...)...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, commandError(op, KindGit, "list the host repository's refs", res.ExitCode)
	}
	return parseRefs(op, res.Stdout, prefix)
}

// parseRefs reads `for-each-ref` output into ref name and object id.
//
// A line that is not those two fields is not interpreted: the output is Git's
// own and bounded, and a permissive parser here is how a ref name carrying a
// space would become an argv element later.
func parseRefs(op string, out []byte, prefix string) (map[string]string, error) {
	refs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, object, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(name, prefix) || !isObjectID(object) {
			return nil, &Error{Op: op, Kind: KindVerification, Err: errors.New("a ref could not be read")}
		}
		refs[strings.TrimPrefix(name, prefix)] = object
	}
	return refs, nil
}

// isObjectID accepts the one shape an object id has. The value becomes an argv
// element, so it is validated rather than trusted for having come from Git.
func isObjectID(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (m *Manager) guestAncestor(ctx context.Context, op, workspace, old, next string) (bool, error) {
	res, err := m.run(ctx, op, m.workspaceGit(workspace, "merge-base", "--is-ancestor", old, next))
	if err != nil {
		return false, err
	}
	return ancestorAnswer(op, res)
}

func (m *Manager) hostAncestor(ctx context.Context, op, hub, old, next string) (bool, error) {
	res, err := m.runHost(ctx, op, hub, "merge-base", "--is-ancestor", old, next)
	if err != nil {
		return false, err
	}
	return ancestorAnswer(op, res)
}

// ancestorAnswer maps the two documented exits of `merge-base --is-ancestor`.
// Any other exit is unverifiable ancestry, and a ref whose ancestry could not be
// established must not move.
func ancestorAnswer(op string, res execx.Result) (bool, error) {
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, commandError(op, KindGit, "compare two histories", res.ExitCode)
	}
}

func (m *Manager) countGuest(ctx context.Context, op, workspace, arg string) (int, error) {
	res, err := m.run(ctx, op, m.workspaceGit(workspace, "rev-list", "--count", arg))
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, commandError(op, KindGit, "count commits", res.ExitCode)
	}
	return parseCount(op, res)
}

func (m *Manager) countHost(ctx context.Context, op, hub, arg string) (int, error) {
	res, err := m.runHost(ctx, op, hub, "rev-list", "--count", arg)
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
		return 0, &Error{Op: op, Kind: KindVerification, Err: errors.New("a commit count could not be read")}
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
		return execx.Result{}, &Error{Op: op, Kind: KindGit, Err: errors.New("git could not be run on the host")}
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

// attachFromHub materializes a local project from the host repository, which is
// the third source a session may draw a checkout from (ADR-0024, ADR-0029).
//
// It reports whether there was anything to materialize from. A host repository
// that has never been written is not a failure: it is the state of a project
// nobody has reconciled yet, and the caller has a refusal that names both ways
// forward.
func (m *Manager) attachFromHub(ctx context.Context, op, id, workspace string) (bool, error) {
	hub, err := m.hubPath(id)
	if err != nil {
		return false, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	if !hubExists(hub) {
		return false, nil
	}
	refs, err := m.listHostRefs(ctx, op, hub, "refs/", "refs/heads", "refs/tags")
	if err != nil {
		return false, err
	}
	if len(refs) == 0 {
		return false, nil
	}
	// Before anything clones from it. HEAD is what a clone reads to decide what
	// to check out, and a bare repository does not get one from having refs
	// written into it.
	if err := m.ensureHubHead(ctx, op, hub); err != nil {
		return false, err
	}
	dir, err := os.MkdirTemp("", "torio-project-hub-")
	if err != nil {
		return false, &Error{Op: op, Kind: KindTransport, Err: errors.New("private host staging could not be created")}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	hostBundle := filepath.Join(dir, bundleFileName)
	// HEAD is in the bundle because a clone reads it to decide what to check
	// out. Without it the clone lands on a branch name it invented, checks out
	// nothing, and the origin removal that follows takes the arriving refs with
	// it: a checkout holding no ref at all. Proven on a real box.
	if err := m.mustRunHost(ctx, op, "bundle the host repository", hub,
		"bundle", "create", hostBundle, "HEAD", "--branches", "--tags"); err != nil {
		return false, err
	}
	// The same carry a bundle attach makes, for the same reason: the guest gains
	// a repository and no remote, and the file is read once and forgotten.
	return true, m.attachFromBundle(ctx, op, hostBundle, workspace)
}
