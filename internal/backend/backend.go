// Package backend states what Torio requires of an agent backend, separately
// from any implementation of it.
//
// Torio's boundary is not agent-specific: a VM whose state lives on its own
// native filesystem, a dedicated non-root guest identity that owns the
// checkouts, write capability against an origin that arrives only with an
// operator session, and every claim proven by a fail-closed probe rather than
// assumed. Anything that runs as a guest identity and reads a filesystem fits
// inside it. What differs between agents is narrower than it looks: how the
// binary is installed and pinned, how it reports a version, whether it keeps a
// project registry, whether it runs as a service, and how an operator opens a
// session with it.
//
// So the contract is those five things plus the identity that owns them, and
// three of them are declarable: a backend that keeps no project registry, runs
// no service, or offers no interactive session says so, and Torio reports that
// as a state rather than inventing a failure. Verification stays honest in both
// directions — whatever a backend declares, `vm bootstrap` and `serve status`
// must be able to prove; whatever it declares it has not got, they must not
// pretend to check.
//
// This package holds no guest mechanics of its own. It imports the transport
// result type and nothing else, so an implementation can live wherever its
// guest-side machinery already does.
package backend

import (
	"context"
	"strconv"

	"github.com/wzslr321/torio/internal/execx"
)

// Identity is the guest identity a backend owns and the paths that belong to
// it. Every path is absolute and every one of them is proven during bootstrap
// against RequiredPaths.
//
// The identity is never the Lima login user. That user holds passwordless root
// on the guest, so an agent running as it would sit above every control the
// guest enforces — the group set, the absent sudo, the file ownership that
// separates a credential store from the process that must not read it.
type Identity struct {
	// Name is the registry key and the value stored in an instance's config
	// (`hermes`, `claude-code`).
	Name string
	// GuestUser is the dedicated non-root guest identity.
	GuestUser string
	// Home is the identity's home directory.
	Home string
	// ProfilePath is the backend's own state directory. Empty when the backend
	// keeps no state Torio distinguishes from the home.
	ProfilePath string
	// BrainPath is the Second Brain vault.
	BrainPath string
	// WorkspacePath is the shared project workspace.
	WorkspacePath string
}

// PathSpec is one required guest path and the ownership and mode bootstrap
// proves it has.
type PathSpec struct {
	Path  string
	Owner string
	Group string
	// Modes are the accepted `stat -c %a` spellings (0710 may appear as 710).
	Modes []string
	// AllowStricter accepts a mode granting strictly less than the spec. Set it
	// only where the surrendered permission is one nothing outside the owning
	// identity uses: an agent that tightens its own credential directory on
	// first write must not thereby leave the box permanently unbootstrapped.
	AllowStricter bool
}

// StepRunner is the bootstrap run handed to a backend. It exists so a backend's
// steps cannot acquire their own transport, their own truncation policy, or
// their own idea of what a recorded check is: probes fail closed on truncated
// output exactly as every other bootstrap probe does, and a failure is recorded
// in the same report the operator reads.
type StepRunner interface {
	// Probe runs a fixed argv on the guest and returns a usable, non-truncated
	// result. Transport failures and truncated output are returned as errors
	// the caller must propagate, never interpreted.
	Probe(ctx context.Context, name string, argv ...string) (execx.Result, error)
	// ProbeInput is Probe with a fed standard input, for writing a generated
	// file through a filter rather than as an argv element.
	ProbeInput(ctx context.Context, name string, stdin []byte, argv []string) (execx.Result, error)
	// Record records a passing check and its short derived detail.
	Record(name string, ok bool, detail string)
	// Fail records a failed check and returns the fail-closed error carrying an
	// actionable remediation.
	Fail(name, detail, remediation string) error
	// PinnedVersion is the version pin the caller wants enforced for this run,
	// empty when unpinned. A backend that carries its own pin enforces that one
	// regardless.
	PinnedVersion() string
	// Reconcile reports whether this run may repair what it finds. It is false
	// for a caller that is only asking what is true — `torio backend status` —
	// and a step that would install, link or write anything must instead fail
	// with a remediation naming `torio vm bootstrap`.
	//
	// It exists because the alternative was a status command that downloaded a
	// pinned binary and rewrote a root-owned settings file while its own help
	// text said it changed nothing. Verification does not need repair; only
	// bootstrap does.
	Reconcile() bool
}

// Backend is the contract. The bootstrap hooks run in the order they are
// declared here, interleaved with the checks Torio owns for every backend (the
// shared group exists, the operator is in it and the session carries it, the
// architecture matches, git is present, the required paths resolve on a native
// filesystem, no host share is mounted, the session helpers are root-owned).
type Backend interface {
	// Identity describes the guest identity and its paths.
	Identity() Identity
	// RequiredPaths are the guest directories bootstrap proves.
	RequiredPaths() []PathSpec

	// VerifyIdentity proves the guest identity exists.
	VerifyIdentity(ctx context.Context, r StepRunner) error
	// VerifyMembership proves the identity can reach the shared workspace.
	VerifyMembership(ctx context.Context, r StepRunner) error
	// VerifyIsolation proves the identity holds no authority beyond its own
	// work: not the docker group, and whatever else the backend must not have.
	VerifyIsolation(ctx context.Context, r StepRunner) error
	// Install reconciles the pinned install and proves the pin. It is
	// idempotent: a reconciled guest is verified and left alone.
	Install(ctx context.Context, r StepRunner) error
	// VerifyVersion proves the documented stable command path answers with a
	// recognizable version. A clean exit is not proof.
	VerifyVersion(ctx context.Context, r StepRunner) error
	// VerifyGuardrails checks the files that shape the backend's own behaviour
	// but are honoured by it rather than enforced against it. Drift is
	// reported, never repaired, and never described as a boundary.
	VerifyGuardrails(ctx context.Context, r StepRunner) error
	// ProbeAuth reports offline whether the backend holds a credential. It
	// never makes a network call and never fails bootstrap: a box must
	// bootstrap before anyone can log in to it.
	ProbeAuth(ctx context.Context, r StepRunner) error

	// Registry is the backend's project-registration surface, nil when it
	// declares none.
	Registry() ProjectRegistry
	// Service is the backend's guest service, nil when it declares none.
	Service() *ServiceSpec
	// Session is the backend's interactive session, nil when it declares none.
	Session() *SessionSpec

	// StatusChecks names the bootstrap checks `torio backend status` reads
	// back out of the report.
	StatusChecks() StatusChecks

	// BrainSkill is where this backend discovers skills, so Torio can install
	// the vault's retrieval skill somewhere the agent will actually find it.
	BrainSkill() BrainSkill

	// ProvisionScript is the guest identity and directory layout this backend
	// needs, as plain shell substituted into the VM template at creation. It
	// runs as root on every boot, so every step must be idempotent, and it is
	// the only place a backend shapes the guest before bootstrap can verify it.
	ProvisionScript() string
}

// StatusChecks names the bootstrap checks the status renderer reads back. A
// backend declares them because a check name belongs to the steps that record
// it, and is not derivable from the name the backend is registered under.
//
// Deriving it was tried and was wrong in the way that matters. The renderer
// built `<identity name>_auth`, which happened to be what the first backend
// called its check and was not what the second one called anything — so a box
// that had proven it held a credential reported that nobody could ask. That is
// the failure this contract exists to prevent, arriving through the reporting
// layer instead of through a probe.
//
// An empty name is a declaration, not a gap, exactly as a nil Registry is: the
// backend records no such check. The renderer must say so rather than read an
// absent check as a negative answer.
type StatusChecks struct {
	// Version is the check whose detail is the installed version.
	Version string
	// Auth is the check whose detail answers whether a credential is held.
	// Empty when the backend has no offline way to ask, which is reported as
	// not-applicable and never as logged out.
	Auth string
	// MCPServers is the check listing the configured MCP servers by name.
	// Empty when the backend is not an MCP client.
	MCPServers string
}

// The two details an auth check may record. They are constants shared by the
// backend that writes one and the renderer that reads it, and the renderer
// compares by equality.
//
// The first version recovered the state from the prose by searching for
// "present" in a detail beginning "credent" — so "credential not present", the
// most natural rewording of the second constant, would have reported a
// credential the box does not hold. A check name is already shared as a
// constant; the answer it records is worth no less.
//
// Neither carries a remediation. What an operator should do about a logged-out
// box is the CLI's to say, and it says it from the state rather than by
// appending a sentence a comparison then has to tolerate.
const (
	CredentialPresent = "credential present"
	CredentialAbsent  = "credential absent"
)

// Transport is the one-shot guest command channel a capability surface uses.
// It is declared here rather than imported so this package stays free of guest
// mechanics; *lima.Adapter satisfies it.
type Transport interface {
	SSH(ctx context.Context, command []string) (execx.Result, error)
}

// RegistryStatus is what a backend's project registry reports about one
// project. It is deliberately small, and it carries no path: the caller already
// knows the path it asked about, and an answer that repeated it back would put
// a guest path into every report that only needed a yes.
type RegistryStatus struct {
	// Present reports that a project with our id exists at all.
	Present bool
	// Archived reports that the registry holds it in an archived state.
	Archived bool
	// PrimaryMatches reports that the project's primary path is the workspace
	// path the caller asked about.
	PrimaryMatches bool
}

// Registered reports the state a successful attach must reach: present, not
// archived, and pointing at the derived workspace path.
func (s RegistryStatus) Registered() bool {
	return s.Present && !s.Archived && s.PrimaryMatches
}

// Conflicts reports that the id is taken by a project pointing somewhere else —
// something Torio did not create and must not touch.
func (s RegistryStatus) Conflicts() bool {
	return s.Present && !s.PrimaryMatches
}

// RegistryError is a registry answer Torio must not act on. Malformed
// separates the two faults a caller has to treat differently: output that could
// not be parsed is unverifiable state, while everything else is a registration
// the backend could not carry out.
type RegistryError struct {
	Malformed bool
	Err       error
}

func (e *RegistryError) Error() string { return e.Err.Error() }
func (e *RegistryError) Unwrap() error { return e.Err }

// ProjectRegistry is a backend's own record of the projects it works on.
//
// Every implementation must prove state from what the backend reports, not
// from an exit code, unless it has a documented exit-code contract worth
// trusting. Torio has been burned precisely here: one backend release exited 0
// for an unknown project and the next exited non-zero, so a reading that was
// correct against either was wrong against the other.
type ProjectRegistry interface {
	// Status reads the registry state for id.
	Status(ctx context.Context, t Transport, id, workspace string) (RegistryStatus, error)
	// Create registers id against workspace under the given display name.
	Create(ctx context.Context, t Transport, id, displayName, workspace string) error
	// Restore reactivates an archived project.
	Restore(ctx context.Context, t Transport, id string) error
	// Archive archives the project. It never touches the checkout.
	Archive(ctx context.Context, t Transport, id string) error
	// Activate makes the project the backend's active one.
	Activate(ctx context.Context, t Transport, id string) error
}

// ServiceSpec is a backend that runs as a guest service: a user systemd unit
// bound to guest loopback, with an unauthenticated readiness endpoint.
type ServiceSpec struct {
	// UnitName is the user unit Torio owns for the backend.
	UnitName string
	// UnitDir is the directory the unit is installed into.
	UnitDir string
	// RenderUnit produces the exact unit bytes. It is deterministic and locked
	// by a golden test so the loopback bind cannot drift off loopback by hand.
	RenderUnit func() []byte
	// BindHost and BindPort are the loopback address the service binds. They are
	// declared rather than discovered so a probe and the generated unit can
	// never disagree about where the service is, and so neither can be widened
	// to a public bind by anything short of editing this declaration.
	BindHost string
	BindPort int
	// StatusPath is the unauthenticated readiness endpoint's path.
	StatusPath string
	// ParseReady extracts the version from a readiness response body. A
	// parseable version proves the probe reached the real endpoint and not an
	// unrelated listener that accepted the socket.
	ParseReady func(body []byte) (version string, ok bool)
}

// EndpointURL is the loopback readiness URL probed on the guest.
func (s *ServiceSpec) EndpointURL() string {
	return "http://" + s.BindHost + ":" + strconv.Itoa(s.BindPort) + s.StatusPath
}

// BrainSkill is a backend's skill-discovery layout and the retrieval skill it
// gets installed there.
//
// Root is the directory the backend walks for skills. An empty Root means the
// backend discovers none, and Torio installs none: the vault is still a vault,
// and `brain status` reports that it has no retrieval surface rather than
// reporting one as missing.
//
// Category groups the skill inside that root, and exists because one backend
// renders a static, alphabetically ordered skill index whose position decides
// whether a skill is seen at all. A backend that has no such index leaves it
// empty and the skill sits directly under Root.
//
// Payload is the SKILL.md the backend gets, and it belongs to the backend for
// the same reason the session helper does: a retrieval skill is instructions
// addressed to one particular agent, naming the tools that agent has and the
// vault path that agent's identity owns. There is no backend-neutral text to
// share — a single payload would have to name one backend's tools, and
// installing it into another would tell that agent to call tools it does not
// have. So the skill travels with the backend, and a backend that has a Root
// but no Payload installs nothing rather than something wrong.
//
// CategoryPayload is the category description, and is required exactly when
// Category is set: a category directory without one is a heading with no text
// under it in the index the model reads.
type BrainSkill struct {
	Root            string
	Category        string
	Payload         []byte
	CategoryPayload []byte
}

// Installable reports whether there is both somewhere to put the retrieval
// skill and something to put there. Both halves are the backend's own
// declaration, so this is the one condition every caller asks.
func (s BrainSkill) Installable() bool {
	return s.Root != "" && len(s.Payload) > 0
}

// BrainSkillName is the directory the retrieval skill is installed in, and the
// name inside its own frontmatter. It is one name across every backend because
// it identifies one product surface: an operator reading two boxes' guests
// should not have to learn that the same skill is called something else on each,
// and a backend that renamed it would silently stop matching what documentation
// and error messages tell the operator to look for.
const BrainSkillName = "torio-brain"

// SessionSpec is a backend an operator opens an interactive session with,
// inside a checkout, as the backend's own identity.
type SessionSpec struct {
	// HelperPath is the root-owned guest entry point of the session. The remote
	// argv is this path plus one validated project path — never a command the
	// host composed.
	HelperPath string
	// Helper is the embedded helper content bootstrap installs when the path is
	// absent and proves on every run.
	Helper []byte
	// LoginArgv is the fixed guest command that starts the backend outside a
	// project, so an operator can grant it a credential. It is a constant argv
	// built from the backend's own identity and install path — no caller value
	// reaches it, which is why it needs no root-owned helper to launder it.
	// Empty when the backend takes no credential of its own.
	LoginArgv []string

	// PushHelperPath and PushHelper are the second entry point: a session that
	// may ask to push, taking the project path plus the socket of the mediated
	// agent (ADR-0015). Both empty when the backend offers no such session, and
	// a backend that offers one is not thereby offering it by default — the
	// operator asks for it per session.
	//
	// It is a separate helper on purpose. The ordinary one is provably free of
	// SSH_AUTH_SOCK, which is a guarantee worth more than the duplication costs.
	PushHelperPath string
	PushHelper     []byte
}
