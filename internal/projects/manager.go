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
		return lima.Hermes()
	}
	return m.bootstrapOpts.Backend
}

// identity is the guest identity that owns the checkouts.
func (m *Manager) identity() backend.Identity { return m.backend().Identity() }

// registry is the backend's project-registration surface, nil when the backend
// declares none. A nil registry is a shape, not a failure: the checkout is
// still cloned, verified and recorded, and the report says the backend keeps no
// registry rather than reporting an absent registration as drift.
func (m *Manager) projectRegistry() backend.ProjectRegistry { return m.backend().Registry() }

// mapRegistryErr classifies a registry failure. Output that could not be parsed
// is unverifiable state; everything else is a registration the backend could
// not carry out.
func (m *Manager) mapRegistryErr(op string, err error) error {
	var re *backend.RegistryError
	if errors.As(err, &re) {
		if re.Malformed {
			return &Error{Op: op, Kind: KindVerification, Err: re.Err}
		}
		return &Error{Op: op, Kind: KindRegistration, Err: re.Err}
	}
	return fromGuestErr(op, err)
}

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
// Hermes, and only then records it in config.
//
// The order is the point. Nothing is written to config until every guest
// postcondition holds, and nothing on the guest is deleted when a later step
// fails — a half-finished add leaves a verified checkout and an archived (or
// unregistered) Hermes project, which is exactly the state a rerun finishes
// from.
//
// A remote the guest cannot read still fails closed as an auth error. For an
// SSH remote that failure carries the public half of a read-only deploy key the
// guest now holds, so the rerun that finishes the attach is the same command
// again, once the key is authorized on the forge.
func (m *Manager) Add(ctx context.Context, req AddRequest) (AddReport, error) {
	const op = "add"
	var report AddReport

	entry := config.Project{ID: req.ID, DisplayName: req.DisplayName, Remote: req.Remote}
	if err := validateProject(entry); err != nil {
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: err}
	}
	if strings.HasPrefix(req.DisplayName, "-") {
		// The display name is passed to `hermes project create` as a positional
		// argument; a leading dash would be read as a flag there. The config layer
		// has no reason to care, this boundary does.
		return report, &Error{Op: op, Kind: KindInvalidConfig, Err: errors.New("display name must not start with '-'")}
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

	keyPath, key, err := m.ensureRemoteReadable(ctx, op, req.ID, entry.Remote)
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

	if err := m.ensureRegistered(ctx, op, report.Project, &report); err != nil {
		return report, err
	}

	if err := m.persist(op, entry, req.AllowDuplicateRemote); err != nil {
		m.rollbackAfterConfigFailure(ctx, op, &report)
		return report, err
	}
	report.Registered = true

	if req.Use {
		if err := m.activate(ctx, op, report.Project); err != nil {
			report.Notes = append(report.Notes, "project_added_but_not_activated")
			return report, err
		}
		report.Activated = true
	}
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
// state of the guest checkout and of the Hermes registration. It reports drift
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

	hermes, err := m.registryStatus(ctx, op, id, workspace)
	if err != nil {
		return report, err
	}
	report.Hermes = hermes
	// Registration issues exist only where a registry does. Without this clause
	// every project on a backend that keeps none reports `hermes_project_absent`
	// forever — an issue naming a registration that was never possible, on a
	// checkout with nothing wrong with it, which is how an operator learns to
	// read the issues list as noise.
	if m.projectRegistry() != nil {
		switch {
		case hermes.Conflicts():
			report.Issues = append(report.Issues, "hermes_project_slug_conflict")
		case !hermes.Present:
			report.Issues = append(report.Issues, "hermes_project_absent")
		case hermes.Archived:
			report.Issues = append(report.Issues, "hermes_project_archived")
		}
	}
	return report, nil
}

// Use makes a registered project the active Hermes project. The project must be
// registered here and registered there: activating a slug Torio does not own
// would point Hermes at a path Torio cannot vouch for.
func (m *Manager) Use(ctx context.Context, id string) (UseReport, error) {
	const op = "use"
	var report UseReport

	entry, workspace, err := m.resolve(op, id)
	if err != nil {
		return report, err
	}
	report.Project = view(entry, workspace)

	// There is no active project to select where there is no registry to select
	// it in. Naming that is the honest answer; the registration error below would
	// tell an operator to run `project add` to fix something `add` cannot create.
	if m.projectRegistry() == nil {
		return report, &Error{Op: op, Kind: KindNoRegistry, Err: fmt.Errorf(
			"backend %q declares no project registry; there is no active project to select", m.identity().Name)}
	}
	if err := m.requirePrepared(ctx, op); err != nil {
		return report, err
	}
	hermes, err := m.registryStatus(ctx, op, id, workspace)
	if err != nil {
		return report, err
	}
	if !hermes.Registered() {
		return report, &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("no Hermes project for %q points at the derived workspace path; re-run `project add` to reconcile it", id)}
	}
	if err := m.activate(ctx, op, report.Project); err != nil {
		return report, err
	}
	return report, nil
}

// Remove forgets a project. It archives the Hermes project first and removes
// the config entry only after that succeeded, so an interruption always leaves
// the config entry behind — the marker a rerun needs to finish the removal.
// The checkout is never touched, and the report says so.
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

	hermes, err := m.registryStatus(ctx, op, id, workspace)
	if err != nil {
		return report, err
	}
	switch {
	case m.projectRegistry() == nil:
		// Nothing to archive and nothing missing. Reporting the registration as
		// absent would describe a registration that could never have existed.
	case hermes.Conflicts():
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("the Hermes project holding slug %q points elsewhere; refusing to archive it", id)}
	case !hermes.Present:
		report.HermesAbsent = true
	case hermes.Archived:
		report.HermesAlreadyArchived = true
	default:
		if err := m.archiveRegistered(ctx, op, report.Project); err != nil {
			return report, err
		}
		report.HermesArchived = true
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
	// cannot write the tree `hermes` owns — or writes files `hermes` then
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

func sessionDriftError(op, id string, checkout CheckoutStatus) error {
	return &Error{
		Op:   op,
		Kind: KindVerification,
		Err: fmt.Errorf("the checkout for %q is not in a state a session can be opened in (%s); re-run `torio project add` to reconcile it",
			id, strings.Join(checkout.issues(), ", ")),
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
	return res.ExitCode == 0, nil
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
			"the guest cannot read the remote noninteractively; for a private repository use the SSH remote, which Torio can provision a read-only deploy key for")}
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
	origin, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "git", "-C", workspace, "config", "--get", "remote.origin.url"))
	if err != nil {
		return st, err
	}
	st.OriginMatches = origin.ExitCode == 0 && strings.TrimSpace(string(origin.Stdout)) == remote

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
// and setgid of the shared-workspace layout, so `hermes` and the
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

// ensureRegistered reconciles the backend's registration for p. A backend that
// declares no registry has nothing to reconcile and the report says so.
//
// The reconcile never trusts an exit code: it reads the state, acts once, and
// re-reads to prove the state it wanted. Creating is safe only because the
// first read proved the id free — one backend silently creates `<id>-2` on a
// taken id instead of failing.
func (m *Manager) ensureRegistered(ctx context.Context, op string, p Project, report *AddReport) error {
	reg := m.projectRegistry()
	if reg == nil {
		return nil
	}
	report.RegistryDeclared = true

	st, err := m.registryStatus(ctx, op, p.ID, p.Path)
	if err != nil {
		return err
	}
	switch {
	case st.Conflicts():
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("a backend project already holds slug %q with a different primary path", p.ID)}
	case st.Registered():
		return nil
	case st.Present && st.Archived:
		// Ours, archived by an earlier `remove`. Restoring is how the backend
		// undoes that, and it touches nothing on the filesystem.
		if err := reg.Restore(ctx, m.guest, p.ID); err != nil {
			return m.mapRegistryErr(op, err)
		}
		report.HermesRestored = true
	default:
		if err := reg.Create(ctx, m.guest, p.ID, p.DisplayName, p.Path); err != nil {
			return m.mapRegistryErr(op, err)
		}
		report.HermesCreated = true
	}

	after, err := m.registryStatus(ctx, op, p.ID, p.Path)
	if err != nil {
		return err
	}
	if !after.Registered() {
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("the backend project for %q did not reach the expected state", p.ID)}
	}
	return nil
}

// archiveRegistered archives the project and confirms the result by re-reading
// it. Archiving is idempotent and never touches the filesystem, which is what
// makes it the right non-destructive undo.
func (m *Manager) archiveRegistered(ctx context.Context, op string, p Project) error {
	reg := m.projectRegistry()
	if reg == nil {
		return nil
	}
	if err := reg.Archive(ctx, m.guest, p.ID); err != nil {
		return m.mapRegistryErr(op, err)
	}
	after, err := m.registryStatus(ctx, op, p.ID, p.Path)
	if err != nil {
		return err
	}
	if after.Present && !after.Archived {
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("the backend project for %q is still active after archiving", p.ID)}
	}
	return nil
}

// activate makes the project the backend's active one.
func (m *Manager) activate(ctx context.Context, op string, p Project) error {
	reg := m.projectRegistry()
	if reg == nil {
		return &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("backend %q keeps no project registry, so it has no active project to set", m.identity().Name)}
	}
	if err := reg.Activate(ctx, m.guest, p.ID); err != nil {
		return m.mapRegistryErr(op, err)
	}
	return nil
}

// hermesStatus reads the Hermes project state for id from `show` stdout.
//
// The exit code of `show` is not evidence in either direction, and the two
// directions failed at different times.
//
// Hermes 0.19.0 exited 0 for an unknown project, writing only a stderr
// diagnostic, because upstream discarded the handler's return value. So a clean
// exit never meant the project existed. Hermes 0.19.1 fixed that and now exits
// non-zero — which broke the other half of the original reading, where a
// non-zero exit was taken to mean the CLI itself was broken. On 0.19.1 the most
// ordinary case in the product, adding the first project to a fresh VM, fails
// closed on a guest that is working perfectly.
//
// So neither exit code answers "does this project exist?", and the answer has
// to come from somewhere that is not an exit code:
//
//   - `show` printed a block — the project exists; parse it.
//   - `show` printed nothing and `list` does not name the slug — the slug is
//     free, whatever `show` exited with.
//   - `show` printed nothing and `list` does name the slug — the project exists
//     but could not be described. `show` is the only source of the primary
//     path, so this is unverifiable state and fails closed.
//   - `list` itself failed — the CLI is broken or absent. Fails closed.
//
// `list` is still never a source of *state*: its output carries slugs and
// names, never a path. It is used here only to answer an existence question,
// which is exactly what a list of slugs can answer.
func (m *Manager) registryStatus(ctx context.Context, op, id, workspace string) (HermesStatus, error) {
	reg := m.projectRegistry()
	if reg == nil {
		return HermesStatus{}, nil
	}
	st, err := reg.Status(ctx, m.guest, id, workspace)
	if err != nil {
		return HermesStatus{}, m.mapRegistryErr(op, err)
	}
	return st, nil
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

// rollbackAfterConfigFailure records what a failed config write left behind and
// undoes the Hermes registration this run made — but only in the one way Hermes
// offers that touches no files. The checkout always stays: it is verified, it
// cost a full clone, and deleting an operator's tree to tidy up a config write
// would be the destructive act this package exists to avoid. A rerun finds the
// checkout compliant, restores or recreates the Hermes project, and retries the
// write.
func (m *Manager) rollbackAfterConfigFailure(ctx context.Context, op string, report *AddReport) {
	report.Notes = append(report.Notes, "checkout_retained")
	if !report.HermesCreated && !report.HermesRestored {
		report.Notes = append(report.Notes, "hermes_project_unchanged")
		report.Notes = append(report.Notes, "rerun_finishes")
		return
	}
	if err := m.archiveRegistered(ctx, op, report.Project); err != nil {
		report.Notes = append(report.Notes, "hermes_project_left_registered")
	} else {
		report.Notes = append(report.Notes, "hermes_project_archived")
	}
	report.Notes = append(report.Notes, "rerun_finishes")
}

// sshAgentProbeTimeout bounds the agent query. A wedged agent socket blocks
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

// CheckServiceEnv reads whether the persistent Hermes backend environment
// declares SSH_AUTH_SOCK. It is the read-only counterpart of an operator
// session: the session's forwarded agent is supposed to die with it, and this
// is what notices if it did not.
//
// It reads a unit property and tests it for one variable name. The property
// value itself is never returned, printed or logged. A guest with no backend
// installed is not a failure — there is nothing to leak into — so it reports
// "not checked" rather than inventing a verdict.
func (m *Manager) CheckServiceEnv(ctx context.Context) (ServiceEnvCheck, error) {
	const op = shellOp
	var unavailable ServiceEnvCheck

	uid, err := m.run(ctx, op, guestexec.UserExecAs(m.identity().GuestUser, "id", "-u", m.identity().GuestUser))
	if err != nil || uid.ExitCode != 0 {
		return unavailable, nil
	}
	runtimeDir, ok := userRuntimeDir(string(uid.Stdout))
	if !ok {
		return unavailable, nil
	}

	service := m.backend().Service()
	if service == nil {
		// The backend runs no persistent service, so there is no long-lived
		// environment a forwarded agent could have leaked into. "Not checked" is
		// the honest answer; "clean" would claim a check that never happened.
		return unavailable, nil
	}
	show, err := m.run(ctx, op,
		guestexec.UserExecAs(m.identity().GuestUser, "env", "XDG_RUNTIME_DIR="+runtimeDir,
			"systemctl", "--user", "show", service.UnitName, "--property=Environment"))
	if err != nil || show.ExitCode != 0 {
		return unavailable, nil
	}
	if bytes.Contains(show.Stdout, []byte(agentSocketVar)) {
		check := ServiceEnvCheck{Checked: true, AgentSocketPresent: true}
		return check, &Error{
			Op:   op,
			Kind: KindVerification,
			Err: fmt.Errorf("the persistent backend service environment declares %s; ephemeral operator forwarding must never reach it (ADR-0003)",
				agentSocketVar),
		}
	}
	return ServiceEnvCheck{Checked: true}, nil
}

// agentSocketVar is the environment variable that carries a forwarded agent.
const agentSocketVar = "SSH_AUTH_SOCK"

// userRuntimeDir derives /run/user/<uid> from `id -u` output. The uid must
// parse as a number: it is interpolated into an argv, and an unparseable one is
// unverifiable state rather than a value to pass along.
func userRuntimeDir(out string) (string, bool) {
	uid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || uid < 0 {
		return "", false
	}
	return "/run/user/" + strconv.Itoa(uid), true
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

// gitExec builds a Git argv that runs as `hermes` under the noninteractive
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
// `project shell` (ADR-0003). Pinning the read-only key there would authenticate
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
