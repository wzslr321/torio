package lima

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func validGuestPolicyDigest() string {
	set, err := ParseDocuments(map[string][]byte{"atlassian.json": []byte(validGuestPolicy)})
	if err != nil {
		panic(err)
	}
	return set.Digest()
}

// brokerProbeArgs is the full limactl argv for the nth guest probe the broker
// verification makes. Tests pin argv verbatim: the whole point of the typed
// boundary is that the guest sees a fixed argument array, so a change here must
// be a deliberate edit, never a silent drift.
func brokerProbeArgs(command ...string) []string {
	args := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--"}
	return append(args, command...)
}

// okBrokerScript is the probe-by-probe happy path: every invariant of ADR-0004
// holds on the guest.
//
// Every `stat -c %F` probe names statControlPath first, so its reply carries one
// line for the control path and one more only if the path under test is there.
// An absent path is therefore a one-line reply, never an empty one — an empty
// reply means the probe did not run, which is a different answer.
func okBrokerScript() []scriptedResponse {
	return []scriptedResponse{
		{result: stdoutResult("997\n")}, // id -u torio-mcp
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")}, // getent passwd
		{result: stdoutResult("torio-mcp\n")},                                              // primary group
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},                            // all broker groups
		{result: exitResult(1, "", "not allowed")},                                         // no sudo grants
		{result: stdoutResult("1000\n")},                                                   // id -u hermes
		{result: stdoutResult("torio-mcp-clients:x:995:hermes\n")},                         // getent group
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},                // id -nG hermes (client)
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},                // id -nG hermes (not owner)
		{result: exitResult(1, "", "not allowed")},                                         // no hermes sudo grants
		{result: stdoutResult("directory\ndirectory\n")},                                   // stat -c %F home: present
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},                                // stat -c %U:%G %a home
		{result: exitResult(1, "directory\n", "stat: cannot statx '...': No such file")},   // stat mcp-tokens: absent
		{result: stdoutResult("directory\ndirectory\n")},                                   // stat %F policy dir
		{result: stdoutResult("root:root 755\n")},                                          // stat %U:%G %a policy dir
		{result: stdoutResult("atlassian.json root root 644 f\n")},                         // find policy documents
		{result: stdoutResult(validGuestPolicy)},                                           // read and parse policy document
		{result: stdoutResult("directory\nregular file\n")},                                // stat %F hermes config.yaml
		{result: stdoutResult(relayOnlyConfig)},                                            // cat config.yaml
		{result: stdoutResult("directory\ndirectory\n")},                                   // runtime is present
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},    // unit metadata
		{result: stdoutResult("enabled\n")},                                                // unit enabled
		{result: stdoutResult("active\n")},                                                 // unit active
		{result: stdoutResult(string(mcpBrokerUnit()))},                                    // exact unit content
		{result: stdoutResult(effectiveUnitOutput())},                                      // effective unit
		{result: stdoutResult("directory\ndirectory\n")},                                   // stat %F socket dir
		{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
		{result: stdoutResult(validGuestPolicyDigest() + "\n")},
	}
}

// relayOnlyConfig is a Hermes config whose every MCP server goes through the
// broker relay — the shape ADR-0004 §3 prescribes.
const relayOnlyConfig = `model:
  provider: custom
mcp_servers:
  atlassian:
    command: /usr/local/bin/torio-mcp-connect
    args: ["atlassian"]
`

const validGuestPolicy = `{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`

func TestVerifyMCPBrokerHappyPath(t *testing.T) {
	fr := &fakeRunner{script: okBrokerScript()}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("VerifyMCPBroker: unexpected error: %v", err)
	}

	wantArgs := [][]string{
		brokerProbeArgs("id", "-u", TorioMCPUser),
		brokerProbeArgs("getent", "passwd", TorioMCPUser),
		brokerProbeArgs("id", "-gn", TorioMCPUser),
		brokerProbeArgs("id", "-nG", TorioMCPUser),
		brokerProbeArgs("sudo", "-n", "-l", "-U", TorioMCPUser),
		brokerProbeArgs("id", "-u", HermesUser),
		brokerProbeArgs("getent", "group", TorioMCPClientsGroup),
		brokerProbeArgs("id", "-nG", HermesUser),
		brokerProbeArgs("id", "-nG", HermesUser),
		brokerProbeArgs("sudo", "-n", "-l", "-U", HermesUser),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, HermesMCPTokensPath),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPPolicyDir),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPPolicyDir),
		brokerProbeArgs("sudo", "-n", "find", TorioMCPPolicyDir, "-mindepth", "1", "-maxdepth", "1", "-printf", `%f %u %g %m %y\n`),
		brokerProbeArgs("sudo", "-n", "cat", "--", TorioMCPPolicyDir+"/atlassian.json"),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, HermesConfigPath),
		brokerProbeArgs("sudo", "-n", "cat", HermesConfigPath),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPSocketDir),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F %U:%G %a", "/etc/systemd/system", TorioMCPBrokerUnitPath),
		brokerProbeArgs("sudo", "-n", "systemctl", "is-enabled", TorioMCPBrokerUnitName),
		brokerProbeArgs("sudo", "-n", "systemctl", "is-active", TorioMCPBrokerUnitName),
		brokerProbeArgs("sudo", "-n", "cat", TorioMCPBrokerUnitPath),
		brokerProbeArgs(mcpBrokerEffectiveUnitShowArgs()...),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPSocketDir),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPSocketDir),
		brokerProbeArgs("sudo", "-n", "find", TorioMCPSocketDir, "-mindepth", "1", "-maxdepth", "1", "-type", "s", "-printf", `%f %u %g %m\n`),
		brokerProbeArgs("sudo", "-n", "ss", "-lxH"),
		brokerProbeArgs("sudo", "-n", "cat", torioMCPPolicyDigestPath),
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

func TestVerifyBrokerUserRejectsPrivilegedSupplementaryGroup(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")},
		{result: stdoutResult("torio-mcp\n")},
		{result: stdoutResult("torio-mcp torio-mcp-clients docker\n")},
		{result: exitResult(1, "", "not allowed")},
	}}
	rep := &MCPBrokerReport{}

	if err := New(fr).verifyBrokerUser(context.Background(), rep); err == nil {
		t.Fatal("broker identity with docker authority was accepted")
	}
}

func TestVerifyHermesCustodyRejectsDockerMembership(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("hermes torio-projects torio-mcp-clients docker\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyHermesNotBrokerOwner(context.Background(), rep); err == nil {
		t.Fatal("hermes with docker authority was accepted as unable to read broker credentials")
	}
}

func TestVerifyHermesCustodyRejectsSudoAuthority(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("hermes torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("User hermes may run the following commands\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyHermesNotBrokerOwner(context.Background(), rep); err == nil {
		t.Fatal("hermes with sudo authority was accepted as unable to read broker credentials")
	}
}

func TestVerifyBrokerUserRejectsPrivilegedNumericIdentity(t *testing.T) {
	tests := []struct {
		name   string
		uid    string
		passwd string
	}{
		{name: "root uid", uid: "0\n", passwd: "torio-mcp:x:0:989::/home/torio-mcp:/usr/sbin/nologin\n"},
		{name: "root primary gid", uid: "989\n", passwd: "torio-mcp:x:989:0::/home/torio-mcp:/usr/sbin/nologin\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := okBrokerScript()
			script[0] = scriptedResponse{result: stdoutResult(tt.uid)}
			script[1] = scriptedResponse{result: stdoutResult(tt.passwd)}
			rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
			if err == nil {
				t.Fatal("privileged numeric broker identity was accepted")
			}
			assertFailedCheck(t, rep, "broker_user")
		})
	}
}

func TestVerifyBrokerUserRejectsTheHermesUID(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")},
		{result: stdoutResult("torio-mcp\n")},
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},
		{result: exitResult(1, "", "not allowed")},
		{result: stdoutResult("997\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyBrokerUser(context.Background(), rep); err == nil {
		t.Fatal("broker identity sharing the Hermes numeric uid was accepted")
	}
}

// TestVerifyMCPBrokerBrokenBoundaryIsDrift is the other half of that
// distinction: a broker that exists but whose custody boundary is broken must
// classify as verification failure, so it reaches the operator as drift.
func TestVerifyMCPBrokerBrokenBoundaryIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[8] = scriptedResponse{result: stdoutResult("hermes torio-mcp\n")}
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
	script[7] = scriptedResponse{result: stdoutResult("hermes torio-projects\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("hermes outside torio-mcp-clients: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "hermes_broker_client")
}

// TestVerifyMCPBrokerHermesOwnsBrokerIdentity is the custody invariant of
// ADR-0004. If hermes lands in the torio-mcp group the broker's home and its
// tokens become readable by the identity the agent has a shell as, and the whole
// decision is void — so this must fail closed, loudly.
func TestVerifyMCPBrokerHermesOwnsBrokerIdentity(t *testing.T) {
	script := okBrokerScript()
	script[8] = scriptedResponse{result: stdoutResult("hermes torio-projects torio-mcp-clients torio-mcp\n")}
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
	script[11] = scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp 750\n")}
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
// under the agent's own identity, which is exactly what ADR-0004 removes.
func TestVerifyMCPBrokerLeftoverTokens(t *testing.T) {
	script := okBrokerScript()
	script[12] = scriptedResponse{result: stdoutResult("directory\ndirectory\n")}
	script = insertAt(script, 13, scriptedResponse{result: stdoutResult("xx")})
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
	script[12] = scriptedResponse{result: stdoutResult("directory\ndirectory\n")}
	script = insertAt(script, 13, scriptedResponse{result: stdoutResult("")})
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
	script[12] = scriptedResponse{result: exitResult(1, "", "sudo: a password is required")}
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
