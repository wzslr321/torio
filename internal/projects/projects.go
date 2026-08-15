// Package projects attaches, verifies and forgets Git repositories on the
// managed Lima guest. It is the one place that implements the attach / adopt /
// verify / register / remove invariants of ADR-0003, so no caller has to
// re-derive them.
//
// Two boundaries define the package. The workspace path is always derived from
// the project ID as /home/hermes/projects/<id> — never taken from an operator,
// never stored in config — so an attachment cannot point anywhere else on the
// guest. Host Git credentials never enter this package: every remote operation
// runs noninteractively as the `hermes` service user, so a repository the guest
// cannot read fails closed instead of prompting.
//
// A private SSH remote is the ordinary case of that, so an unreadable one is
// not the end of the attach. The guest generates its own deploy key, keeps the
// private half, and reports the public half for the operator to authorize
// without write access on the forge (ADR-0018). Torio never reads, transports
// or stores that private half, and authorizing the key stays a human act.
package projects

import (
	"path"
	"regexp"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/config"
)

const (
	// sharedGroup is the group the operator and `hermes` both belong to, and the
	// only way found to let both identities work one checkout without sudo:
	// workspaceRoot is 2770 hermes:torio-projects, so setgid puts every file
	// created below it in the group and both members can write it. Membership is
	// deliberately narrow — the operator's login identity and `hermes`, never
	// "every uid >= 500", which on Ubuntu also sweeps in systemd dynamic users.
	// It is also why `hermes` is never in the docker group: that group is
	// root-equivalent, so it would give the agent the whole guest.
	sharedGroup = "torio-projects"
)

// gitNoninteractiveEnv is the env prefix every remote Git operation runs under.
// GIT_TERMINAL_PROMPT=0 refuses a credential prompt outright, and BatchMode=yes
// does the same for SSH, so a repository the guest cannot already read fails
// instead of hanging on a prompt no one is there to answer.
// StrictHostKeyChecking=accept-new pins the host key on first contact and
// refuses a changed one afterwards.
var gitNoninteractiveEnv = []string{
	"env",
	"GIT_TERMINAL_PROMPT=0",
	gitSSHCommandVar + "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
}

// gitSSHCommandVar is the assignment prefix a deploy key is appended to, named
// once so the keyed and keyless environments cannot disagree about it.
const gitSSHCommandVar = "GIT_SSH_COMMAND="

// publicKeyPattern is the one shape a generated public key may have before this
// package will carry it into a report. It is deliberately narrow: the value
// comes back from a guest file and ends up printed, so anything else is treated
// as unverifiable rather than passed along.
//
// The comment field is restricted to printable ASCII rather than to anything
// that is not a line break. The file is writable by the backend identity, which
// is the identity the agent runs as, so a comment carrying terminal control
// sequences would let guest-side content decide how the operator's terminal
// renders the rest of the failure.
var publicKeyPattern = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp[0-9]+) [A-Za-z0-9+/=]+( [ -~]*)?$`)

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
	// RegistryDeclared reports that the backend keeps a project registry at
	// all. When it is false the two fields below are meaningless rather than
	// false-and-alarming: nothing was registered because there was nowhere to
	// register it.
	RegistryDeclared bool
	// HermesCreated / HermesRestored report what this run did to the backend's
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
	// DeployKey is the guest-held key this run provisioned for an SSH
	// remote the guest could not read, nil whenever no key was involved. It is
	// set on the failure path as well: the public half is the whole point of
	// that failure, because authorizing it is what a rerun needs.
	DeployKey *DeployKey
}

// DeployKey is the public face of a guest-held deploy key. Every field is safe
// to print. The private half stays in the guest file the identity owns, and
// nothing in this package reads it.
type DeployKey struct {
	// PublicKey is the one line of `<keypath>.pub`, the half meant to be
	// published.
	PublicKey string
	// Host is the SSH host the key has to be authorized on, taken from the
	// recorded remote and already validated as a hostname.
	Host string
	// KeyPath is the guest path of the private half, reported so an operator can
	// find it, never read.
	KeyPath string
	// Generated distinguishes a key this run created from one it found already
	// on the guest. The second case means the authorization on the forge is what
	// is still missing, so a rerun alone will not change the outcome.
	Generated bool
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

// SetRemoteReport is the outcome of correcting one recorded remote (ADR-0023).
//
// The record is the part that always moves. The checkout is repointed only
// where its origin still matched the remote being replaced, because Torio does
// not repoint a working tree it cannot vouch for, and Notes says which of those
// happened.
type SetRemoteReport struct {
	Project Project
	// PreviousRemote is what the record held before this correction.
	PreviousRemote string
	// CheckoutRepointed reports that the guest checkout's origin was moved to
	// the corrected remote. False covers every reason it was not: no checkout,
	// a box that is not running, or an origin that matched neither remote.
	CheckoutRepointed bool
	Notes             []string
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

// HermesStatus is the derived state of the backend's project registration. It
// is the contract's registry status under the name the JSON envelope has always
// used for it; a backend that declares no registry leaves it zero, which
// `RegistryDeclared` is what distinguishes from "declared and absent".
type HermesStatus = backend.RegistryStatus

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

// EnterSpec is the data an ordinary interactive project session needs. It is
// deliberately separate from ShellSpec: this session never carries the
// operator's SSH agent or Git remote write capability.
type EnterSpec struct {
	Project       Project
	Group         string
	Instance      string
	OperatorUser  string
	Preconditions []string
}

var enterPreconditions = []string{
	"vm_running",
	"project_enter_helper",
	"shared_group_membership",
	"checkout_present",
	"origin_matches",
	"shared_permissions",
}

// EnterSession is an ordinary workspace session whose preconditions were
// proven without inspecting or forwarding the host SSH agent.
type EnterSession struct {
	EnterSpec
	Verified []string
	// Review is what the checkout looked like at this moment, on the same terms
	// as ShellSession.Review: description, never verification.
	Review ReviewContext
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
	// Review is what the checkout looked like at this moment. It is description,
	// not verification: it is deliberately absent from Verified and from
	// shellPreconditions, and nothing refuses a session because of it.
	Review ReviewContext
}

// ReviewContext is the state of a checkout when a session was opened: the two
// facts an operator would otherwise open the session and type `git status` and
// `git diff` to learn.
//
// It is a snapshot and says so everywhere it is shown. Torio does not watch the
// checkout during the session and makes no claim about what is pushed out of it,
// which is the same refusal `reportShellEnd` already prints.
type ReviewContext struct {
	// Branch is the checked-out branch, empty on a detached HEAD.
	Branch string
	// Ahead is how many commits Branch leads its upstream by. AheadKnown
	// reports whether there was an upstream to count against at all: zero
	// commits ahead and no upstream configured are different facts, and only one
	// of them means there is nothing to push.
	Ahead      int
	AheadKnown bool
}

// ServiceEnvCheck is the read-only look at the persistent Hermes backend
// environment that follows an operator session.
//
// ADR-0003 puts write capability in the ephemeral session and nowhere else:
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
	// Update applies one project-registry mutation atomically.
	Update(func(config.File) (config.File, error)) error
}

// FileRegistry is the production Registry: it resolves the same canonical
// config path the CLI does and persists through the config package's
// crash-safe write (private temp, fsync, atomic rename, read back and verify).
type FileRegistry struct {
	// Options are the resolved config inputs (--config plus the environment
	// accessors) the invocation was started with.
	Options config.Options
}

// Load reads the instance document and overlays the resolved project registry
// on it. A first run with neither on disk is not an error: it yields an empty
// registry the first mutation will persist.
//
// The overlay is where the migration lives, and config.ResolveRegistry owns
// which document currently holds the projects. What matters here is that the
// registry never comes from the instance document this call just loaded: that
// document is the instance's own — its backend, its settings — and reading a
// registry out of it is what made switching instances switch projects.
func (r FileRegistry) Load() (config.File, error) {
	rt, err := config.Load(r.Options)
	if err != nil {
		return config.File{}, err
	}
	projects, err := config.ResolveRegistry(rt.Paths)
	if err != nil {
		return config.File{}, err
	}
	rt.File.Projects = projects
	return rt.File, nil
}

// Save persists the registry to the shared document.
//
// Only the projects are written. The rest of f is the instance's own document,
// which this path has no business rewriting: a project addition must not be
// what re-persists a backend declaration or a timeout.
func (r FileRegistry) Save(f config.File) error {
	paths, err := config.ResolvePaths(r.Options)
	if err != nil {
		return err
	}
	return config.WriteRegistryForPaths(paths, f.Projects)
}

// Update applies a project mutation while the shared registry is locked.
func (r FileRegistry) Update(update func(config.File) (config.File, error)) error {
	paths, err := config.ResolvePaths(r.Options)
	if err != nil {
		return err
	}
	return config.UpdateRegistry(paths, func(projects []config.Project) ([]config.Project, error) {
		next, err := update(config.File{SchemaVersion: config.ConfigSchemaVersion, Projects: projects})
		if err != nil {
			return nil, err
		}
		return next.Projects, nil
	})
}

var _ Registry = FileRegistry{}

// derivePath returns the guest workspace path for id, and refuses anything that
// would not land exactly one level under the workspace root.
//
// The config layer's slug validation already makes traversal impossible, so
// this is a containment assertion rather than a second validator: if the
// derived path ever stops being the backend workspace root + /<id>, no caller
// should act on it.
func derivePath(workspaceRoot, id string) (string, error) {
	if id == "" || workspaceRoot == "" {
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
