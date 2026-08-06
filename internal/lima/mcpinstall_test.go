package lima

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testMCPBrokerBinary = "broker-linux-arm64"
	testMCPRelayBinary  = "relay-linux-arm64"
)

func effectiveUnitOutput() string {
	return "FragmentPath=" + TorioMCPBrokerUnitPath + "\n" +
		"DropInPaths=\nNeedDaemonReload=no\n" +
		"User=torio-mcp\nGroup=torio-mcp-clients\nSupplementaryGroups=\nDynamicUser=no\n" +
		"Type=notify\nNotifyAccess=main\nRuntimeDirectory=torio-mcp\nRuntimeDirectoryMode=0750\n" +
		"UMask=0077\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=strict\n" +
		"ReadWritePaths=/home/torio-mcp\nAmbientCapabilities=\nRestart=on-failure\nRestartUSec=2s\n"
}

func installTestAdapter(t *testing.T, fr *fakeRunner) *Adapter {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		testProfile.MCPBrokerArtifact(): testMCPBrokerBinary,
		testProfile.MCPRelayArtifact():  testMCPRelayBinary,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	a := New(fr)
	a.MCPGuestBinaryDir = dir
	return a
}

func binaryFreshInstallScript() []scriptedResponse {
	var script []scriptedResponse
	for _, fixture := range []struct {
		target string
		body   string
	}{
		{TorioMCPBrokerPath, testMCPBrokerBinary},
		{TorioMCPRelayPath, testMCPRelayBinary},
	} {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fixture.body)))
		script = append(script,
			scriptedResponse{result: exitResult(1, "directory root:root 755\n", "no such file")},
			scriptedResponse{result: stdoutResult("")}, // clear stale staging path
			scriptedResponse{result: stdoutResult("")}, // dd staging payload
			scriptedResponse{result: stdoutResult("")}, // chmod staging
			scriptedResponse{result: stdoutResult("")}, // atomic rename
			scriptedResponse{result: stdoutResult("")}, // sync install directory
			scriptedResponse{result: stdoutResult("directory root:root 755\nregular file root:root 755\n")},
			scriptedResponse{result: stdoutResult(digest + "  " + fixture.target + "\n")},
		)
	}
	return script
}

func binarySettledInstallScript() []scriptedResponse {
	var script []scriptedResponse
	for _, fixture := range []struct {
		target string
		body   string
	}{
		{TorioMCPBrokerPath, testMCPBrokerBinary},
		{TorioMCPRelayPath, testMCPRelayBinary},
	} {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fixture.body)))
		script = append(script,
			scriptedResponse{result: stdoutResult("directory root:root 755\nregular file root:root 755\n")},
			scriptedResponse{result: stdoutResult(digest + "  " + fixture.target + "\n")},
		)
	}
	return script
}

func activeBinaryUpgradeScript() []scriptedResponse {
	settled := settledInstallScript()
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testMCPBrokerBinary)))
	upgrade := []scriptedResponse{
		{result: stdoutResult("directory root:root 755\nregular file root:root 755\n")},
		{result: stdoutResult(strings.Repeat("0", 64) + "  " + TorioMCPBrokerPath + "\n")},
		{result: stdoutResult("")}, // clear stale staging path
		{result: stdoutResult("")}, // dd staging payload
		{result: stdoutResult("")}, // chmod staging
		{result: stdoutResult("")}, // atomic rename
		{result: stdoutResult("")}, // sync install directory
		{result: stdoutResult("directory root:root 755\nregular file root:root 755\n")},
		{result: stdoutResult(digest + "  " + TorioMCPBrokerPath + "\n")},
	}

	script := append([]scriptedResponse{}, settled[:7]...)
	script = append(script, upgrade...)
	script = append(script, settled[9:]...)
	script = script[:len(script)-len(unitSettledInstallScript())-len(socketVerificationScript())]
	script = append(script,
		scriptedResponse{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		scriptedResponse{result: stdoutResult(string(mcpBrokerUnit()))},
		scriptedResponse{result: stdoutResult("")},     // systemd-analyze verify installed unit
		scriptedResponse{result: stdoutResult("no\n")}, // NeedDaemonReload
		scriptedResponse{result: stdoutResult(effectiveUnitOutput())},
		scriptedResponse{result: stdoutResult("enabled\n")}, // is-enabled
		scriptedResponse{result: stdoutResult("active\n")},  // is-active
		scriptedResponse{result: stdoutResult("4242\n")},    // old MainPID
		scriptedResponse{result: stdoutResult(strings.Repeat("0", 64) + "  /proc/4242/exe\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		scriptedResponse{result: stdoutResult("")},          // restart active broker
		scriptedResponse{result: stdoutResult("enabled\n")}, // verify enabled
		scriptedResponse{result: stdoutResult("active\n")},  // verify active
		scriptedResponse{result: stdoutResult("4243\n")},    // new MainPID
		scriptedResponse{result: stdoutResult(digest + "  /proc/4243/exe\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		scriptedResponse{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp-clients 750\n")},
	)
	script = append(script, socketVerificationScript()...)
	return script
}

func interruptedBinaryUpgradeScript() []scriptedResponse {
	settled := settledInstallScript()
	prefixLen := len(settled) - len(unitSettledInstallScript()) - len(socketVerificationScript())
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testMCPBrokerBinary)))
	script := append([]scriptedResponse{}, settled[:prefixLen]...)
	script = append(script,
		scriptedResponse{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		scriptedResponse{result: stdoutResult(string(mcpBrokerUnit()))},
		scriptedResponse{result: stdoutResult("")},     // systemd-analyze verify installed unit
		scriptedResponse{result: stdoutResult("no\n")}, // NeedDaemonReload
		scriptedResponse{result: stdoutResult(effectiveUnitOutput())},
		scriptedResponse{result: stdoutResult("enabled\n")}, // is-enabled
		scriptedResponse{result: stdoutResult("active\n")},  // is-active
		scriptedResponse{result: stdoutResult("4242\n")},    // MainPID
		scriptedResponse{result: stdoutResult(strings.Repeat("0", 64) + "  /proc/4242/exe\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		scriptedResponse{result: stdoutResult("")},          // restart stale process
		scriptedResponse{result: stdoutResult("enabled\n")}, // verify enabled
		scriptedResponse{result: stdoutResult("active\n")},  // verify active
		scriptedResponse{result: stdoutResult("4243\n")},    // new MainPID
		scriptedResponse{result: stdoutResult(digest + "  /proc/4243/exe\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		scriptedResponse{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp-clients 750\n")},
	)
	script = append(script, socketVerificationScript()...)
	return script
}

func interruptedUnitUpgradeScript() []scriptedResponse {
	settled := settledInstallScript()
	prefixLen := len(settled) - len(unitSettledInstallScript()) - len(socketVerificationScript())
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testMCPBrokerBinary)))
	script := append([]scriptedResponse{}, settled[:prefixLen]...)
	script = append(script,
		scriptedResponse{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		scriptedResponse{result: stdoutResult(string(mcpBrokerUnit()))},
		scriptedResponse{result: stdoutResult("")},      // systemd-analyze verify installed unit
		scriptedResponse{result: stdoutResult("yes\n")}, // stale systemd manager generation
		scriptedResponse{result: stdoutResult("")},      // daemon-reload
		scriptedResponse{result: stdoutResult(effectiveUnitOutput())},
		scriptedResponse{result: stdoutResult("enabled\n")}, // is-enabled
		scriptedResponse{result: stdoutResult("active\n")},  // is-active
		scriptedResponse{result: stdoutResult("4242\n")},    // old MainPID
		scriptedResponse{result: stdoutResult(digest + "  /proc/4242/exe\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		scriptedResponse{result: stdoutResult("")},          // restart with loaded unit
		scriptedResponse{result: stdoutResult("enabled\n")}, // verify enabled
		scriptedResponse{result: stdoutResult("active\n")},  // verify active
		scriptedResponse{result: stdoutResult("4243\n")},    // new MainPID
		scriptedResponse{result: stdoutResult(digest + "  /proc/4243/exe\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		scriptedResponse{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp-clients 750\n")},
	)
	script = append(script, socketVerificationScript()...)
	return script
}

func unitFreshInstallScript() []scriptedResponse {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testMCPBrokerBinary)))
	return []scriptedResponse{
		{result: exitResult(1, "directory root:root 755\n", "no such file")}, // unit stat: control path only
		{result: stdoutResult("")}, // clear stale staging path
		{result: stdoutResult("")}, // dd staging unit
		{result: stdoutResult("")}, // chmod staging unit
		{result: stdoutResult("")}, // systemd-analyze verify staging unit
		{result: stdoutResult("")}, // atomic unit install
		{result: stdoutResult("")}, // sync system unit directory
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		{result: stdoutResult(string(mcpBrokerUnit()))},
		{result: stdoutResult("")}, // daemon-reload
		{result: stdoutResult(effectiveUnitOutput())},
		{result: exitResult(1, "disabled\n", "")}, // is-enabled before activation
		{result: exitResult(3, "inactive\n", "")}, // is-active before activation
		{result: stdoutResult("")},                // enable --now
		{result: stdoutResult("enabled\n")},       // is-enabled after activation
		{result: stdoutResult("active\n")},        // is-active after activation
		{result: stdoutResult("4242\n")},          // MainPID
		{result: stdoutResult(digest + "  /proc/4242/exe\n")},
		{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp-clients 750\n")},
	}
}

func unitSettledInstallScript() []scriptedResponse {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testMCPBrokerBinary)))
	return []scriptedResponse{
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		{result: stdoutResult(string(mcpBrokerUnit()))},
		{result: stdoutResult("")},     // systemd-analyze verify installed unit
		{result: stdoutResult("no\n")}, // NeedDaemonReload
		{result: stdoutResult(effectiveUnitOutput())},
		{result: stdoutResult("enabled\n")}, // is-enabled
		{result: stdoutResult("active\n")},  // is-active
		{result: stdoutResult("4242\n")},    // MainPID
		{result: stdoutResult(digest + "  /proc/4242/exe\n")},
		{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp-clients 750\n")},
	}
}

func unitStalePolicyInstallScript() []scriptedResponse {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testMCPBrokerBinary)))
	return []scriptedResponse{
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		{result: stdoutResult(string(mcpBrokerUnit()))},
		{result: stdoutResult("")},     // systemd-analyze verify installed unit
		{result: stdoutResult("no\n")}, // NeedDaemonReload
		{result: stdoutResult(effectiveUnitOutput())},
		{result: stdoutResult("enabled\n")},                    // is-enabled
		{result: stdoutResult("active\n")},                     // is-active
		{result: stdoutResult("4242\n")},                       // MainPID
		{result: stdoutResult(digest + "  /proc/4242/exe\n")},  // running binary is current
		{result: stdoutResult(strings.Repeat("0", 64) + "\n")}, // running policy is stale
		{result: stdoutResult("")},                             // restart
		{result: stdoutResult("enabled\n")},                    // verify enabled
		{result: stdoutResult("active\n")},                     // verify active
		{result: stdoutResult("4243\n")},                       // new MainPID
		{result: stdoutResult(digest + "  /proc/4243/exe\n")},
		{result: stdoutResult(validGuestPolicyDigest() + "\n")},
		{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp-clients 750\n")},
	}
}

func identityVerificationScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")},
		{result: stdoutResult("torio-mcp\n")},
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},
		{result: exitResult(1, "", "not allowed")},
		{result: stdoutResult("1000\n")},
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: exitResult(1, "", "not allowed")},
		// Two lines: the verification probe names statControlPath first, so a
		// present path answers with the control line and its own.
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},
	}
}

func policyVerificationScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("root:root 755\n")},
		{result: stdoutResult("atlassian.json root root 644 f\n")},
		{result: stdoutResult(validGuestPolicy)},
	}
}

func socketVerificationScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
		{result: stdoutResult(validGuestPolicyDigest() + "\n")},
	}
}

// freshInstallScript is a guest with nothing provisioned yet: every probe says
// absent, every mutation succeeds, and the closing identity verification then
// sees the finished shape.
func freshInstallScript() []scriptedResponse {
	script := []scriptedResponse{
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
	script = append(script, binaryFreshInstallScript()...)
	script = append(script, identityVerificationScript()...)
	script = append(script, policyVerificationScript()...)
	script = append(script, unitFreshInstallScript()...)
	script = append(script, socketVerificationScript()...)
	return script
}

// settledInstallScript is a guest that already holds the finished shape: every
// probe answers "present", so no mutation may be issued at all.
func settledInstallScript() []scriptedResponse {
	script := []scriptedResponse{
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},          // getent group
		{result: stdoutResult("997\n")},                                     // id -u torio-mcp
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},             // id -nG torio-mcp
		{result: stdoutResult("directory\n")},                               // stat %F home
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},                 // stat %U:%G %a home
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")}, // id -nG hermes
		{result: stdoutResult("directory\n")},                               // stat %F policy dir
	}
	script = append(script, binarySettledInstallScript()...)
	script = append(script, identityVerificationScript()...)
	script = append(script, policyVerificationScript()...)
	script = append(script, unitSettledInstallScript()...)
	script = append(script, socketVerificationScript()...)
	return script
}

func TestInstallMCPBrokerProvisionsAndVerifies(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := installTestAdapter(t, fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if !rep.Changed {
		t.Error("a fresh guest was provisioned but Changed is false")
	}

	var joined []string
	for i := 0; i < fr.callCount(); i++ {
		joined = append(joined, strings.Join(fr.callArgs(i), " "))
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"groupadd --system " + TorioMCPClientsGroup,
		"useradd --system",
		"--user-group",
		"--shell /usr/sbin/nologin",
		"usermod -aG " + TorioMCPClientsGroup + " " + HermesUser,
		"chmod 700 " + TorioMCPHome,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("install never issued %q; issued:\n%s", want, all)
		}
	}
}

// The broker changes each socket to torio-mcp-clients after binding it. Linux
// permits that only when the creating identity belongs to the target group, so
// installing a broker outside its client group produces EPERM before it can
// serve anything.
func TestInstallMCPBrokerMakesBrokerAClient(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	want := "usermod -aG " + TorioMCPClientsGroup + " " + TorioMCPUser
	for i := 0; i < fr.callCount(); i++ {
		if strings.Contains(strings.Join(fr.callArgs(i), " "), want) {
			return
		}
	}
	t.Fatalf("install never issued %q", want)
}

func TestInstallMCPBrokerVerifiesBrokerClientMembershipAfterMutation(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	probes := 0
	for i := 0; i < fr.callCount(); i++ {
		if strings.Contains(strings.Join(fr.callArgs(i), " "), "id -nG "+TorioMCPUser) {
			probes++
		}
	}
	if probes != 3 {
		t.Fatalf("broker client membership probes = %d, want 3 (before and after usermod, then final boundary proof)", probes)
	}
}

func TestInstallMCPBrokerInstallsPackagedGuestBinaries(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := installTestAdapter(t, fr)
	if _, err := a.InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	for _, target := range []string{TorioMCPBrokerPath, TorioMCPRelayPath} {
		want := "dd of=" + target + ".new"
		found := false
		for i := 0; i < fr.callCount(); i++ {
			if strings.Contains(strings.Join(fr.callArgs(i), " "), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("install never staged %s with dd", target)
		}
	}
}

func TestInstallMCPBrokerRestartsAnActiveBrokerAfterBinaryUpgrade(t *testing.T) {
	fr := &fakeRunner{script: activeBinaryUpgradeScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	want := "systemctl restart " + TorioMCPBrokerUnitName
	for i := 0; i < fr.callCount(); i++ {
		if strings.Contains(strings.Join(fr.callArgs(i), " "), want) {
			return
		}
	}
	t.Fatalf("active broker was not restarted after its executable changed; want %q", want)
}

func TestInstallMCPBrokerRestartsAStaleRunningGenerationAfterInterruptedUpgrade(t *testing.T) {
	fr := &fakeRunner{script: interruptedBinaryUpgradeScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	want := "systemctl restart " + TorioMCPBrokerUnitName
	for i := 0; i < fr.callCount(); i++ {
		if strings.Contains(strings.Join(fr.callArgs(i), " "), want) {
			return
		}
	}
	t.Fatalf("stale running broker generation was not restarted; want %q", want)
}

func TestInstallMCPBrokerRestartsAStaleRunningPolicyGeneration(t *testing.T) {
	script := settledInstallScript()
	unitAt := len(script) - len(unitSettledInstallScript()) - len(socketVerificationScript())
	script = append(append(append([]scriptedResponse{}, script[:unitAt]...), unitStalePolicyInstallScript()...), socketVerificationScript()...)
	fr := &fakeRunner{script: script}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	want := "systemctl restart " + TorioMCPBrokerUnitName
	for i := 0; i < fr.callCount(); i++ {
		if strings.Contains(strings.Join(fr.callArgs(i), " "), want) {
			return
		}
	}
	t.Fatalf("stale running policy generation was not restarted; want %q", want)
}

func TestInstallMCPBrokerReloadsAndRestartsAfterInterruptedUnitUpgrade(t *testing.T) {
	fr := &fakeRunner{script: interruptedUnitUpgradeScript()}
	rep, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if !rep.Changed {
		t.Fatal("stale systemd manager generation was reconciled but Changed=false")
	}
	reloadAt, restartAt := -1, -1
	for i := 0; i < fr.callCount(); i++ {
		call := strings.Join(fr.callArgs(i), " ")
		if strings.Contains(call, "systemctl daemon-reload") {
			reloadAt = i
		}
		if strings.Contains(call, "systemctl restart "+TorioMCPBrokerUnitName) {
			restartAt = i
		}
	}
	if reloadAt < 0 || restartAt <= reloadAt {
		t.Fatalf("daemon reload and ordered restart not observed: reload=%d restart=%d", reloadAt, restartAt)
	}
}

func TestInstallMCPBrokerValidatesUnitBeforeActivation(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	verifyAt, syncAt, activateAt := -1, -1, -1
	secureStaging := false
	for i := 0; i < fr.callCount(); i++ {
		argv := strings.Join(fr.callArgs(i), " ")
		if strings.Contains(argv, "systemd-analyze verify") {
			verifyAt = i
		}
		if strings.Contains(argv, "dd of="+mcpBrokerStagingPath) && strings.Contains(argv, "oflag=excl,nofollow") {
			secureStaging = true
		}
		if strings.Contains(argv, "sync -f /etc/systemd/system") {
			syncAt = i
		}
		if strings.Contains(argv, "systemctl enable --now "+TorioMCPBrokerUnitName) {
			activateAt = i
		}
	}
	if verifyAt < 0 {
		t.Fatal("install never validated the broker unit")
	}
	if activateAt < 0 {
		t.Fatal("install never activated the broker unit")
	}
	if !secureStaging {
		t.Fatal("unit staging write was not exclusive and no-follow")
	}
	if syncAt < 0 || syncAt >= activateAt {
		t.Fatalf("system unit directory was not synced before activation: sync=%d activate=%d", syncAt, activateAt)
	}
	if verifyAt >= activateAt {
		t.Fatalf("broker unit activated at call %d before validation at call %d", activateAt, verifyAt)
	}
}

func TestInstallMCPBrokerVerifiesIdentityBoundaryBeforeActivation(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	boundaryAt, activateAt := -1, -1
	for i := 0; i < fr.callCount(); i++ {
		argv := strings.Join(fr.callArgs(i), " ")
		if strings.Contains(argv, "id -u "+TorioMCPUser) {
			boundaryAt = i
		}
		if strings.Contains(argv, "systemctl enable --now "+TorioMCPBrokerUnitName) {
			activateAt = i
		}
	}
	if boundaryAt < 0 || activateAt < 0 {
		t.Fatalf("calls did not include boundary verification and activation: boundary=%d activate=%d", boundaryAt, activateAt)
	}
	if boundaryAt >= activateAt {
		t.Fatalf("broker unit activated at call %d before identity boundary verification at call %d", activateAt, boundaryAt)
	}
}

func TestInstallMCPBrokerVerifiesPolicyBeforeActivation(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	policyAt, activateAt := -1, -1
	for i := 0; i < fr.callCount(); i++ {
		argv := strings.Join(fr.callArgs(i), " ")
		if strings.Contains(argv, "find "+TorioMCPPolicyDir) {
			policyAt = i
		}
		if strings.Contains(argv, "systemctl enable --now "+TorioMCPBrokerUnitName) {
			activateAt = i
		}
	}
	if policyAt < 0 || activateAt < 0 {
		t.Fatalf("calls did not include policy verification and activation: policy=%d activate=%d", policyAt, activateAt)
	}
	if policyAt >= activateAt {
		t.Fatalf("broker unit activated at call %d before policy verification at call %d", activateAt, policyAt)
	}
}

func TestInstallMCPBrokerVerifiesListeningSocketsAfterActivation(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	if _, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}

	activateAt, socketsAt := -1, -1
	for i := 0; i < fr.callCount(); i++ {
		argv := strings.Join(fr.callArgs(i), " ")
		if strings.Contains(argv, "systemctl enable --now "+TorioMCPBrokerUnitName) {
			activateAt = i
		}
		if strings.Contains(argv, "ss -lxH") {
			socketsAt = i
		}
	}
	if activateAt < 0 || socketsAt < 0 {
		t.Fatalf("calls did not include activation and socket verification: activate=%d sockets=%d", activateAt, socketsAt)
	}
	if socketsAt <= activateAt {
		t.Fatalf("socket verification at call %d did not follow activation at call %d", socketsAt, activateAt)
	}
}

// TestInstallMCPBrokerNeverGrantsBroaderAuthority is the check that keeps this
// installer from quietly becoming a privilege grant. torio-mcp must not join
// torio-projects (it would reach project checkouts) and hermes must not join
// torio-mcp (it would read the credential store) -- the two mistakes that would
// void ADR-0004 while leaving every other check green.
func TestInstallMCPBrokerNeverGrantsBroaderAuthority(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := installTestAdapter(t, fr)

	if _, err := a.InstallMCPBroker(context.Background()); err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	// Only the value of a group flag counts. Matching loose substrings would be
	// worse than no check at all here: "torio-mcp-clients" has "torio-mcp" as a
	// prefix, and the transport argv carries the literal "sudo", so a naive scan
	// reads the one grant this installer must make as the grant it must never
	// make, and the operator learns to ignore the failure.
	forbidden := map[string]bool{TorioProjectsGroup: true, TorioMCPUser: true, dockerGroup: true, "sudo": true, "wheel": true, "root": true}
	for i := 0; i < fr.callCount(); i++ {
		argv := fr.callArgs(i)
		for _, g := range groupArgs(argv) {
			if forbidden[g] {
				t.Errorf("install granted membership of %q: %v", g, argv)
			}
		}
	}
}

// groupArgs returns the group names an identity-mutating argv would grant. Any
// flag whose value is a group list is read; anything else is ignored, so a
// username that merely resembles a group name is not mistaken for one.
func groupArgs(argv []string) []string {
	var out []string
	for i, tok := range argv {
		switch tok {
		case "-G", "-aG", "--groups", "--append-groups":
			if i+1 < len(argv) {
				out = append(out, strings.Split(argv[i+1], ",")...)
			}
		}
	}
	return out
}

func TestInstallMCPBrokerIsIdempotent(t *testing.T) {
	fr := &fakeRunner{script: settledInstallScript()}
	a := installTestAdapter(t, fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if rep.Changed {
		t.Error("a settled guest reported Changed=true; a re-run must be a no-op")
	}
	for i := 0; i < fr.callCount(); i++ {
		argv := strings.Join(fr.callArgs(i), " ")
		for _, mutator := range []string{
			" groupadd ", " useradd ", " usermod ", " chmod ", " chown ",
			" dd ", " mv ", " rm ", " sync ",
			"systemctl daemon-reload", "systemctl enable", "systemctl restart",
		} {
			if strings.Contains(argv, mutator) {
				t.Errorf("settled guest still received a mutation: %q", argv)
			}
		}
	}
}

// TestInstallMCPBrokerDoesNotFailOnLeftoverHermesTokens is a deliberate
// asymmetry with `status`. Credentials sitting under the Hermes profile are
// exactly what the broker exists to end, but refusing to install while they are
// there is a deadlock: the operator cannot build the thing they must migrate to.
// Install proves what install created; the ongoing invariant belongs to status.
func TestInstallMCPBrokerDoesNotFailOnLeftoverHermesTokens(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := installTestAdapter(t, fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	for _, c := range rep.Checks {
		if c.Name == "hermes_mcp_tokens" {
			t.Errorf("install ran the ongoing-drift check %q; that belongs to status", c.Name)
		}
	}
}

func TestInstallMCPBrokerReportsPartialChangesWhenPolicyIsStillEmpty(t *testing.T) {
	script := freshInstallScript()
	script[12] = scriptedResponse{result: exitResult(1, "", "no such directory")}
	script = insertAt(script, 13, scriptedResponse{result: stdoutResult("")}) // create policy directory
	policyFind := 14 + len(binaryFreshInstallScript()) + len(identityVerificationScript()) + 2
	script[policyFind] = scriptedResponse{result: stdoutResult("")}
	fr := &fakeRunner{script: script}

	rep, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background())
	if err == nil {
		t.Fatal("empty policy directory was accepted")
	}
	if !rep.Changed {
		t.Fatal("install mutated the guest before failing but reported Changed=false")
	}
}

func TestProvisionMCPBrokerReportsPartialChangesWhenALaterStepFails(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(2, "", "")}, // client group is absent
		{result: stdoutResult("")},      // groupadd succeeds
		{err: fmt.Errorf("probe broker user: unavailable")},
	}}

	rep, err := installTestAdapter(t, fr).ProvisionMCPBroker(context.Background())
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

	rep, err := installTestAdapter(t, fr).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("missing broker membership after usermod was accepted")
	}
	if !rep.Changed {
		t.Fatal("usermod succeeded before verification failed but Changed=false")
	}
}

func TestInstallMCPBrokerReportsBrokerMembershipMutationWhenVerificationFails(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp\n")}, // broker is not a client yet
		{result: stdoutResult("")},            // usermod succeeds
		{result: stdoutResult("torio-mcp\n")}, // verification still misses the group
	}}

	rep, err := installTestAdapter(t, fr).InstallMCPBroker(context.Background())
	if err == nil {
		t.Fatal("missing broker membership after usermod was accepted")
	}
	if !rep.Changed {
		t.Fatal("dormant installer lost a known identity mutation before returning the verification failure")
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

	rep, err := installTestAdapter(t, fr).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("failed home reconcile was accepted")
	}
	if !rep.Changed {
		t.Fatal("chown succeeded before chmod failed but Changed=false")
	}
}

func TestProvisionMCPBrokerReportsRestartRequiredWhenPolicyIsStillEmpty(t *testing.T) {
	script := append([]scriptedResponse{}, freshInstallScript()[:13]...)
	script = append(script, identityVerificationScript()...)
	script = append(script,
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("root:root 755\n")},
		scriptedResponse{result: stdoutResult("")}, // policy directory is empty
	)

	rep, err := installTestAdapter(t, &fakeRunner{script: script}).ProvisionMCPBroker(context.Background())
	if err == nil {
		t.Fatal("empty policy directory was accepted")
	}
	if !rep.RestartRequired {
		t.Fatal("hermes joined the client group before failure but RestartRequired=false")
	}
}

// TestInstallMCPBrokerReportsRestartRequired: a long-running process does not
// gain a group by having the group database change under it. The always-on
// backend keeps the credentials it started with until it is restarted, so an
// installer that stays silent about this hands the operator a broker their agent
// cannot reach and no reason why.
func TestInstallMCPBrokerReportsRestartRequired(t *testing.T) {
	fr := &fakeRunner{script: freshInstallScript()}
	a := installTestAdapter(t, fr)

	rep, err := a.InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker: unexpected error: %v", err)
	}
	if !rep.RestartRequired {
		t.Error("hermes was newly added to the client group but RestartRequired is false")
	}

	fr2 := &fakeRunner{script: settledInstallScript()}
	rep2, err := installTestAdapter(t, fr2).InstallMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("InstallMCPBroker (settled): unexpected error: %v", err)
	}
	if rep2.RestartRequired {
		t.Error("nothing changed, yet the report demands a backend restart")
	}
}
