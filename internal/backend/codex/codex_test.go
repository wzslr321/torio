package codex

import (
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/lima"
)

// TestIdentityIsTheRegisteredNameAndItsOwnLayout pins the two things every
// other surface derives from: the registry key, which becomes the instance
// suffix an operator types, and the guest paths the whole contract is proven
// against.
func TestIdentityIsTheRegisteredNameAndItsOwnLayout(t *testing.T) {
	id := New().Identity()
	for _, tc := range []struct{ got, want, what string }{
		{id.Name, "codex", "registered name"},
		{id.GuestUser, "codex", "guest user"},
		{id.Home, "/home/codex", "home"},
		{id.ProfilePath, "/home/codex/.codex", "profile"},
		{id.BrainPath, "/home/codex/brain", "vault"},
		{id.WorkspacePath, "/home/codex/projects", "workspace"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %q, want %q", tc.what, tc.got, tc.want)
		}
	}
}

// TestRequiredPathsKeepTheProfilePrivateAndTheWorkspaceShared pins the layout a
// bootstrap proves. The home is traversable through the shared group and not
// readable through it; the profile holds the credential and is the identity's
// own; the workspace is setgid so both identities keep working in it.
func TestRequiredPathsKeepTheProfilePrivateAndTheWorkspaceShared(t *testing.T) {
	specs := map[string]backend.PathSpec{}
	for _, s := range New().RequiredPaths() {
		specs[s.Path] = s
	}

	home, ok := specs[Home]
	if !ok {
		t.Fatal("the home is not a required path")
	}
	if home.Group != lima.TorioProjectsGroup {
		t.Errorf("home group is %q, want the shared project group", home.Group)
	}

	profile, ok := specs[ProfilePath]
	if !ok {
		t.Fatal("the profile is not a required path")
	}
	if profile.Owner != User || profile.Group != User {
		t.Errorf("profile is %s:%s, want the identity to own it alone", profile.Owner, profile.Group)
	}
	// Codex writes its credential into this directory and may tighten it doing
	// so. A box that then failed to bootstrap would be unbootstrappable exactly
	// once an operator had logged into it.
	if !profile.AllowStricter {
		t.Error("the profile must accept a stricter mode; the agent tightens it when it writes a credential")
	}

	workspace, ok := specs[WorkspacePath]
	if !ok {
		t.Fatal("the workspace is not a required path")
	}
	if workspace.Group != lima.TorioProjectsGroup {
		t.Errorf("workspace group is %q, want the shared project group", workspace.Group)
	}
	if len(workspace.Modes) == 0 || !strings.HasPrefix(workspace.Modes[0], "2") {
		t.Errorf("workspace modes %v, want setgid so new checkouts keep the group", workspace.Modes)
	}
}

// TestDeclaredCapabilitiesMatchWhatCodexHas pins the declarations the contract
// reads as answers rather than as gaps. Codex keeps no registry Torio can
// register a checkout with and runs no service to probe; declaring either would
// make `serve status` and `project add` invent a failure.
func TestDeclaredCapabilitiesMatchWhatCodexHas(t *testing.T) {
	b := New()
	if b.Registry() != nil {
		t.Error("codex declares a project registry it does not keep")
	}
	if b.Service() != nil {
		t.Error("codex declares a guest service it does not run")
	}
	checks := b.StatusChecks()
	if checks.Version != versionCheck || checks.Auth != authCheck || checks.MCPServers != mcpServersCheck {
		t.Errorf("status checks %+v do not name this package's own checks", checks)
	}
}

// TestProvisionScriptCreatesEveryPathBootstrapLaterProves pins the one place a
// backend shapes the guest before anything can verify it. It runs as root on
// every boot, so each step has to be idempotent, and anything it forgets is a
// path bootstrap then fails on with no way forward but re-creating the VM.
func TestProvisionScriptCreatesEveryPathBootstrapLaterProves(t *testing.T) {
	script := New().ProvisionScript()

	for _, want := range []string{Home, ProfilePath, BrainPath, WorkspacePath, systemConfigDir, installDir} {
		if !strings.Contains(script, want) {
			t.Errorf("provisioning never mentions %s, which bootstrap then proves", want)
		}
	}
	if !strings.Contains(script, "jq") {
		t.Error("provisioning does not install jq, which the waiting marker helper needs")
	}
	if !strings.Contains(script, "id -u "+User) {
		t.Error("provisioning creates the identity unconditionally; it runs on every boot")
	}
	if !strings.Contains(script, lima.TorioProjectsGroup) {
		t.Error("provisioning never joins the shared project group")
	}
	// The guest image ships docker. An agent identity in that group can mount
	// the host filesystem into a container, which is root by another name.
	if !strings.Contains(script, "docker") {
		t.Error("provisioning does not remove the docker group membership")
	}
}
