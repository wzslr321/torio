package lima

import (
	"context"
	"errors"
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
//
// Every `stat -c %F` probe names statControlPath first, so its reply carries one
// line for the control path and one more only if the path under test is there.
// An absent path is therefore a one-line reply, never an empty one — an empty
// reply means the probe did not run, which is a different answer.
func okBrokerScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("997\n")},                                                  // id -u torio-mcp
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},                       // getent group
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},              // id -nG hermes (client)
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},              // id -nG hermes (not owner)
		{result: stdoutResult("directory\ndirectory\n")},                                 // stat -c %F home: present
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},                              // stat -c %U:%G %a home
		{result: exitResult(1, "directory\n", "stat: cannot statx '...': No such file")}, // stat mcp-tokens: absent
		{result: exitResult(1, "directory\n", "no such file")},                           // stat /run/torio-mcp: no daemon yet
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
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, HermesMCPTokensPath),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPSocketDir),
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

// TestVerifyMCPBrokerMissingUser pins the distinction the exit-code contract
// rests on: a guest that was never provisioned is an unmet precondition, not
// drift. Both fail closed, but only one of them means somebody broke a boundary,
// and an operator who has simply not run the installer yet must not be told the
// custody guarantee was violated.
func TestVerifyMCPBrokerMissingUser(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(1, "", "no such user")}}}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("missing torio-mcp user: expected a failure, got nil")
	}
	assertFailedCheck(t, rep, "broker_user")

	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error %v is not a *lima.Error", err)
	}
	if lerr.Kind != KindNotFound {
		t.Errorf("kind = %q, want %q: an unprovisioned broker is a precondition, not drift", lerr.Kind, KindNotFound)
	}
}

// TestVerifyMCPBrokerBrokenBoundaryIsDrift is the other half of that
// distinction: a broker that exists but whose custody boundary is broken must
// classify as verification failure, so it reaches the operator as drift.
func TestVerifyMCPBrokerBrokenBoundaryIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[3] = scriptedResponse{result: stdoutResult("hermes torio-mcp\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	_, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("hermes in the torio-mcp group: expected a failure, got nil")
	}
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error %v is not a *lima.Error", err)
	}
	if lerr.Kind != KindVerificationFailed {
		t.Errorf("kind = %q, want %q", lerr.Kind, KindVerificationFailed)
	}
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
	script[6] = scriptedResponse{result: stdoutResult("directory\ndirectory\n")}
	script = insertAt(script, 7, scriptedResponse{result: stdoutResult("xx")})
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
	script[6] = scriptedResponse{result: stdoutResult("directory\ndirectory\n")}
	script = insertAt(script, 7, scriptedResponse{result: stdoutResult("")})
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

// TestVerifyMCPBrokerTokenProbeUnusableIsNotAbsence is the same fail-open shape
// as the socket probe, on the check that matters most: this one is the only
// thing that notices a credential put back under the agent's own identity.
//
// The comment this replaces reasoned "as root the only ordinary reason stat
// fails is that the path is not there" — but running as root is exactly what a
// failed `sudo -n` has not established. One sudoers change and the check goes
// green on a guest with live tokens sitting under $HERMES_HOME.
func TestVerifyMCPBrokerTokenProbeUnusableIsNotAbsence(t *testing.T) {
	script := okBrokerScript()
	script[6] = scriptedResponse{result: exitResult(1, "", "sudo: a password is required")}
	fr := &fakeRunner{script: script}

	rep, err := New(fr).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("unusable root probe accepted as an empty token store; expected the check to fail closed")
	}
	assertFailedCheck(t, rep, "hermes_mcp_tokens")
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

// insertAt splices a scripted response into the middle of a script. Probes run
// in a fixed order, so a test that needs an extra reply must place it where the
// adapter will ask for it — appending to the end silently feeds it to a later
// check instead.
func insertAt(script []scriptedResponse, i int, extra ...scriptedResponse) []scriptedResponse {
	out := make([]scriptedResponse, 0, len(script)+len(extra))
	out = append(out, script[:i]...)
	out = append(out, extra...)
	return append(out, script[i:]...)
}
