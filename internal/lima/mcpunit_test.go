package lima

import (
	"context"
	"strings"
	"testing"
)

// The runtime directory is the access path to every broker socket. At 0750 its
// group is what lets hermes traverse it, so the unit must set both the mode and
// the client group explicitly rather than accepting systemd's defaults.
func TestMCPBrokerUnitCreatesTheClientRuntimeDirectory(t *testing.T) {
	u := string(mcpBrokerUnit())
	for _, want := range []string{
		"Type=notify",
		"User=torio-mcp",
		"Group=torio-mcp-clients",
		"RuntimeDirectory=torio-mcp",
		"RuntimeDirectoryMode=0750",
		"ExecStart=/usr/local/bin/torio-mcp-broker",
		"ReadWritePaths=/home/torio-mcp",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(u, want+"\n") {
			t.Errorf("broker unit is missing %q:\n%s", want, u)
		}
	}
}

func TestVerifyMCPBrokerUnitRejectsRootOwnedContentDrift(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		{result: stdoutResult("enabled\n")},
		{result: stdoutResult("active\n")},
		{result: stdoutResult("[Service]\nUser=root\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyMCPBrokerUnit(context.Background(), rep); err == nil {
		t.Fatal("root-owned but altered broker unit was accepted")
	}
	assertFailedCheck(t, *rep, "broker_unit")
}

func TestVerifyMCPBrokerUnitRejectsEffectiveDropIns(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		{result: stdoutResult("enabled\n")},
		{result: stdoutResult("active\n")},
		{result: stdoutResult(string(mcpBrokerUnit()))},
		{result: stdoutResult("FragmentPath=" + TorioMCPBrokerUnitPath + "\nDropInPaths=/run/systemd/system/torio-mcp-broker.service.d/50-User.conf\nNeedDaemonReload=no\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyMCPBrokerUnit(context.Background(), rep); err == nil {
		t.Fatal("an effective runtime override of the root-owned unit was accepted")
	}
	assertFailedCheck(t, *rep, "broker_unit")
}

func TestVerifyMCPBrokerUnitRejectsAnEffectiveSecurityOverride(t *testing.T) {
	effective := strings.Replace(effectiveUnitOutput(), "ProtectSystem=strict", "ProtectSystem=full", 1)
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		{result: stdoutResult("enabled\n")},
		{result: stdoutResult("active\n")},
		{result: stdoutResult(string(mcpBrokerUnit()))},
		{result: stdoutResult(effective)},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyMCPBrokerUnit(context.Background(), rep); err == nil {
		t.Fatal("an effective ProtectSystem override was accepted")
	}
	assertFailedCheck(t, *rep, "broker_unit")
}
