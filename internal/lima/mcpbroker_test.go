package lima

import (
	"context"
	"strings"
	"testing"
)

// brokerProbeArgs is the full limactl argv for the nth guest probe the broker
// verification makes. Tests pin argv verbatim: the whole point of the typed
// boundary is that the guest sees a fixed argument array, so a change here must
// be a deliberate edit, never a silent drift.
func brokerProbeArgs(command ...string) []string {
	args := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--"}
	return append(args, command...)
}

// okBrokerScript is the probe-by-probe happy path: every invariant of ADR-0022
// holds on the guest.
func okBrokerScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("997\n")},                                       // id -u torio-mcp
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},            // getent group
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},   // id -nG hermes (client)
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},   // id -nG hermes (not owner)
		{result: stdoutResult("directory\n")},                                 // stat -c %F home
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},                   // stat -c %U:%G %a home
		{result: exitResult(1, "", "stat: cannot statx '...': No such file")}, // stat mcp-tokens: absent
	}
}

func TestVerifyMCPBrokerHappyPath(t *testing.T) {
	fr := &fakeRunner{script: okBrokerScript()}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("VerifyMCPBroker: unexpected error: %v", err)
	}

	wantArgs := [][]string{
		brokerProbeArgs("id", "-u", TorioMCPUser),
		brokerProbeArgs("getent", "group", TorioMCPClientsGroup),
		brokerProbeArgs("id", "-nG", HermesUser),
		brokerProbeArgs("id", "-nG", HermesUser),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", HermesMCPTokensPath),
	}
	if fr.callCount() != len(wantArgs) {
		t.Fatalf("probe count = %d, want %d", fr.callCount(), len(wantArgs))
	}
	for i, want := range wantArgs {
		if got := fr.callArgs(i); !equalArgs(got, want) {
			t.Errorf("probe %d argv = %v, want %v", i, got, want)
		}
	}

	if len(rep.Checks) == 0 {
		t.Fatal("report recorded no checks")
	}
	for _, c := range rep.Checks {
		if !c.OK {
			t.Errorf("check %q failed on the happy path: %s", c.Name, c.Detail)
		}
	}
}

func TestVerifyMCPBrokerMissingUser(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(1, "", "no such user")}}}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("missing torio-mcp user: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "broker_user")
}

// TestVerifyMCPBrokerHermesNotClient covers the boundary from the other side: an
// installed broker that hermes cannot reach is a broken install, not a safe one.
func TestVerifyMCPBrokerHermesNotClient(t *testing.T) {
	script := okBrokerScript()
	script[2] = scriptedResponse{result: stdoutResult("hermes torio-projects\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("hermes outside torio-mcp-clients: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "hermes_broker_client")
}

// TestVerifyMCPBrokerHermesOwnsBrokerIdentity is the custody invariant of
// ADR-0022. If hermes lands in the torio-mcp group the broker's home and its
// tokens become readable by the identity the agent has a shell as, and the whole
// decision is void — so this must fail closed, loudly.
func TestVerifyMCPBrokerHermesOwnsBrokerIdentity(t *testing.T) {
	script := okBrokerScript()
	script[3] = scriptedResponse{result: stdoutResult("hermes torio-projects torio-mcp-clients torio-mcp\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("hermes in the torio-mcp group: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "hermes_not_broker_owner")
}

func TestVerifyMCPBrokerHomeIsGroupReadable(t *testing.T) {
	script := okBrokerScript()
	script[5] = scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp 750\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("group-readable broker home: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "path:"+TorioMCPHome)
}

// TestVerifyMCPBrokerLeftoverTokens is the drift check that catches somebody
// running `hermes mcp add` directly on a managed guest: credentials reappear
// under the agent's own identity, which is exactly what ADR-0022 removes.
func TestVerifyMCPBrokerLeftoverTokens(t *testing.T) {
	script := okBrokerScript()
	script[6] = scriptedResponse{result: stdoutResult("directory\n")}
	script = append(script, scriptedResponse{result: stdoutResult("xx")})
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("leftover MCP tokens under HERMES_HOME: expected a verification failure, got nil")
	}
	c := assertFailedCheck(t, rep, "hermes_mcp_tokens")

	// The count is the finding; the names are not. A service name is a weak
	// secret at best and the surrounding contract already forbids leaking guest
	// filenames, so the detail must carry a number and nothing else.
	if !strings.Contains(c.Detail, "2") {
		t.Errorf("detail %q does not report how many credential files were found", c.Detail)
	}
	for _, leak := range []string{".json", "atlassian", "/home/"} {
		if strings.Contains(c.Detail, leak) {
			t.Errorf("detail %q leaks %q; the check reports a count, never names or paths", c.Detail, leak)
		}
	}
}

// TestVerifyMCPBrokerEmptyTokensDirIsClean distinguishes "the directory exists
// but holds nothing" from "somebody put credentials back". Hermes creates the
// directory on its own, so its mere presence cannot be the finding.
func TestVerifyMCPBrokerEmptyTokensDirIsClean(t *testing.T) {
	script := okBrokerScript()
	script[6] = scriptedResponse{result: stdoutResult("directory\n")}
	script = append(script, scriptedResponse{result: stdoutResult("")})
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("empty mcp-tokens directory: unexpected error: %v", err)
	}
	for _, c := range rep.Checks {
		if c.Name == "hermes_mcp_tokens" && !c.OK {
			t.Errorf("empty mcp-tokens directory reported as drift: %s", c.Detail)
		}
	}
}

// assertFailedCheck asserts that name is the check the report failed on, and
// returns it. A report that fails must carry the failing check so the CLI can
// render a precise, redacted marker instead of a generic error.
func assertFailedCheck(t *testing.T, rep MCPBrokerReport, name string) CheckResult {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			if c.OK {
				t.Fatalf("check %q recorded as OK, want failed", name)
			}
			return c
		}
	}
	t.Fatalf("report has no check named %q; checks: %+v", name, rep.Checks)
	return CheckResult{}
}
