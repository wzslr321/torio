// Package projects attaches, verifies and forgets Git repositories on the
// managed Lima guest. It is the one place that implements the attach / adopt /
// verify / register / remove invariants of ADR-0015, so no caller has to
// re-derive them.
//
// Two boundaries define the package. The workspace path is always derived from
// the project ID as /home/hermes/projects/<id> — never taken from an operator,
// never stored in config — so an attachment cannot point anywhere else on the
// guest. And Torio never stores, configures or reads Git credentials: every
// remote operation runs noninteractively as the `hermes` service user, so a
// repository Torio cannot already read fails closed instead of prompting.
// Provisioning access is a human, out-of-band act.
package projects

import (
	"path"
	"strings"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
)

const (
	// workspaceRoot is the fixed parent of every attached checkout. Bootstrap
	// already proves it exists as hermes:torio-projects 2770 on native ext4.
	workspaceRoot = lima.HermesWorkspacePath
	// sharedGroup is the group the operator and `hermes` both belong to. The
	// promoted Gate-0 operator-shell spike
	// (archive/pre-v1:docs/spike-results/v1-operator-shell-20260727T132420Z) established it as
	// the only way both identities can work one checkout without sudo — and
	// established just as firmly that `hermes` must never be in the docker group.
	sharedGroup = "torio-projects"
)

// gitNoninteractiveEnv is the env prefix every remote Git operation runs under.
// GIT_TERMINAL_PROMPT=0 refuses a credential prompt outright, and BatchMode=yes
// does the same for SSH, so a repository the guest cannot already read fails
// instead of hanging on a prompt no one is there to answer.
// StrictHostKeyChecking=accept-new is the promoted spike's setting: it pins the
// host key on first contact and refuses a changed one afterwards.
var gitNoninteractiveEnv = []string{
	"env",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
}

// Project is one attached project as this package reports it: the non-secret
// registry identity plus the path derived from the ID.
type Project struct {
	// ID is the stable slug; it also derives Path.
	ID string
	// DisplayName is the human label, and the name the Hermes project carries.
	DisplayName string
	// Remote is the validated Git remote. The config layer has already proven it
	// carries no credential, which is what makes it safe to echo.
	Remote string
	// Path is the derived guest workspace path.
	Path string
}

// AddRequest is the operator intent for one attachment.
type AddRequest struct {
	// ID is the requested slug. It is validated before any I/O happens.
	ID string
	// DisplayName is the human label.
	DisplayName string
	// Remote is the Git remote to attach.
	Remote string
	// Use activates the project in Hermes after a successful add.
	Use bool
	// AllowDuplicateRemote carries the explicit operator decision to register a
	// remote another project already uses.
	AllowDuplicateRemote bool
}

// AddReport is the outcome of an attachment. It distinguishes the three ways a
// rerun can end — fresh clone, adopted checkout, nothing to do — and records
// what survived a failure, because "what is on the guest now" is the only
// question an operator has after one.
type AddReport struct {
	Project Project
	// Cloned reports that this run created the checkout.
	Cloned bool
	// Adopted reports that this run verified and kept an existing checkout.
	Adopted bool
	// HermesCreated / HermesRestored report what this run did to the Hermes
	// project registration.
	HermesCreated  bool
	HermesRestored bool
	// Registered reports that the config registry holds the project after the
	// call. It is false on every failure path.
	Registered bool
	// Activated reports that `hermes project use` succeeded.
	Activated bool
	// Notes are bounded, non-secret state markers describing what a failure left
	// behind and what a rerun will finish (see addNote*).
	Notes []string
}

// RemoveReport is the outcome of forgetting a project. The checkout is never
// touched, and the report says so explicitly rather than leaving the operator
// to assume it either way.
type RemoveReport struct {
	Project Project
	// HermesArchived / HermesAlreadyArchived / HermesAbsent are the three
	// idempotent shapes the Hermes side can end in.
	HermesArchived        bool
	HermesAlreadyArchived bool
	HermesAbsent          bool
	// CheckoutRetained is always true: V1 has no --delete.
	CheckoutRetained bool
	// CheckoutPath is the directory that still exists after the removal.
	CheckoutPath string
	Notes        []string
}

// CheckoutStatus is the derived state of the guest checkout. It carries only
// booleans and metadata — never a file name, a diff, or raw Git output.
type CheckoutStatus struct {
	PathExists bool
	// Symlink reports that the derived path is a symlink. Torio never follows
	// one: it would move the checkout outside the derived location.
	Symlink bool
	// Directory reports that the path exists and is a directory.
	Directory bool
	// Repository reports that the path is a Git repository whose top level is
	// exactly the derived path — not a subdirectory of some other repository.
	Repository bool
	// OriginMatches reports that remote.origin.url is byte-identical to the
	// registered remote.
	OriginMatches bool
	// FullClone reports that the repository is not shallow.
	FullClone bool
	// Clean reports an empty `git status --porcelain`, untracked files included.
	Clean bool
	// NoCredentialHelper reports that no repo-local credential.* setting exists.
	NoCredentialHelper bool
	// Owner, Group and Mode are the observed directory metadata.
	Owner string
	Group string
	Mode  string
	// SharedPermissions reports hermes:torio-projects ownership with the setgid
	// bit and group rwx, so both trusted identities can work the tree.
	SharedPermissions bool
}

// compliant reports whether the checkout satisfies every attachment invariant.
func (c CheckoutStatus) compliant() bool {
	return c.PathExists && !c.Symlink && c.Directory && c.Repository &&
		c.OriginMatches && c.FullClone && c.Clean && c.NoCredentialHelper &&
		c.SharedPermissions
}

// issues names the failed invariants as stable, payload-free markers.
func (c CheckoutStatus) issues() []string {
	var out []string
	switch {
	case c.Symlink:
		out = append(out, "path_is_symlink")
	case !c.PathExists:
		out = append(out, "checkout_absent")
	case !c.Directory:
		out = append(out, "path_is_not_a_directory")
	}
	if c.PathExists && !c.Symlink && c.Directory {
		if !c.Repository {
			out = append(out, "not_a_git_repository")
		}
		if !c.OriginMatches {
			out = append(out, "origin_mismatch")
		}
		if !c.FullClone {
			out = append(out, "shallow_clone")
		}
		if !c.Clean {
			out = append(out, "worktree_dirty")
		}
		if !c.NoCredentialHelper {
			out = append(out, "repo_local_credential_helper")
		}
		if !c.SharedPermissions {
			out = append(out, "shared_permissions_missing")
		}
	}
	return out
}

// HermesStatus is the derived state of the Hermes project registration. It is
// read from `hermes project show` stdout, never from its exit code.
type HermesStatus struct {
	// Present reports that a project with our slug exists at all.
	Present bool
	// Archived reports the ` (archived)` flag on the header line.
	Archived bool
	// PrimaryMatches reports that the project's primary path is our derived path.
	PrimaryMatches bool
}

// registered reports the state `Add` must reach: present, not archived, and
// pointing at our derived path.
func (h HermesStatus) registered() bool {
	return h.Present && !h.Archived && h.PrimaryMatches
}

// conflicts reports a project holding our slug but pointing somewhere else.
// Torio never adopts, repoints or archives it.
func (h HermesStatus) conflicts() bool {
	return h.Present && !h.PrimaryMatches
}

// ShowReport is the inspected state of one attached project.
type ShowReport struct {
	Project  Project
	Checkout CheckoutStatus
	Hermes   HermesStatus
	// Issues are stable, payload-free markers for everything that does not hold.
	Issues []string
}

// UseReport is the outcome of activating a project in Hermes.
type UseReport struct {
	Project Project
}

// ShellSpec is the data an interactive operator shell needs, and nothing more.
//
// It executes nothing — no guest command, no SSH, no transport of any kind.
// The interactive transport is a separate typed runner and the
// `torio project shell` command sits on top of it; both consume this value
// rather than re-deriving the path, the identities or the requirements from
// config.
type ShellSpec struct {
	Project Project
	// Group is the shared group the operator must land in to work the checkout.
	Group string
	// Instance is the Lima instance holding the checkout.
	Instance string
	// OperatorUser is the guest login identity the shell runs as.
	OperatorUser string
	// Preconditions are the stable names of the checks the interactive command
	// must pass before opening a session. They are declared here, next to the
	// identity they apply to, and verified by the caller that owns execution.
	Preconditions []string
}

// shellPreconditions is the fixed checklist an interactive session must
// satisfy, in the order ShellPreflight proves it. It is a closed vocabulary:
// every entry is a marker a caller may report, and nothing else is.
//
// Conspicuously absent: anything about the push itself. A preflight that
// pushed to prove a session would work would be mutating a remote to answer a
// question, with a capability Torio does not have and must never acquire.
var shellPreconditions = []string{
	"vm_running",
	"operator_shell_helper",
	"shared_group_membership",
	"checkout_present",
	"origin_matches",
	"shared_permissions",
	"operator_ssh_agent",
}

// ShellSession is a preflighted operator session: the spec the interactive
// transport needs, plus the checks that were actually proven to get it.
//
// The two halves travel together on purpose. A ShellSpec alone says where a
// session would go; only a ShellSession says that it may be opened.
type ShellSession struct {
	ShellSpec
	// Verified names the preconditions this preflight proved, in the order it
	// proved them. It is drawn from shellPreconditions and nothing else.
	Verified []string
}

// ServiceEnvCheck is the read-only look at the persistent Hermes backend
// environment that follows an operator session.
//
// ADR-0015 puts write capability in the ephemeral session and nowhere else:
// the persistent `hermes` service identity must never hold SSH_AUTH_SOCK. This
// is the cheap regression detector for that invariant. It carries a verdict —
// never the environment it read.
//
// The two booleans are three states, and the middle one matters: a guest with
// no backend installed has nothing to leak into, which is neither a clean bill
// of health nor a failure.
type ServiceEnvCheck struct {
	// Checked reports that the backend unit environment was actually read.
	Checked bool
	// AgentSocketPresent reports the invariant breach: the persistent service
	// environment declares SSH_AUTH_SOCK.
	AgentSocketPresent bool
}

// Registry is the narrow config boundary the manager reads and persists the
// project registry through. It is an interface so a failing write — the case
// that must leave a rerunnable state rather than a deleted checkout — is
// testable without a filesystem.
type Registry interface {
	// Load returns the current config document.
	Load() (config.File, error)
	// Save persists the document atomically and verifies the result.
	Save(config.File) error
}

// FileRegistry is the production Registry: it resolves the same canonical
// config path the CLI does and persists through the config package's
// crash-safe write (private temp, fsync, atomic rename, read back and verify).
type FileRegistry struct {
	// Options are the resolved config inputs (--config / --state-dir plus the
	// environment accessors) the invocation was started with.
	Options config.Options
}

// Load reads the config document. A first run with no document on disk is not
// an error: it yields an empty registry the first mutation will persist.
func (r FileRegistry) Load() (config.File, error) {
	rt, err := config.Load(r.Options)
	if err != nil {
		return config.File{}, err
	}
	return rt.File, nil
}

// Save persists f to the resolved config path.
func (r FileRegistry) Save(f config.File) error {
	paths, err := config.ResolvePaths(r.Options)
	if err != nil {
		return err
	}
	return config.WriteFile(paths.ConfigFile, f)
}

var _ Registry = FileRegistry{}

// derivePath returns the guest workspace path for id, and refuses anything that
// would not land exactly one level under the workspace root.
//
// The config layer's slug validation already makes traversal impossible, so
// this is a containment assertion rather than a second validator: if the
// derived path ever stops being workspaceRoot/<id>, no caller should act on it.
func derivePath(id string) (string, error) {
	if id == "" {
		return "", errInvalidID
	}
	p := workspaceRoot + "/" + id
	if p != path.Clean(p) || path.Dir(p) != workspaceRoot || path.Base(p) != id {
		return "", errInvalidID
	}
	if strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return "", errInvalidID
	}
	return p, nil
}

// validateProject runs the config layer's own validators over p by validating a
// one-entry document. Reusing them is deliberate: the slug charset, the display
// name bounds and — above all — the rules that keep a credential out of a remote
// must have exactly one definition.
func validateProject(p config.Project) error {
	return config.File{SchemaVersion: config.ConfigSchemaVersion, Projects: []config.Project{p}}.Validate()
}

// view converts a registry entry into the reported Project.
func view(p config.Project, workspacePath string) Project {
	return Project{ID: p.ID, DisplayName: p.DisplayName, Remote: p.Remote, Path: workspacePath}
}
