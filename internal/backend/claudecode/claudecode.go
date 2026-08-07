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

// BrainSkill declares no skill root, and the reason is content rather than
// plumbing.
//
// Claude Code does discover skills, under ~/.claude/skills, and the plumbing to
// install one there is in place. What does not exist yet is a retrieval skill
// written for it: the one Torio ships names another backend's tools and another
// backend's vault path, so installing it here would tell the agent to call
// tools it does not have against a directory that does not exist. That is worse
// than installing nothing, and `brain status` says "not applicable" rather than
// reporting a missing thing.
//
// The vault is still a vault without it — a git-versioned directory the agent
// can read, in a checkout-shaped layout it already knows how to search. Writing
// the skill is a content task, and it gets its own change.
func (claudeBackend) BrainSkill() backend.BrainSkill { return backend.BrainSkill{} }

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
