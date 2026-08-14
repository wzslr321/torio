// Package codex implements the Torio backend contract for the Codex CLI.
//
// It is the second process backend and the first one written against a contract
// that already existed. Claude Code was designed alongside ADR-0009, so it could
// not test the claim that a further backend costs an implementation package and
// one registration; this package is that test, and it is deliberately unlike its
// predecessor in the places that matter. Codex keeps its configuration in TOML
// rather than JSON, it reads a system layer under `/etc/codex` rather than one
// managed file, and it publishes release archives with no checksum of their own.
//
// What each of those changes is that the control an operator relies on has to be
// found again rather than assumed. The answer this package settles on is the
// same shape as before: a dedicated uid with no sudo, an exact group set, a
// pinned root-owned binary, and every control in a file the agent cannot write.
// What is proven is what the backend declares, and nothing else.
package codex

import (
	"context"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/lima"
)

// New returns the Codex backend.
func New() backend.Backend { return codexBackend{} }

type codexBackend struct{}

// The guest identity and its layout. An interactive session runs as this uid, so
// whatever it holds is what the agent holds for as long as it is running.
const (
	// User is the dedicated non-root identity Codex runs as. It is never the
	// Lima login user, which holds passwordless root.
	User = "codex"
	// Home is the identity's home.
	Home = "/home/codex"
	// ProfilePath is `CODEX_HOME`: the credential, the agent's own
	// configuration, its session records and the skills it discovers all live
	// under it.
	ProfilePath = "/home/codex/.codex"
	// BrainPath is the Second Brain vault.
	BrainPath = "/home/codex/brain"
	// WorkspacePath is the shared project workspace.
	WorkspacePath = "/home/codex/projects"
)

// The names of the checks `torio backend status` reads back. They are constants
// so the name a step records and the name the renderer looks up cannot drift,
// which is the only way a report could claim a state nobody probed.
const (
	versionCheck    = "codex_version"
	authCheck       = "codex_auth"
	mcpServersCheck = "codex_mcp_servers"
)

func (codexBackend) Identity() backend.Identity {
	return backend.Identity{
		Name:          "codex",
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
func (codexBackend) RequiredPaths() []backend.PathSpec {
	return []backend.PathSpec{
		{Path: Home, Owner: User, Group: lima.TorioProjectsGroup, Modes: []string{"710", "0710"}},
		{Path: ProfilePath, Owner: User, Group: User, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: BrainPath, Owner: User, Group: User, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: WorkspacePath, Owner: User, Group: lima.TorioProjectsGroup, Modes: []string{"2770"}},
	}
}

// ProvisionScript creates the identity and its layout. Codex ships one
// self-contained binary and needs no toolchain; jq is the one runtime dependency
// Torio adds, for the root-owned hook helper to select a bounded session
// identifier out of the JSON document Codex writes to a hook's standard input.
func (codexBackend) ProvisionScript() string {
	return `apt-get install -y --no-install-recommends jq

if ! id -u ` + User + ` >/dev/null 2>&1; then
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
install -d -o root -g root -m 0755 ` + systemConfigDir + `
install -d -o root -g root -m 0755 ` + installDir + `
`
}

// systemConfigDir is the system configuration layer Codex reads on Linux. It is
// root-owned, and provisioning creates it so the guardrail step has somewhere to
// write before anything has run as the agent.
const systemConfigDir = "/etc/codex"

// Registry is nil: Codex keeps no project registry. A project is a directory it
// is started in, and there is nothing for Torio to register a checkout with.
func (codexBackend) Registry() backend.ProjectRegistry { return nil }

// Service is nil: Codex is a per-session process, not a daemon. Reporting an
// absent service as an unready one would teach an operator to ignore the state
// that means a real backend has died.
func (codexBackend) Service() *backend.ServiceSpec { return nil }

// StatusChecks names this backend's own checks. They are prefixed with the guest
// identity rather than with the registered name, and the renderer is told which
// is which instead of guessing.
func (codexBackend) StatusChecks() backend.StatusChecks {
	return backend.StatusChecks{
		Version:    versionCheck,
		Auth:       authCheck,
		MCPServers: mcpServersCheck,
	}
}

// VerifyIdentity proves the dedicated identity exists.
func (codexBackend) VerifyIdentity(ctx context.Context, r backend.StepRunner) error {
	const name = "codex_user"
	res, err := r.Probe(ctx, name, "id", "-u", User)
	if err != nil {
		return err
	}
	uid := trimmed(res.Stdout)
	if res.ExitCode != 0 || uid == "" {
		return r.Fail(name, "codex user not found", "re-create the VM so provisioning creates the backend identity")
	}
	r.Record(name, true, "uid="+uid)
	return nil
}

// VerifyMembership proves the identity can reach the shared workspace.
func (codexBackend) VerifyMembership(ctx context.Context, r backend.StepRunner) error {
	const name = "codex_torio_projects"
	res, err := r.Probe(ctx, name, "id", "-nG", User)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "cannot read codex group membership", "confirm the codex user exists on the guest")
	}
	if !hasGroup(res.Stdout, lima.TorioProjectsGroup) {
		return r.Fail(name, "codex is not in "+lima.TorioProjectsGroup,
			"add codex to the "+lima.TorioProjectsGroup+" group on the guest")
	}
	r.Record(name, true, "member")
	return nil
}
