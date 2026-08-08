// Package claudecode implements the Torio backend contract for Claude Code.
//
// It is a process backend, and that is the whole reason it exists: it proves
// the contract can carry an agent that is not shaped like the one Torio was
// built around. There is no daemon to install, no loopback endpoint to probe
// and no project registry to drive. A project is a directory; an operator
// reaches the agent by opening a session inside a checkout, as the agent's own
// guest identity.
//
// What the box provides that a host install cannot is the reason to run it here
// at all. On a laptop, an agent's blast radius is the operator's whole account,
// so the only available control is a prompt inside the agent's own process —
// which the agent could ignore, and which the operator clicks through anyway.
// Inside the box, the enforcement moved below the process: a uid with no sudo,
// an exact group set, no route to a Git remote, and a VM edge. Permission
// prompts are then off not as a concession but because the thing they were
// standing in for is now real.
package claudecode

import (
	"context"
	_ "embed"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/lima"
)

// New returns the Claude Code backend.
func New() backend.Backend { return claudeBackend{} }

type claudeBackend struct{}

// The guest identity and its layout. The identity is dedicated and unprivileged
// for the same reason every other one here is, and for one more that is
// specific to this backend: an interactive agent session runs *as* this user,
// so anything it holds is something the agent holds for as long as it is
// running.
const (
	// User is the dedicated non-root identity Claude Code runs as. It is never
	// the Lima login user, which holds passwordless root: an agent running as
	// that user would sit above every control the guest enforces, including the
	// credential store it must not read and the root-owned helpers that gate
	// sessions.
	User = "claude"
	// Home is the identity's home. `~/.claude` under it holds the credential,
	// the settings the agent may write, and its skills.
	Home = "/home/claude"
	// ProfilePath is the agent's own state directory. Claude Code tightens it
	// when it writes a credential, which is why the path spec accepts stricter.
	ProfilePath = "/home/claude/.claude"
	// BrainPath is the Second Brain vault.
	BrainPath = "/home/claude/brain"
	// WorkspacePath is the shared project workspace.
	WorkspacePath = "/home/claude/projects"
)

// The names of the checks `torio backend status` reads back. They are
// constants rather than literals repeated at each site so that the name a step
// records and the name the renderer looks up cannot drift apart — which is the
// only way the report can claim a credential state nobody probed.
const (
	versionCheck    = "claude_version"
	authCheck       = "claude_auth"
	mcpServersCheck = "claude_mcp_servers"
)

func (claudeBackend) Identity() backend.Identity {
	return backend.Identity{
		Name:          "claude-code",
		GuestUser:     User,
		Home:          Home,
		ProfilePath:   ProfilePath,
		BrainPath:     BrainPath,
		WorkspacePath: WorkspacePath,
	}
}

// RequiredPaths mirrors the layout every backend gets: a home the operator can
// traverse through the shared group but not read, a private profile and vault,
// and a setgid workspace both identities work in.
func (claudeBackend) RequiredPaths() []backend.PathSpec {
	return []backend.PathSpec{
		{Path: Home, Owner: User, Group: lima.TorioProjectsGroup, Modes: []string{"710", "0710"}},
		{Path: ProfilePath, Owner: User, Group: User, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: BrainPath, Owner: User, Group: User, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: WorkspacePath, Owner: User, Group: lima.TorioProjectsGroup, Modes: []string{"2770"}},
	}
}

// ProvisionScript creates the identity and its layout. Claude Code ships a
// single self-contained binary, so unlike the Hermes backend there are no build
// dependencies to add: a box running this backend carries no compiler
// toolchain.
func (claudeBackend) ProvisionScript() string {
	return `if ! id -u ` + User + ` >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash --user-group ` + User + `
fi
usermod -aG ` + lima.TorioProjectsGroup + ` ` + User + `
if id -nG ` + User + ` | tr ' ' '\n' | grep -qx docker; then
  gpasswd -d ` + User + ` docker || true
fi

chown ` + User + `:` + lima.TorioProjectsGroup + ` ` + Home + `
chmod 0710 ` + Home + `
install -d -o ` + User + ` -g ` + lima.TorioProjectsGroup + ` -m 2770 ` + WorkspacePath + `
install -d -o ` + User + ` -g ` + User + ` -m 0750 ` + ProfilePath + `
install -d -o ` + User + ` -g ` + User + ` -m 0750 ` + BrainPath + `
install -d -o root -g root -m 0755 ` + managedSettingsDir + `
install -d -o root -g root -m 0755 ` + installDir + `
`
}

// BrainSkill installs the retrieval skill into the personal skill root Claude
// Code walks, `~/.claude/skills`, where one copy is visible from every checkout
// the agent is started in.
//
// There is no category. Claude Code routes by reading each skill's description
// and deciding, so a skill is not competing for a position in a static
// alphabetical index the way it is on the other backend — which is the entire
// reason that mechanism exists there and the reason it is absent here.
//
// The payload is this backend's own text, not a shared document. It names the
// tools this agent has — `Grep`, `Glob`, `Read` — and the vault path this
// identity owns. The other backend's skill names neither, which is why
// installing it here would have told the agent to call tools it does not have
// against a directory that does not exist.
func (claudeBackend) BrainSkill() backend.BrainSkill {
	return backend.BrainSkill{Root: ProfilePath + "/skills", Payload: embeddedBrainSkill}
}

//go:embed templates/skill/SKILL.md
var embeddedBrainSkill []byte

// Registry is nil: Claude Code keeps no project registry. A project is a
// directory it is started in, and its per-project state is files inside that
// directory. There is nothing for Torio to register a checkout with, and
// nothing it should invent.
func (claudeBackend) Registry() backend.ProjectRegistry { return nil }

// Service is nil: Claude Code is a per-session process, not a daemon. There is
// no unit to install and no readiness endpoint to probe, and reporting an
// absent service as an unready one would teach an operator to ignore the state
// that means a real backend has died.
func (claudeBackend) Service() *backend.ServiceSpec { return nil }

// StatusChecks names this backend's own checks. They are prefixed with the
// guest identity rather than with the registered name `claude-code`, and the
// renderer is told which is which instead of guessing.
func (claudeBackend) StatusChecks() backend.StatusChecks {
	return backend.StatusChecks{
		Version:    versionCheck,
		Auth:       authCheck,
		MCPServers: mcpServersCheck,
	}
}

func (claudeBackend) Session() *backend.SessionSpec {
	return &backend.SessionSpec{
		HelperPath: AgentSessionHelper,
		Helper:     embeddedAgentSession,
		LoginArgv:  loginArgv(),
	}
}

// VerifyIdentity proves the dedicated identity exists.
func (claudeBackend) VerifyIdentity(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_user"
	res, err := r.Probe(ctx, name, "id", "-u", User)
	if err != nil {
		return err
	}
	uid := trimmed(res.Stdout)
	if res.ExitCode != 0 || uid == "" {
		return r.Fail(name, "claude user not found", "re-create the VM so provisioning creates the backend identity")
	}
	r.Record(name, true, "uid="+uid)
	return nil
}

// VerifyMembership proves the identity can reach the shared workspace.
func (claudeBackend) VerifyMembership(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_torio_projects"
	res, err := r.Probe(ctx, name, "id", "-nG", User)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "cannot read claude group membership", "confirm the claude user exists on the guest")
	}
	if !hasGroup(res.Stdout, lima.TorioProjectsGroup) {
		return r.Fail(name, "claude is not in torio-projects", "add claude to the torio-projects group on the guest")
	}
	r.Record(name, true, "member")
	return nil
}
