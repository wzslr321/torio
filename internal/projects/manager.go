package projects

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/backend/claudecode"
	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

// Guest is the narrow, typed VM boundary used by the project manager. Every
// command crosses it as an exact argv — never a concatenated string, never
// through a shell.
type Guest interface {
	Bootstrap(ctx context.Context, opts lima.BootstrapOptions) (lima.BootstrapReport, error)
	SSH(ctx context.Context, command []string) (execx.Result, error)
	// CopyToGuest carries one directory in, one shot, bounded. It is here for
	// the bundle attach (ADR-0027): a repository that has no remote arrives as
	// a file or it does not arrive at all.
	CopyToGuest(ctx context.Context, hostSourceDir, guestDestinationDir, guestHome string) error
}

var _ Guest = (*lima.Adapter)(nil)

// SSHAgent is the operator's host SSH agent, as narrowly as this package is
// allowed to see it: where the socket is, and how many identities it holds.
//
// The narrowness is the contract. An operator shell forwards this agent, so a
// preflight has to know it can sign something — and knowing that must never
// require holding a key, a public key, or a fingerprint. Implementations MUST
// derive the count without retaining, returning or logging any of them.
type SSHAgent interface {
	// Socket returns SSH_AUTH_SOCK, empty when the operator has no agent.
	Socket() string
	// Identities returns how many identities the agent currently holds.
	Identities(ctx context.Context) (int, error)
}

// backend is the agent backend this instance runs. It arrives with the
// bootstrap options rather than as its own constructor argument because every
// guest command here is already bounded by the same pins.
func (m *Manager) backend() backend.Backend {
	if m.bootstrapOpts.Backend == nil {
		return claudecode.New()
	}
	return m.bootstrapOpts.Backend
}

// identity is the guest identity that owns the checkouts.
func (m *Manager) identity() backend.Identity { return m.backend().Identity() }

// Manager owns attaching, verifying and forgetting guest projects.
type Manager struct {
	guest         Guest
	registry      Registry
	bootstrapOpts lima.BootstrapOptions
	agent         SSHAgent
}

// New builds a Manager over a guest and a config registry. The bootstrap
// options carry the pins and the Lima login identity; the latter is the second
// trusted identity of every shared checkout.
func New(guest Guest, registry Registry, opts lima.BootstrapOptions) *Manager {
	return &Manager{
		guest:         guest,
		registry:      registry,
		bootstrapOpts: opts,
		agent:         hostSSHAgent{runner: &execx.ExecRunner{}},
	}
}

// Add attaches a repository: it clones the exact remote into the derived
// workspace path, or verifies and adopts a checkout that is already there,
// gives both trusted identities shared access, registers the project with
// the backend, and only then records it in config.
//
// The order is the point. Nothing is written to config until every guest
// postcondition holds, and nothing on the guest is deleted when a later step
// fails — a half-finished add leaves a verified checkout and an archived (or
// unregistered) checkout, which is exactly the state a rerun finishes
// from.
//
// A remote the guest cannot read still fails closed as an auth error. For an
// SSH remote that failure carries the public half of a deploy key the guest now
// holds, so the rerun that finishes the attach is the same command again, once
// the key is authorized without write access on the forge.
func (m *Manager) Add(ctx context.Context, req AddRequest) (AddReport, error) {
	const op = "add"
	var report AddReport

	entry := config.Project{ID: req.ID, DisplayName: req.DisplayName, Remote: req.Remote}
	if err := validateProject(entry); err != nil {
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	if strings.HasPrefix(req.DisplayName, "-") {
		// The display name is recorded as a positional
		// argument; a leading dash would be read as a flag there. The config layer
		// has no reason to care, this boundary does.
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: errors.New("display name must not start with '-'")}
	}
	if err := requireOneAttachSource(req); err != nil {
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	workspace, err := derivePath(m.identity().WorkspacePath, req.ID)
	if err != nil {
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	report.Project = view(entry, workspace)

	current, err := m.registry.Load()
	if err != nil {
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	existing, alreadyRegistered := findProject(current, req.ID)
	if alreadyRegistered && existing != entry {
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("project %q is already registered with different details", req.ID)}
	}
	if !alreadyRegistered {
		// Refuse a duplicate ID or an unapproved duplicate remote here, before a
		// single guest command runs, rather than after cloning.
		if _, err := current.WithProject(entry, config.AddOptions{AllowDuplicateRemote: req.AllowDuplicateRemote}); err != nil {
			return report, &Error{Op: op, Kind: KindConflict, Err: err}
		}
	}

	if err := m.requirePrepared(ctx, op); err != nil {
		return report, err
	}

	status, err := m.inspectCheckout(ctx, op, workspace, entry.Remote)
	if err != nil {
		return report, err
	}
	if status.Symlink {
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("workspace path for %q is a symlink; refusing to follow it", req.ID)}
	}
	if status.PathExists && !status.Directory {
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("workspace path for %q exists and is not a directory", req.ID)}
	}

	// How the checkout comes to exist. A recorded remote is cloned from, which
	// is the path this command has always taken. A project with no remote is
	// local (ADR-0027): it is initialized empty, or carried in as a bundle, and
	// which of the two is an explicit decision rather than something inferred
	// from an absence.
	var keyPath string
	switch {
	case entry.Remote != "":
		var key *DeployKey
		keyPath, key, err = m.ensureRemoteReadable(ctx, op, req.ID, entry.Remote)
		if key != nil {
			report.DeployKey = key
			if key.Generated {
				report.Notes = append(report.Notes, "deploy_key_generated")
			} else {
				report.Notes = append(report.Notes, "deploy_key_pending_authorization")
			}
		}
		if err != nil {
			return report, err
		}
		if keyPath != "" {
			report.Notes = append(report.Notes, "deploy_key_used")
		}

		if !status.PathExists {
			// --quiet for the same reason the preflight above asks only for HEAD,
			// though this one is a latent case rather than an observed failure:
			// clone progress is unbounded chatter on stderr, nothing here reads it,
			// and it grows with repository size and network time. Raising the
			// retained-output bound would be the wrong repair — it exists so a
			// runaway child cannot exhaust memory, and no bound covers every
			// repository.
			if err := m.mustRun(ctx, op, KindGit, "clone the remote",
				m.gitExec(keyPath, "clone", "--quiet", "--", entry.Remote, workspace)); err != nil {
				return report, err
			}
			report.Cloned = true
		} else {
			// An existing checkout is adopted only when it already is the repository
			// we would have cloned, and only when nothing would be lost by using it.
			// Ownership and permission bits are reconciled below — they are additive
			// and reversible — but content, history and the worktree never are:
			// Torio does not reset, clean or delete anything here.
			if err := requireAdoptable(op, req.ID, status); err != nil {
				return report, err
			}
			report.Adopted = true
		}

	case req.BundlePath != "":
		if status.PathExists {
			// A bundle is a whole repository arriving. Cloning it over a
			// checkout that is already there would be the destructive act this
			// command refuses everywhere else, so an existing tree is adopted
			// on its own terms or refused on them.
			if err := requireAdoptable(op, req.ID, status); err != nil {
				return report, err
			}
			report.Adopted = true
			report.Notes = append(report.Notes, "bundle_unused_checkout_present")
			break
		}
		if err := m.attachFromBundle(ctx, op, req.BundlePath, workspace); err != nil {
			return report, err
		}
		report.Cloned = true
		report.Notes = append(report.Notes, "attached_from_bundle")

	case req.Local:
		if status.PathExists {
			if err := requireAdoptable(op, req.ID, status); err != nil {
				return report, err
			}
			report.Adopted = true
			break
		}
		// No first commit: an empty repository is what was asked for, and the
		// first thing in a project's history belongs to whoever starts it.
		if err := m.mustRun(ctx, op, KindGit, "initialize the repository",
			guestexec.UserExecAs(m.identity().GuestUser, "git", "init", "--initial-branch=main", workspace)); err != nil {
			return report, err
		}
		report.Initialized = true
		report.Notes = append(report.Notes, "initialized_local")

	default:
		return report, localProjectNeedsASourceError(op, req.ID)
	}

	if err := m.ensureSharedPermissions(ctx, op, workspace); err != nil {
		return report, err
	}
	for _, user := range []string{m.identity().GuestUser, m.bootstrapOpts.OperatorUser} {
		if err := m.ensureSafeDirectory(ctx, op, user, workspace); err != nil {
			return report, err
		}
	}
	if keyPath != "" {
		// Only now, with a checkout on disk to scope the include to. A fetch the
		// identity runs on its own reaches the remote the same way this run did.
		if err := m.provisionGuestGitAccess(ctx, op, req.ID, workspace); err != nil {
			return report, err
		}
	}

	final, err := m.inspectCheckout(ctx, op, workspace, entry.Remote)
	if err != nil {
		return report, err
	}
	if !final.compliant() {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("checkout for %q did not satisfy the attachment postconditions: %s", req.ID, strings.Join(final.issues(), ", "))}
	}

	if err := m.persist(op, entry, req.AllowDuplicateRemote); err != nil {
		report.Notes = append(report.Notes, "checkout_retained", "rerun_finishes")
		return report, err
	}
	report.Registered = true
	return report, nil
}

// List returns the registered projects with their derived paths, sorted by ID.
// It reads config only and runs no guest command, so it works with the VM down.
func (m *Manager) List() ([]Project, error) {
	const op = "list"
	current, err := m.registry.Load()
	if err != nil {
		return nil, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	out := make([]Project, 0, len(current.Projects))
	for _, p := range current.Projects {
		workspace, err := derivePath(m.identity().WorkspacePath, p.ID)
		if err != nil {
			return nil, &Error{Op: op, Kind: KindVerification, Err: err}
		}
		out = append(out, view(p, workspace))
	}
	slices.SortFunc(out, func(a, b Project) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Show inspects one attached project: the registry entry, the derived path, the
// state of the guest checkout. It reports drift
// instead of repairing it.
func (m *Manager) Show(ctx context.Context, id string) (ShowReport, error) {
	const op = "show"
	var report ShowReport

	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return report, err
	}
	report.Project = view(entry, workspace)

	if err := m.requirePrepared(ctx, op); err != nil {
		return report, err
	}
	checkout, err := m.inspectCheckout(ctx, op, workspace, entry.Remote)
	if err != nil {
		return report, err
	}
	report.Checkout = checkout
	report.Issues = checkout.issues()

	return report, nil
}

// Remove forgets a project. It removes
// the config entry only after that succeeded, so an interruption always leaves
// the config entry behind — the marker a rerun needs to finish the removal.
// The checkout is never touched, and the report says so.
// SetRemote corrects the remote of a registered project (ADR-0023).
//
// The record is what the operator asked to change and is what always changes.
// The checkout is a second copy of the same fact, so it follows only where it
// still holds the remote being replaced: an origin pointing anywhere else is
// somebody's deliberate act, and repointing it would be Torio editing a working
// tree it cannot vouch for. Every other guest's checkout of the same project is
// out of reach from here and stays as it is until the next command runs there.
//
// Nothing is removed. The entry keeps its id and its display name, so the
// derived paths, the registrations and the per-guest deploy keys all still name
// the same project.
func (m *Manager) SetRemote(ctx context.Context, id, remote string) (SetRemoteReport, error) {
	const op = "set_remote"
	var report SetRemoteReport

	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return report, err
	}
	if remote == "" {
		// A record may hold no remote, but not by having one taken away here.
		// Every other guest's checkout of this project still points at the
		// remote, and forgetting it centrally would strand them all with no
		// way to say what they are pointing at (ADR-0027).
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: errors.New(
			"a remote cannot be removed: other guests' checkouts still point at it. " +
				"Correct it to another remote, or forget the project with `torio project remove <id>`")}
	}
	report.PreviousRemote = entry.Remote
	corrected := entry
	corrected.Remote = remote
	report.Project = view(corrected, workspace)

	// The document decides whether the replacement is writable at all, before
	// any guest is touched: it runs the addition's own validation, so a
	// correction can never record what an addition would have refused.
	if err := m.registry.Update(func(current config.File) (config.File, error) {
		return current.WithUpdatedRemote(id, remote, config.AddOptions{})
	}); err != nil {
		if errors.Is(err, config.ErrDuplicateProjectID) || errors.Is(err, config.ErrDuplicateRemote) {
			return report, &Error{Op: op, Kind: KindConflict, Err: err}
		}
		if errors.Is(err, config.ErrProjectNotFound) {
			return report, &Error{Op: op, Kind: KindConflict, Err: err}
		}
		return report, &Error{Op: op, Kind: KindConfigWrite, Err: err}
	}

	// From here the record is already corrected. A guest that cannot be reached
	// is reported, never rolled back: the record was the thing asked for, and
	// undoing it would leave the operator with the remote they just told Torio
	// was wrong.
	if err := m.requirePrepared(ctx, op); err != nil {
		report.Notes = append(report.Notes, "checkout_untouched_guest_unavailable")
		return report, nil
	}
	checkout, err := m.inspectCheckout(ctx, op, workspace, entry.Remote)
	if err != nil {
		report.Notes = append(report.Notes, "checkout_untouched_guest_unavailable")
		return report, nil
	}
	// Which act this is, decided by the tree rather than by the record.
	//
	// A checkout with no origin is being given one; a checkout that already
	// holds the remote being replaced is being moved; anything else is somebody
	// else's decision and is left alone. Reading this off the record instead
	// breaks the rerun: the first attempt at a promotion records the remote
	// before it can fail on an authorization, so the second attempt would see
	// an already-corrected record and report the origin-less checkout as
	// pointing elsewhere. Found by running it.
	promoting := !checkout.HasOrigin
	switch {
	case !checkout.PathExists || !checkout.Repository:
		report.Notes = append(report.Notes, "checkout_absent")
		return report, nil
	case !promoting && !checkout.OriginMatches:
		report.Notes = append(report.Notes, "checkout_origin_differs")
		return report, nil
	}

	// A project that had no remote is being given one, which is the moment the
	// guest first needs to be able to read it — and the moment a deploy key
	// first means anything (ADR-0027). It is the same fail-closed shape `add`
	// has: the record is already corrected, so the rerun that finishes this is
	// the same command, once the key is authorized.
	keyPath := ""
	if promoting {
		var key *DeployKey
		keyPath, key, err = m.ensureRemoteReadable(ctx, op, id, remote)
		if key != nil {
			report.DeployKey = key
			if key.Generated {
				report.Notes = append(report.Notes, "deploy_key_generated")
			} else {
				report.Notes = append(report.Notes, "deploy_key_pending_authorization")
			}
		}
		if err != nil {
			return report, err
		}
		if keyPath != "" {
			report.Notes = append(report.Notes, "deploy_key_used")
		}
	}

	// Plainly as the agent identity, without the transport environment the
	// clone and the readability preflight carry: setting a URL rewrites the
	// checkout's own configuration and reaches no remote.
	//
	// `set-url` needs an origin to move; a promoted local checkout has none, so
	// this is the one place the two shapes differ.
	originArgs := []string{"remote", "set-url", "origin", remote}
	if promoting {
		originArgs = []string{"remote", "add", "origin", remote}
	}
	if err := m.mustRun(ctx, op, KindGit, "attach the checkout origin",
		guestexec.UserExecAs(m.identity().GuestUser, append([]string{"git", "-C", workspace}, originArgs...)...)); err != nil {
		return report, err
	}
	if keyPath != "" {
		// The include is scoped to this checkout, so a fetch the identity runs
		// on its own reaches the remote the way this run did.
		if err := m.provisionGuestGitAccess(ctx, op, id, workspace); err != nil {
			return report, err
		}
	}
	final, err := m.inspectCheckout(ctx, op, workspace, remote)
	if err != nil {
		return report, err
	}
	if !final.OriginMatches {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf(
			"the checkout for %q still does not carry the corrected remote", id)}
	}
	report.CheckoutRepointed = true
	if promoting {
		report.Notes = append(report.Notes, "remote_attached")
	} else {
		report.Notes = append(report.Notes, "checkout_repointed")
	}
	return report, nil
}

func (m *Manager) Remove(ctx context.Context, id string) (RemoveReport, error) {
	const op = "remove"
	var report RemoveReport

	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return report, err
	}
	report.Project = view(entry, workspace)
	report.CheckoutPath = workspace
	report.CheckoutRetained = true
	report.Notes = append(report.Notes, "checkout_retained")

	if err := m.requirePrepared(ctx, op); err != nil {
		return report, err
	}

	// A deploy key outlives the entry that caused it, the same way the checkout
	// does. Removing is forgetting, not revoking: withdrawing the authorization
	// on the forge is the operator's call, and saying so beats leaving a key
	// nobody remembers granting.
	held, err := m.testAsUser(ctx, op, "-f", m.deployKeyPath(id))
	if err != nil {
		return report, err
	}
	if held {
		report.Notes = append(report.Notes, "deploy_key_retained")
	}

	err = m.registry.Update(func(fresh config.File) (config.File, error) {
		return fresh.WithoutProject(id)
	})
	if err != nil {
		if errors.Is(err, config.ErrProjectNotFound) {
			// A concurrent rerun already removed the entry; the removal is done.
			return report, nil
		}
		report.Notes = append(report.Notes, "config_entry_retained", "rerun_finishes")
		return report, &Error{Op: op, Kind: KindConfigWrite, Err: err}
	}
	return report, nil
}

const (
	// shellOp names the push-capable operator-shell operations in errors.
	shellOp = "shell"
	// enterOp names ordinary workspace-session operations in errors.
	enterOp = "enter"
)

// EnterPreflight proves that an ordinary project workspace session may be
// opened. It verifies the same checkout and shared-permission boundary as the
// push-capable shell, but never inspects the host SSH agent.
func (m *Manager) EnterPreflight(ctx context.Context, id string) (EnterSession, error) {
	const op = enterOp
	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return EnterSession{}, err
	}
	if err := m.requireOperatorUser(op); err != nil {
		return EnterSession{}, err
	}
	session := EnterSession{EnterSpec: EnterSpec{
		Project:       view(entry, workspace),
		Group:         sharedGroup,
		Instance:      lima.InstanceName,
		OperatorUser:  m.bootstrapOpts.OperatorUser,
		Preconditions: slices.Clone(enterPreconditions),
	}}

	if err := m.requirePrepared(ctx, op); err != nil {
		return EnterSession{}, err
	}
	session.Verified = append(session.Verified, "vm_running", "project_enter_helper", "shared_group_membership")

	checkout, err := m.inspectCheckout(ctx, op, session.Project.Path, session.Project.Remote)
	if err != nil {
		return EnterSession{}, err
	}
	if !checkout.PathExists || checkout.Symlink || !checkout.Directory || !checkout.Repository {
		return EnterSession{}, sessionDriftError(op, id, checkout)
	}
	session.Verified = append(session.Verified, "checkout_present")
	if !checkout.OriginMatches {
		return EnterSession{}, sessionDriftError(op, id, checkout)
	}
	session.Verified = append(session.Verified, "origin_matches")
	if !checkout.SharedPermissions {
		return EnterSession{}, sessionDriftError(op, id, checkout)
	}
	session.Verified = append(session.Verified, "shared_permissions")

	session.Review = m.reviewContext(ctx, op, session.Project.Path)

	return session, nil
}

// ShellPreflight proves that an ephemeral operator session may be opened for
// id, and returns the data that session needs.
//
// It is the only way to obtain a ShellSpec, which is the point: a session
// forwards the operator's SSH agent into the guest, so "where would the session
// go" and "may it be opened" must not be separable questions.
//
// What it deliberately does not do is test the push. A preflight that proved
// write access by using it would mutate a remote to answer a question, and it
// would need a credential Torio does not have and must never acquire. The
// checks below prove the session can be opened; whether a push succeeds is the
// operator's business, inside the session, with their own key.
func (m *Manager) ShellPreflight(ctx context.Context, id string) (ShellSession, error) {
	const op = shellOp
	spec, err := m.shellSpec(op, id)
	if err != nil {
		return ShellSession{}, err
	}
	session := ShellSession{ShellSpec: spec}

	// Bootstrap is the guest gate, not a lighter re-derivation of it: it already
	// proves the instance is Running, that the operator shell helper is a
	// root-owned 0755 regular file nobody else can rewrite, and that both
	// identities are in the shared group. Re-implementing weaker versions here
	// would be the same checks with fewer guarantees.
	if err := m.requirePrepared(ctx, op); err != nil {
		return ShellSession{}, err
	}
	session.Verified = append(session.Verified, "vm_running", "operator_shell_helper", "shared_group_membership")

	checkout, err := m.inspectCheckout(ctx, op, spec.Project.Path, spec.Project.Remote)
	if err != nil {
		return ShellSession{}, err
	}
	// A dirty worktree, a shallow clone or a repo-local credential helper are
	// not checked here. The first two are exactly what an operator opens a
	// session to deal with, and refusing them would leave no way to fix them.
	if !checkout.PathExists || checkout.Symlink || !checkout.Directory || !checkout.Repository {
		return ShellSession{}, shellDriftError(id, checkout)
	}
	session.Verified = append(session.Verified, "checkout_present")

	if !checkout.OriginMatches {
		return ShellSession{}, shellDriftError(id, checkout)
	}
	session.Verified = append(session.Verified, "origin_matches")

	// Without the shared group ownership and setgid, the operator's session
	// cannot write the tree the agent owns — or writes files the agent then
	// cannot read back.
	if !checkout.SharedPermissions {
		return ShellSession{}, shellDriftError(id, checkout)
	}
	session.Verified = append(session.Verified, "shared_permissions")

	if err := m.requireForwardableAgent(ctx, op); err != nil {
		return ShellSession{}, err
	}
	session.Verified = append(session.Verified, "operator_ssh_agent")

	session.Review = m.reviewContext(ctx, op, spec.Project.Path)

	return session, nil
}

// reviewContext reads what the checkout looks like right now.
//
// It runs after every precondition has been proved, and it cannot fail the
// preflight. Everything above decides whether a session may be opened; this only
// decides what the operator is told when it opens, and refusing to open a
// session because a description could not be assembled would be the tail wagging
// the dog. Anything unreadable is left unsaid instead.
//
// Both commands are ordinary read-only Git. Neither pushes, fetches or contacts
// a remote — TestShellPreflightNeverTestsThePush still holds over the whole
// preflight argv, and this must never be the change that makes it stop holding.
func (m *Manager) reviewContext(ctx context.Context, op, workspace string) ReviewContext {
	var review ReviewContext

	// A detached HEAD exits non-zero here, which is not an error: it is a
	// checkout with no branch to name.
	branch, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "symbolic-ref", "--short", "HEAD"))
	if err != nil || branch.ExitCode != 0 {
		return review
	}
	review.Branch = strings.TrimSpace(string(branch.Stdout))
	if review.Branch == "" {
		return review
	}

	// `@{u}` resolves the configured upstream. With none configured this exits
	// non-zero, and "no upstream" is left as the absence it is rather than
	// reported as zero commits ahead.
	ahead, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "rev-list", "--count", "@{u}..HEAD"))
	if err != nil || ahead.ExitCode != 0 {
		return review
	}
	count, convErr := strconv.Atoi(strings.TrimSpace(string(ahead.Stdout)))
	if convErr != nil || count < 0 {
		return review
	}
	review.Ahead = count
	review.AheadKnown = true
	return review
}

// shellSpec resolves the registry entry into the data a session needs. It runs
// nothing; ShellPreflight is what makes the result usable.
func (m *Manager) shellSpec(op, id string) (ShellSpec, error) {
	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return ShellSpec{}, err
	}
	if err := m.requireOperatorUser(op); err != nil {
		return ShellSpec{}, err
	}
	return ShellSpec{
		Project:       view(entry, workspace),
		Group:         sharedGroup,
		Instance:      lima.InstanceName,
		OperatorUser:  m.bootstrapOpts.OperatorUser,
		Preconditions: slices.Clone(shellPreconditions),
	}, nil
}

// shellDriftError reports a checkout the registry claims is attached and the
// guest says is not. It names the stable markers and nothing else — a rerun of
// `project add` is the remedy for all of them.
func shellDriftError(id string, checkout CheckoutStatus) error {
	return sessionDriftError(shellOp, id, checkout)
}

// The remedy names the project, because the one-argument form is the one that
// applies here: the entry is registered, so `add` completes the remote from the
// record and materializes the checkout this guest is missing. The bare verb
// would leave the operator to work out which of the shapes of `add` reconciles
// a checkout rather than attaching a second repository.
func sessionDriftError(op, id string, checkout CheckoutStatus) error {
	issues := checkout.issues()
	return &Error{
		Op:     op,
		Kind:   KindVerification,
		Issues: issues,
		Err: fmt.Errorf("the checkout for %q is not in a state a session can be opened in (%s); re-run `torio project add %s` to reconcile it",
			id, strings.Join(issues, ", "), id),
	}
}

// requireForwardableAgent proves the operator has an agent worth forwarding.
//
// `ssh -A` with no agent, or with an agent holding nothing, opens a session
// that looks identical to a working one and fails only at the push — after the
// operator has already done the work. The two failures are separated because
// their remedies are: no socket is an agent that was never started, an empty
// agent is access nobody has granted yet.
func (m *Manager) requireForwardableAgent(ctx context.Context, op string) error {
	if strings.TrimSpace(m.agent.Socket()) == "" {
		return &Error{Op: op, Kind: KindPrecondition, Err: errors.New("SSH_AUTH_SOCK is not set; start an ssh-agent on this Mac, then `ssh-add` the key that can push")}
	}
	count, err := m.agent.Identities(ctx)
	if err != nil {
		// The cause is dropped rather than wrapped. It is the one diagnostic in
		// this package derived from agent output, and agent output is where key
		// material would be if it were anywhere.
		return &Error{Op: op, Kind: KindPrecondition, Err: errors.New("the SSH agent at SSH_AUTH_SOCK could not be queried; confirm it is running")}
	}
	if count < 1 {
		return &Error{Op: op, Kind: KindAuth, Err: errors.New("the SSH agent holds no identity to forward; `ssh-add` the key that can push")}
	}
	return nil
}

// resolve looks a registered project up and derives its workspace path. An ID
// that is not in the registry is a conflict, not a silent empty result.
func (m *Manager) resolve(op, id string) (config.Project, string, error) {
	workspace, err := derivePath(m.identity().WorkspacePath, id)
	if err != nil {
		return config.Project{}, "", &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	current, err := m.registry.Load()
	if err != nil {
		return config.Project{}, "", &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	entry, found := findProject(current, id)
	if !found {
		return config.Project{}, "", &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("%w: %q", config.ErrProjectNotFound, id)}
	}
	return entry, workspace, nil
}

// requirePrepared proves the guest can be worked on at all: a bootstrap-verified
// Running VM, passwordless sudo, and a configured Lima login identity.
func (m *Manager) requirePrepared(ctx context.Context, op string) error {
	if err := m.requireOperatorUser(op); err != nil {
		return err
	}
	if _, err := m.guest.Bootstrap(ctx, m.bootstrapOpts); err != nil {
		return &Error{
			Op:   op,
			Kind: KindPrecondition,
			Err:  fmt.Errorf("VM %q is not bootstrap-verified; run `torio vm bootstrap`: %w", lima.InstanceName, err),
		}
	}
	return m.mustRun(ctx, op, KindGuestCommand, "verify passwordless sudo", guestexec.RootExec("true"))
}

func (m *Manager) requireOperatorUser(op string) error {
	// Bootstrap validates this identity against a strict allowlist before any
	// guest work; refusing an empty one here keeps a misconfigured caller from
	// building a `sudo -u` argv with a missing operand.
	if strings.TrimSpace(m.bootstrapOpts.OperatorUser) == "" {
		return &Error{Op: op, Kind: KindPrecondition, Err: errors.New("the Lima login user is not configured; it is the second identity a shared checkout must trust")}
	}
	return nil
}

// preflightRemote reports whether the guest can read the remote without a
// prompt, before anything is cloned. GIT_TERMINAL_PROMPT=0 and SSH BatchMode
// make a missing credential an immediate answer instead of a hanging prompt.
//
// It returns readability rather than an error because an unreadable remote is
// not always the end: the caller decides whether a deploy key changes it. A
// transport failure is still an error.
func (m *Manager) preflightRemote(ctx context.Context, op, remote, keyPath string) (bool, error) {
	// Asking for HEAD alone, because this proves readability and nothing else
	// reads the output. An unqualified ls-remote returns every ref the server
	// advertises, and GitHub advertises refs/pull/* — a busy upstream answered
	// with 4.7 MiB, which exceeds execx.DefaultMaxOutputPerStream and turned a
	// perfectly readable remote into "bounded guest output was truncated".
	// Restricting the query keeps it at one line.
	//
	// Deliberately without --exit-code: an empty repository has no HEAD, and
	// --exit-code would report that as unreadable when the guest can read it
	// fine. Readability is already carried by the exit status below — an
	// unreadable remote exits 128.
	res, err := m.run(ctx, op, m.gitExec(keyPath, "ls-remote", "--", remote, "HEAD"))
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 && mentionsUnresolvedHost(res.Stderr) {
		return false, unresolvedHostError(op, remote)
	}
	return res.ExitCode == 0, nil
}

// unresolvedHostMarker is what SSH prints when the name in a remote is one the
// guest has no way to look up. It is matched rather than parsed: the host it
// names is already known from the remote, and the rest of the line is a
// resolver diagnostic that varies.
const unresolvedHostMarker = "Could not resolve hostname"

func mentionsUnresolvedHost(stderr []byte) bool {
	return bytes.Contains(stderr, []byte(unresolvedHostMarker))
}

// unresolvedHostError separates a host the guest never reached from a remote it
// was not allowed to read.
//
// The two used to answer alike, and the answer was the wrong one: an operator
// told to authorize a deploy key authorizes it, reruns, and gets the same
// refusal, because nothing was ever presented to the forge. A host only the
// operator's own machine knows is what this looks like from inside a VM, and
// the record is the thing to correct (ADR-0023).
func unresolvedHostError(op, remote string) error {
	host := classifyRemote(remote).Host
	if host == "" {
		host = remote
	}
	return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf(
		"the guest cannot resolve %q, so the remote on record names a host it has no way to reach; "+
			"a host-local SSH alias looks like this from inside the VM. Correct the record with `torio project set-remote <id> <remote>`",
		host)}
}

// requireOneAttachSource refuses an attach that names more than one way in.
//
// A remote, a bundle and an empty repository are three different repositories
// to end up with, and a command given two of them would silently pick one. The
// operator asked for something specific either way; which one is not for Torio
// to decide.
func requireOneAttachSource(req AddRequest) error {
	switch {
	case req.Remote != "" && req.Local:
		return errors.New("a project is either local or attached to a remote, not both")
	case req.Remote != "" && req.BundlePath != "":
		return errors.New("a remote is cloned from the remote; a bundle attaches a project that has none")
	case req.Local && req.BundlePath != "":
		return errors.New("a bundle carries a repository in; an empty one is initialized instead")
	default:
		return nil
	}
}

// localProjectNeedsASourceError is the one shape Add cannot answer: a record
// that says the project is local, on a guest that does not hold it.
//
// Materializing clones from the remote on record (ADR-0024), and there is none
// to clone from. The two ways forward are both real and neither is Torio's to
// choose: carry the repository in, or give the project a remote so every guest
// can reach it. Both are named, because a refusal that names no next step is
// where an operator stops.
func localProjectNeedsASourceError(op, id string) error {
	return &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf(
		"project %q is local: it has no remote on record, so there is nothing to clone it from here. "+
			"Carry it in with `torio project add %s --from-bundle <file>`, or give it a remote with "+
			"`torio project set-remote %s <remote>` so every guest can reach it", id, id, id)}
}

// unreadableRemoteError says what the operator has to do next, and says it
// differently per transport because the two have different answers.
//
// An SSH remote is one authorization away: the key named here is on the guest
// already and the rerun is the same command. An HTTPS remote is not, because
// reading a private one takes a credential, and holding one is what Torio does
// not do.
func unreadableRemoteError(op string, key *DeployKey) error {
	if key == nil {
		return &Error{Op: op, Kind: KindAuth, Err: errors.New(
			"the guest cannot read the remote noninteractively; for a private repository use the SSH remote, which Torio can provision a deploy key for")}
	}
	// Without a position: the human path prints the key above this line, the JSON
	// path carries it in the error details, and a message naming a place is wrong
	// in one of them.
	//
	// It names the deploy key mechanism rather than saying "authorize", because
	// the read-only property this decision rests on comes from where the key is
	// added and nowhere else. Added to the account instead of to the repository,
	// the same key attaches the project just as well and hands the guest write
	// capability over every repository the account can reach.
	return &Error{Op: op, Kind: KindAuth, Err: fmt.Errorf(
		"the guest cannot read the remote yet; add its public key to the repository on %s as a deploy key with write access off, not as an account key, then run the same command again", key.Host)}
}

// ensureRemoteReadable proves the guest can read the remote, provisioning a
// deploy key for an SSH remote it cannot.
//
// The key is offered on the run that generates it, so a key an operator
// authorized ahead of time attaches in one command. When the forge has not been
// told about the key yet the run still fails closed, carrying the public half
// that makes the next run succeed.
func (m *Manager) ensureRemoteReadable(ctx context.Context, op, id, remote string) (string, *DeployKey, error) {
	keyPath := m.deployKeyPath(id)
	held, err := m.testAsUser(ctx, op, "-f", keyPath)
	if err != nil {
		return "", nil, err
	}
	offered := ""
	if held {
		offered = keyPath
	}
	readable, err := m.preflightRemote(ctx, op, remote, offered)
	if err != nil {
		return "", nil, err
	}

	access := classifyRemote(remote)
	switch {
	case readable:
		// A key that is on the guest is offered from here on, whether or not it
		// is what opened the remote: it is this project's key, and using it keeps
		// the checkout reading the same way on every later fetch.
		return offered, nil, nil
	case access.Transport != TransportSSH || access.Host == "":
		// Reading a private HTTPS remote takes a credential, and holding one is
		// the thing Torio does not do.
		return "", nil, unreadableRemoteError(op, nil)
	}

	// Reading back an existing key rather than generating a second one: when the
	// key is already there, the act still missing is on the forge.
	pub := ""
	if held {
		pub, err = m.readPublicKey(ctx, op, keyPath)
	} else {
		pub, err = m.ensureDeployKey(ctx, op, id)
	}
	if err != nil {
		return "", nil, err
	}
	key := &DeployKey{PublicKey: pub, Host: access.Host, KeyPath: keyPath, Generated: !held}
	if held {
		return "", key, unreadableRemoteError(op, key)
	}

	// The key was generated during this run, so it has not been offered yet.
	readable, err = m.preflightRemote(ctx, op, remote, keyPath)
	if err != nil {
		return "", nil, err
	}
	if !readable {
		return "", key, unreadableRemoteError(op, key)
	}
	return keyPath, key, nil
}

// inspectCheckout derives the state of the guest checkout. It never follows a
// symlink and never runs Git inside a path it has not first proven to be a
// plain directory.
func (m *Manager) inspectCheckout(ctx context.Context, op, workspace, remote string) (CheckoutStatus, error) {
	var st CheckoutStatus

	link, err := m.testRoot(ctx, op, "-L", workspace)
	if err != nil {
		return st, err
	}
	if link {
		st.PathExists = true
		st.Symlink = true
		return st, nil
	}
	exists, err := m.testRoot(ctx, op, "-e", workspace)
	if err != nil {
		return st, err
	}
	st.PathExists = exists
	if !exists {
		return st, nil
	}
	dir, err := m.testRoot(ctx, op, "-d", workspace)
	if err != nil {
		return st, err
	}
	st.Directory = dir
	if !dir {
		return st, nil
	}

	meta, err := m.run(ctx, op, guestexec.RootExec("stat", "-c", "%U:%G %a", workspace))
	if err != nil {
		return st, err
	}
	if meta.ExitCode != 0 {
		return st, commandError(op, KindGuestCommand, "inspect checkout ownership", meta.ExitCode)
	}
	st.Owner, st.Group, st.Mode = guestexec.ParseOwnershipMode(string(meta.Stdout))
	st.SharedPermissions = st.Owner == m.identity().GuestUser && st.Group == sharedGroup && sharedModeOK(st.Mode)

	// The top level must be the derived path itself: a checkout nested inside
	// another repository would let that repository's config govern ours.
	top, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "rev-parse", "--show-toplevel"))
	if err != nil {
		return st, err
	}
	st.Repository = top.ExitCode == 0 && strings.TrimSpace(string(top.Stdout)) == workspace
	if !st.Repository {
		return st, nil
	}

	// The raw stored URL, not the effective one: an insteadOf rewrite must not
	// be able to make a different remote look like ours.
	//
	// A record with no remote is a local project (ADR-0027), and agreement for
	// one is an origin that is not there. The comparison is the same either
	// way — what the record says the tree points at — so a local checkout that
	// has grown an origin is ordinary drift, in the vocabulary drift already
	// had.
	origin, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "config", "--get", "remote.origin.url"))
	if err != nil {
		return st, err
	}
	recorded := ""
	if origin.ExitCode == 0 {
		recorded = strings.TrimSpace(string(origin.Stdout))
	}
	st.HasOrigin = recorded != ""
	st.OriginMatches = recorded == remote

	shallow, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "rev-parse", "--is-shallow-repository"))
	if err != nil {
		return st, err
	}
	if shallow.ExitCode != 0 {
		return st, commandError(op, KindGit, "inspect clone depth", shallow.ExitCode)
	}
	switch strings.TrimSpace(string(shallow.Stdout)) {
	case "false":
		st.FullClone = true
	case "true":
		st.FullClone = false
	default:
		return st, &Error{Op: op, Kind: KindVerification, Err: errors.New("could not determine whether the checkout is a shallow clone")}
	}

	worktree, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "status", "--porcelain=v1", "--untracked-files=normal"))
	if err != nil {
		return st, err
	}
	if worktree.ExitCode != 0 {
		return st, commandError(op, KindGit, "inspect worktree", worktree.ExitCode)
	}
	st.Clean = len(bytes.TrimSpace(worktree.Stdout)) == 0

	// `--get-regexp` exits 0 only when it matched something, so exit 1 is the
	// proof we want. Any other exit is unverifiable state and fails closed.
	cred, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "config", "--local", "--get-regexp", `^credential\.`))
	if err != nil {
		return st, err
	}
	switch cred.ExitCode {
	case 0:
		st.NoCredentialHelper = false
	case 1:
		st.NoCredentialHelper = true
	default:
		return st, commandError(op, KindGit, "inspect repository credential configuration", cred.ExitCode)
	}
	return st, nil
}

// requireAdoptable refuses every existing checkout that is not already the
// repository Torio would have cloned. Ownership and mode are excluded here on
// purpose: they are reconciled by the caller and verified afterwards, and
// refusing them up front would reject a checkout an operator cloned by hand for
// a reason Torio can fix without touching content.
func requireAdoptable(op, id string, st CheckoutStatus) error {
	switch {
	case !st.Repository:
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("workspace path for %q exists but is not the top level of a Git repository", id)}
	case !st.OriginMatches:
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("checkout for %q has a different origin remote", id)}
	case !st.FullClone:
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("checkout for %q is a shallow clone", id)}
	case !st.Clean:
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("checkout for %q has uncommitted changes; Torio will not reset or clean it", id)}
	case !st.NoCredentialHelper:
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("checkout for %q configures a repository-local credential helper", id)}
	}
	return nil
}

// ensureSharedPermissions gives the checkout the group ownership, group write
// and setgid of the shared-workspace layout, so the agent and the
// operator can both work the tree and new files stay in the shared group. It
// changes metadata only — no content, no history, nothing removed.
func (m *Manager) ensureSharedPermissions(ctx context.Context, op, workspace string) error {
	if err := m.mustRun(ctx, op, KindGuestCommand, "set shared checkout ownership",
		guestexec.RootExec("chown", "-R", m.identity().GuestUser+":"+sharedGroup, "--", workspace)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "set shared checkout permissions",
		guestexec.RootExec("chmod", "-R", "g+rwX", "--", workspace)); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "set setgid on checkout directories",
		guestexec.RootExec("find", workspace, "-type", "d", "-exec", "chmod", "g+s", "{}", "+"))
}

// ensureSafeDirectory records the checkout as safe for one trusted identity.
// Git refuses to operate in a tree owned by another user without it, and both
// identities need it because both work this tree.
//
// `--add` appends unconditionally, so the entry is read first and the result is
// read back afterwards: a rerun must not grow the operator's global config.
func (m *Manager) ensureSafeDirectory(ctx context.Context, op, user, workspace string) error {
	present, err := m.safeDirectoryPresent(ctx, op, user, workspace)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	if err := m.mustRun(ctx, op, KindGit, "add a safe.directory entry",
		guestexec.UserExecAs(user, "git", "config", "--global", "--add", "safe.directory", workspace)); err != nil {
		return err
	}
	present, err = m.safeDirectoryPresent(ctx, op, user, workspace)
	if err != nil {
		return err
	}
	if !present {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("the safe.directory entry for %q did not persist", user)}
	}
	return nil
}

func (m *Manager) safeDirectoryPresent(ctx context.Context, op, user, workspace string) (bool, error) {
	res, err := m.run(ctx, op, guestexec.UserExecAs(user, "git", "config", "--global", "--get-all", "safe.directory"))
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0, 1: // 1 is "the key is unset", not a failure
	default:
		return false, commandError(op, KindGit, "read safe.directory entries", res.ExitCode)
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if strings.TrimSpace(line) == workspace {
			return true, nil
		}
	}
	return false, nil
}

// persist appends the entry to the config registry. It reloads first, so it
// writes on top of the document as it is now rather than as it was before the
// guest work, and it is a no-op when the entry is already there — which is what
// makes a rerun after an interrupted add finish cleanly.
func (m *Manager) persist(op string, entry config.Project, allowDuplicateRemote bool) error {
	err := m.registry.Update(func(current config.File) (config.File, error) {
		if existing, found := findProject(current, entry.ID); found {
			if existing == entry {
				return current, nil
			}
			return config.File{}, fmt.Errorf("%w: project %q is already registered with different details", config.ErrDuplicateProjectID, entry.ID)
		}
		return current.WithProject(entry, config.AddOptions{AllowDuplicateRemote: allowDuplicateRemote})
	})
	if err != nil {
		if errors.Is(err, config.ErrDuplicateProjectID) || errors.Is(err, config.ErrDuplicateRemote) {
			return &Error{Op: op, Kind: KindConflict, Err: err}
		}
		return &Error{Op: op, Kind: KindConfigWrite, Err: err}
	}
	return nil
}

// forever otherwise, and this runs before an interactive session that is
// deliberately unbounded — the one place where "no timeout" is correct.
const sshAgentProbeTimeout = 5 * time.Second

// hostSSHAgent is the production SSHAgent: `ssh-add -l` over the typed runner.
//
// `-l` lists fingerprints, not keys (`-L` would list the public keys), and even
// that never leaves this type: the exit code decides the outcome and the output
// is reduced to a line count on the spot. Nothing derived from it reaches a
// caller, a log or an error.
type hostSSHAgent struct {
	runner execx.Runner
}

var _ SSHAgent = hostSSHAgent{}

func (h hostSSHAgent) Socket() string { return os.Getenv("SSH_AUTH_SOCK") }

// Identities returns the number of identities the agent holds. It maps the
// three documented `ssh-add -l` exits: 0 lists identities, 1 is a reachable
// agent holding none, and anything else is an agent we could not query — which
// is an error rather than a zero, because "no agent" and "empty agent" have
// different remedies.
func (h hostSSHAgent) Identities(ctx context.Context) (int, error) {
	res, err := h.runner.Run(ctx, execx.Command{
		Name:    "ssh-add",
		Args:    []string{"-l"},
		Timeout: sshAgentProbeTimeout,
	})
	if err != nil {
		return 0, err
	}
	switch res.ExitCode {
	case 0:
		return countLines(res.Stdout), nil
	case 1:
		return 0, nil
	default:
		return 0, fmt.Errorf("ssh-add -l exited %d", res.ExitCode)
	}
}

// countLines counts non-empty lines without retaining or copying their content.
func countLines(out []byte) int {
	n := 0
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

func findProject(f config.File, id string) (config.Project, bool) {
	for _, p := range f.Projects {
		if p.ID == id {
			return p, true
		}
	}
	return config.Project{}, false
}

func (m *Manager) run(ctx context.Context, op string, argv []string) (execx.Result, error) {
	res, err := guestexec.Run(ctx, m.guest, argv)
	switch {
	case errors.Is(err, guestexec.ErrTruncated):
		return execx.Result{}, &Error{Op: op, Kind: KindVerification, Err: err}
	case err != nil:
		return execx.Result{}, fromGuestErr(op, err)
	}
	return res, nil
}

func (m *Manager) mustRun(ctx context.Context, op string, kind ErrorKind, action string, argv []string) error {
	res, err := m.run(ctx, op, argv)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return commandError(op, kind, action, res.ExitCode)
	}
	return nil
}

// testRoot runs `test <flag> <path>` and maps its two documented exits. Any
// other exit is unverifiable and fails closed.
func (m *Manager) testRoot(ctx context.Context, op, flag, path string) (bool, error) {
	res, err := m.run(ctx, op, guestexec.RootExec("test", flag, path))
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, commandError(op, KindGuestCommand, "inspect the workspace path", res.ExitCode)
	}
}

// gitExec builds a Git argv that runs as the agent identity under the noninteractive
// environment. It is the only way this package reaches a remote.
//
// A non-empty keyPath offers that deploy key to SSH and nothing else.
// IdentitiesOnly=yes is not optional there: without it ssh also offers whatever
// else it can find, GitHub authenticates the first key valid for the account
// rather than for this repository, and the run fails as "repository not found"
// against a repository that plainly exists.
func (m *Manager) gitExec(keyPath string, args ...string) []string {
	argv := make([]string, 0, len(gitNoninteractiveEnv)+len(args)+1)
	for _, token := range gitNoninteractiveEnv {
		if keyPath != "" && strings.HasPrefix(token, gitSSHCommandVar) {
			token += " -o IdentitiesOnly=yes -i " + keyPath
		}
		argv = append(argv, token)
	}
	argv = append(argv, "git")
	return guestexec.UserExecAs(m.identity().GuestUser, append(argv, args...)...)
}

// deployKeyPath is the guest path of the project's deploy key. It is derived
// from the identity home and the already-validated project ID, the same way the
// workspace path is derived, so no operator input reaches it.
func (m *Manager) deployKeyPath(id string) string {
	return m.identity().Home + "/.ssh/torio/" + id
}

// testAsUser runs `test <flag> <path>` as the owning identity and maps its two
// documented exits. It is the identity's own view of its home, which is the
// only view that answers whether that identity holds a key.
func (m *Manager) testAsUser(ctx context.Context, op, flag, path string) (bool, error) {
	res, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "test", flag, path))
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, commandError(op, KindGuestCommand, "inspect the deploy key path", res.ExitCode)
	}
}

// ensureDeployKey generates the project's deploy key as the owning identity and
// returns its public half.
//
// The private half never leaves the guest: it is written by ssh-keygen into a
// file the identity owns, and the only file this reads back is the `.pub`. An
// empty passphrase is what makes the key usable noninteractively, which is the
// same reason the rest of this package refuses prompts.
func (m *Manager) ensureDeployKey(ctx context.Context, op, id string) (string, error) {
	keyPath := m.deployKeyPath(id)
	home := m.identity().Home
	if err := m.mustRun(ctx, op, KindGuestCommand, "create the key directory",
		guestexec.UserExecAs(m.identity().GuestUser, "mkdir", "-p", "-m", "0700", home+"/.ssh", home+"/.ssh/torio")); err != nil {
		return "", err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "generate the deploy key",
		guestexec.UserExecAs(m.identity().GuestUser, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", deployKeyComment(id), "-f", keyPath)); err != nil {
		return "", err
	}
	return m.readPublicKey(ctx, op, keyPath)
}

// readPublicKey reads the published half of a deploy key.
//
// It refuses anything that is not one plausible public key line. The file is
// guest state, and a caller is about to print it, so "whatever the guest said"
// is not good enough: an unreadable or unrecognisable file yields an error, not
// a report field carrying arbitrary guest output.
func (m *Manager) readPublicKey(ctx context.Context, op, keyPath string) (string, error) {
	res, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "cat", keyPath+".pub"))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", commandError(op, KindGuestCommand, "read the deploy key public half", res.ExitCode)
	}
	line := strings.TrimSpace(string(res.Stdout))
	if !publicKeyPattern.MatchString(line) {
		return "", &Error{Op: op, Kind: KindVerification, Err: errors.New("the generated deploy key did not read back as a public key")}
	}
	return line, nil
}

// provisionGuestGitAccess makes the identity able to fetch this checkout on its
// own, without the environment Torio wraps its own Git calls in.
//
// The include is scoped to the one workspace and the setting lives outside the
// checkout on purpose. Repository-local config would apply to the operator's
// session too, and an operator pushes through the agent forwarded by
// `project shell` (ADR-0003). Pinning the deploy key there would authenticate
// that push as the deploy key and fail after a successful login.
//
// The gitdir pattern keeps its trailing slash: Git appends `**` to a
// slash-terminated pattern, and without it the condition matches nothing a
// worktree checkout ever has.
func (m *Manager) provisionGuestGitAccess(ctx context.Context, op, id, workspace string) error {
	keyPath := m.deployKeyPath(id)
	include := keyPath + ".gitconfig"
	user := m.identity().GuestUser
	if err := m.mustRun(ctx, op, KindGuestCommand, "record the checkout SSH command",
		guestexec.UserExecAs(user, "git", "config", "-f", include, "core.sshCommand", deployKeySSHCommand(keyPath))); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "include the checkout Git config",
		guestexec.UserExecAs(user, "git", "config", "--global", "includeIf.gitdir:"+workspace+"/.path", include))
}

// deployKeyComment labels the key on the forge. It is one token by design: an
// argv token carrying spaces survives this package fine, but the value is read
// back by humans in a list of authorized keys.
func deployKeyComment(id string) string { return "torio-deploy-" + id }

// deployKeySSHCommand mirrors gitNoninteractiveEnv for the identity's own later
// fetches, so a fetch Torio did not run behaves the way one it ran does.
func deployKeySSHCommand(keyPath string) string {
	return "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes -i " + keyPath
}

// sharedModeOK reports whether a directory mode carries the setgid bit and full
// group access — the two properties that let the second identity work the tree
// and keep new files in the shared group. `stat -c %a` drops leading zeros, so
// a compliant directory always reports four digits.
func sharedModeOK(mode string) bool {
	if len(mode) != 4 {
		return false
	}
	special, group := mode[0], mode[2]
	if special < '0' || special > '7' || group < '0' || group > '7' {
		return false
	}
	return (special-'0')&2 == 2 && (group-'0')&7 == 7
}
