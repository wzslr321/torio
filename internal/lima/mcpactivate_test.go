package lima

import (
	"context"
	"strings"
	"testing"
)

func TestActivateMCPBrokerWaitsUntilEveryPolicyServiceHasPrivateOAuthState(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}, {Name: "linear"}}}
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp 700\n")},
		{result: stdoutResult("directory root:root 755\nregular file torio-mcp:torio-mcp 600\n")},
		{result: exitResult(1, "directory root:root 755\n", "absent")},
	}}

	rep, err := New(fr).activateMCPBrokerForGrant(context.Background(), grant)
	if err != nil {
		t.Fatalf("activateMCPBrokerForGrant: %v", err)
	}
	if rep.Activated || rep.Pending != 1 {
		t.Fatalf("activation = %+v, want inactive with one pending service", rep)
	}
	for i := 0; i < fr.callCount(); i++ {
		if strings.Contains(strings.Join(fr.callArgs(i), " "), "systemctl") {
			t.Fatal("unit was started before every OAuth session existed")
		}
	}
}

func TestActivateMCPBrokerEnablesUnitOnlyAfterPrivateOAuthStateVerifies(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp 700\n")},
		{result: stdoutResult("directory root:root 755\nregular file torio-mcp:torio-mcp 600\n")},
		{result: stdoutResult("")},
		{result: stdoutResult("")},
		{result: stdoutResult("active\n")},
	}}
	rep, err := New(fr).activateMCPBrokerForGrant(context.Background(), grant)
	if err != nil {
		t.Fatalf("activateMCPBrokerForGrant: %v", err)
	}
	if !rep.Activated || rep.Pending != 0 {
		t.Fatalf("activation = %+v, want active", rep)
	}
	want := "sudo -n systemctl enable " + TorioMCPBrokerUnitName
	if got := strings.Join(fr.callArgs(2), " "); !strings.Contains(got, want) {
		t.Fatalf("activation argv = %q, want %q", got, want)
	}
}

func TestInstallRuntimeReconcileStopsAnActiveUnitWhileOAuthIsPending(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(1, "directory root:root 755\n", "oauth absent")},
		{result: stdoutResult("enabled\n")},
		{result: stdoutResult("active\n")},
		{result: stdoutResult("")},
	}}
	rep := &MCPBrokerInstallReport{}
	changed, err := New(fr).reconcileMCPRuntimeAfterInstall(context.Background(), grant, false, rep)
	if err != nil {
		t.Fatalf("reconcileMCPRuntimeAfterInstall: %v", err)
	}
	if !changed {
		t.Fatal("active unit was stopped but changed=false")
	}
	if got := strings.Join(fr.callArgs(3), " "); !strings.Contains(got, "systemctl disable --now "+TorioMCPBrokerUnitName) {
		t.Fatalf("stop argv = %q", got)
	}
}

func TestOAuthPendingDoesNotReadAnUnusablePrivilegedProbeAsAbsence(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}
	_, err := New(&fakeRunner{script: []scriptedResponse{{result: exitResult(1, "", "sudo refused")}}}).mcpOAuthPending(context.Background(), grant)
	if err == nil {
		t.Fatal("unusable root stat was accepted as an absent OAuth directory")
	}
}
