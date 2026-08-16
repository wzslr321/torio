package lima

import (
	"context"

	"github.com/wzslr321/torio/internal/backend"
)

// The guest layout the tests in this package are written against.
//
// It is a fixture rather than a real backend's declaration because this package
// holds no backend implementation and cannot import one: every backend imports
// `lima`, so the dependency only runs one way. What these tests cover is the
// agnostic half — the transport, the shared group, the session helpers, the MCP
// custody — and all of that takes the identity as an argument.
const (
	testUser          = "agent"
	testHome          = "/home/agent"
	testProfilePath   = "/home/agent/.agent"
	testBrainPath     = "/home/agent/brain"
	testWorkspacePath = "/home/agent/projects"
)

// testBackend is a backend that declares the fixture layout and nothing else.
// Every step is a no-op: a test that wants a step to fail drives it through the
// fake runner, not through a second implementation of the contract.
type testBackend struct{}

func newTestBackend() backend.Backend { return testBackend{} }

func (testBackend) Identity() backend.Identity {
	return backend.Identity{
		Name:          "test-agent",
		GuestUser:     testUser,
		Home:          testHome,
		ProfilePath:   testProfilePath,
		BrainPath:     testBrainPath,
		WorkspacePath: testWorkspacePath,
	}
}

func (testBackend) RequiredPaths() []backend.PathSpec {
	return []backend.PathSpec{
		{Path: testHome, Owner: testUser, Group: TorioProjectsGroup, Modes: []string{"710", "0710"}},
		{Path: testProfilePath, Owner: testUser, Group: testUser, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: testBrainPath, Owner: testUser, Group: testUser, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: testWorkspacePath, Owner: testUser, Group: TorioProjectsGroup, Modes: []string{"2770"}},
	}
}

func (testBackend) VerifyIdentity(context.Context, backend.StepRunner) error   { return nil }
func (testBackend) VerifyMembership(context.Context, backend.StepRunner) error { return nil }
func (testBackend) VerifyIsolation(context.Context, backend.StepRunner) error  { return nil }
func (testBackend) Install(context.Context, backend.StepRunner) error          { return nil }
func (testBackend) VerifyVersion(context.Context, backend.StepRunner) error    { return nil }
func (testBackend) VerifyGuardrails(context.Context, backend.StepRunner) error { return nil }
func (testBackend) ProbeAuth(context.Context, backend.StepRunner) error        { return nil }
func (testBackend) Session() *backend.SessionSpec                              { return nil }
func (testBackend) Status() *backend.StatusSpec                                { return nil }
func (testBackend) StatusChecks() backend.StatusChecks                         { return backend.StatusChecks{} }
func (testBackend) BrainSkill() backend.BrainSkill                             { return backend.BrainSkill{} }
func (testBackend) ProvisionScript() string                                    { return "# test backend provisioning\n" }

// testAgentIdentity is the backend identity the MCP custody tests drive. It is
// a literal rather than a backend's own declaration for the same reason the
// layout above is: the implementations import this package, so this package
// cannot import them. Only the name reaches validateMCPBackendIdentity, and it
// has to be one this build has a transport contract for.
func testAgentIdentity() backend.Identity {
	return backend.Identity{
		Name:      "claude-code",
		GuestUser: "claude",
		Home:      "/home/claude",
	}
}
