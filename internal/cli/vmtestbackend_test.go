package cli

import (
	"context"

	"github.com/wzslr321/torio/internal/backend"
)

// The guest layout the `vm bootstrap` tests are written against.
const (
	vmTestBackendName = "test-agent"
	vmTestUser        = "agent"
	vmTestHome        = "/home/agent"
	vmTestProfilePath = "/home/agent/.agent"
	vmTestBrainPath   = "/home/agent/brain"
	vmTestWorkspace   = "/home/agent/projects"
)

// vmTestBackend is a registered backend whose own steps are all no-ops.
//
// It exists so the CLI tests can pin the envelope `vm bootstrap` emits without
// also pinning a real backend's probe sequence: that sequence is a property of
// the backend and is covered beside it, and duplicating it here would make
// every CLI test fail whenever a backend changed how it installs itself.
type vmTestBackend struct{}

func init() { backend.Register(vmTestBackend{}) }

func (vmTestBackend) Identity() backend.Identity {
	return backend.Identity{
		Name:          vmTestBackendName,
		GuestUser:     vmTestUser,
		Home:          vmTestHome,
		ProfilePath:   vmTestProfilePath,
		BrainPath:     vmTestBrainPath,
		WorkspacePath: vmTestWorkspace,
	}
}

func (vmTestBackend) RequiredPaths() []backend.PathSpec {
	return []backend.PathSpec{
		{Path: vmTestHome, Owner: vmTestUser, Group: "torio-projects", Modes: []string{"710", "0710"}},
		{Path: vmTestProfilePath, Owner: vmTestUser, Group: vmTestUser, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: vmTestBrainPath, Owner: vmTestUser, Group: vmTestUser, Modes: []string{"750", "0750"}, AllowStricter: true},
		{Path: vmTestWorkspace, Owner: vmTestUser, Group: "torio-projects", Modes: []string{"2770"}},
	}
}

func (vmTestBackend) VerifyIdentity(context.Context, backend.StepRunner) error   { return nil }
func (vmTestBackend) VerifyMembership(context.Context, backend.StepRunner) error { return nil }
func (vmTestBackend) VerifyIsolation(context.Context, backend.StepRunner) error  { return nil }
func (vmTestBackend) Install(context.Context, backend.StepRunner) error          { return nil }
func (vmTestBackend) VerifyVersion(context.Context, backend.StepRunner) error    { return nil }
func (vmTestBackend) VerifyGuardrails(context.Context, backend.StepRunner) error { return nil }
func (vmTestBackend) ProbeAuth(context.Context, backend.StepRunner) error        { return nil }
func (vmTestBackend) Session() *backend.SessionSpec                              { return nil }
func (vmTestBackend) Status() *backend.StatusSpec                                { return nil }
func (vmTestBackend) StatusChecks() backend.StatusChecks                         { return backend.StatusChecks{} }
func (vmTestBackend) BrainSkill() backend.BrainSkill                             { return backend.BrainSkill{} }
func (vmTestBackend) ProvisionScript() string                                    { return "# test backend provisioning\n" }
