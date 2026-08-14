package lima

import (
	"context"
	"fmt"
	"testing"
)

func effectiveUnitOutput() string {
	return "FragmentPath=" + TorioMCPBrokerUnitPath + "\n" +
		"DropInPaths=\nNeedDaemonReload=no\n" +
		"User=torio-mcp\nGroup=torio-mcp-clients\nSupplementaryGroups=\nDynamicUser=no\n" +
		"Type=notify\nNotifyAccess=main\nRuntimeDirectory=torio-mcp\nRuntimeDirectoryMode=0750\n" +
		"UMask=0077\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=strict\n" +
		"ReadWritePaths=/home/torio-mcp\nAmbientCapabilities=\nRestart=on-failure\nRestartUSec=2s\n"
}

// provisionFreshIdentityScript is a guest with no broker identity yet: every
// probe says absent, every mutation succeeds.
func provisionFreshIdentityScript() []scriptedResponse {
	return []scriptedResponse{
		{result: exitResult(2, "", "")},                         // getent group torio-mcp-clients -> absent
		{result: stdoutResult("")},                              // groupadd
		{result: exitResult(1, "", "no user")},                  // id -u torio-mcp -> absent
		{result: stdoutResult("")},                              // useradd
		{result: stdoutResult("torio-mcp\n")},                   // id -nG torio-mcp -> not yet a client
		{result: stdoutResult("")},                              // usermod -aG
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")}, // verify broker membership
		{result: stdoutResult("directory\n")},                   // stat %F home (useradd made it)
		{result: stdoutResult("torio-mcp:torio-mcp 755\n")},     // stat %U:%G %a -> default mode, too open
		{result: stdoutResult("")},                              // chmod 700
		{result: stdoutResult("hermes torio-projects\n")},       // id -nG hermes -> not yet a client
		{result: stdoutResult("")},                              // usermod -aG
		{result: stdoutResult("directory\n")},                   // policy dir pre-staged by root
	}
}

func identityVerificationScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")},
		{result: stdoutResult("torio-mcp\n")},
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},
		{result: stdoutResult(sudoDeniedFixture)},
		{result: stdoutResult("1000\n")},
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult(sudoDeniedFixture)},
		// Two lines: the verification probe names statControlPath first, so a
		// present path answers with the control line and its own.
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},
	}
}

func TestProvisionMCPBrokerReportsPartialChangesWhenALaterStepFails(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(2, "", "")}, // client group is absent
		{result: stdoutResult("")},      // groupadd succeeds
		{err: fmt.Errorf("probe broker user: unavailable")},
	}}

	rep, err := New(fr).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("later provisioning failure was accepted")
	}
	if !rep.Changed {
		t.Fatal("provisioning mutated the guest before failing but reported Changed=false")
	}
}

func TestProvisionMCPBrokerReportsBrokerMembershipMutationWhenVerificationFails(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp\n")}, // broker is not a client yet
		{result: stdoutResult("")},            // usermod succeeds
		{result: stdoutResult("torio-mcp\n")}, // verification still misses the group
	}}

	rep, err := New(fr).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("missing broker membership after usermod was accepted")
	}
	if !rep.Changed {
		t.Fatal("usermod succeeded before verification failed but Changed=false")
	}
}

func TestProvisionMCPBrokerReportsHomeMutationWhenLaterReconcileFails(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},
		{result: stdoutResult("directory\n")},
		{result: stdoutResult("root:root 755\n")},
		{result: stdoutResult("")},             // chown succeeds
		{result: exitResult(1, "", "refused")}, // chmod fails
	}}

	rep, err := New(fr).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("failed home reconcile was accepted")
	}
	if !rep.Changed {
		t.Fatal("chown succeeded before chmod failed but Changed=false")
	}
}

func TestProvisionMCPBrokerReportsRestartRequiredWhenPolicyIsStillEmpty(t *testing.T) {
	script := provisionFreshIdentityScript()
	script = append(script, identityVerificationScript()...)
	script = append(script,
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("root:root 755\n")},
		scriptedResponse{result: stdoutResult("")}, // policy directory is empty
	)

	rep, err := New(&fakeRunner{script: script}).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("empty policy directory was accepted")
	}
	if !rep.RestartRequired {
		t.Fatal("hermes joined the client group before failure but RestartRequired=false")
	}
}
