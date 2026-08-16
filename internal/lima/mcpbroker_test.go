package lima

import (
	"context"
	"errors"
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
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")},        // getent passwd
		{result: stdoutResult("torio-mcp\n")},                                                     // primary group
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},                                   // all broker groups
		{result: stdoutResult(sudoDeniedFixture)},                                                 // no sudo grants
		{result: stdoutResult("1000\n")},                                                          // id -u the agent
		{result: stdoutResult("torio-mcp-clients:x:995:claude\n")},                                // getent group
		{result: stdoutResult("claude torio-projects torio-mcp-clients\n")},                       // id -nG agent (client)
		{result: stdoutResult("claude torio-projects torio-mcp-clients\n")},                       // id -nG agent (not owner)
		{result: stdoutResult(sudoDeniedFixture)},                                                 // no agent sudo grants
		{result: stdoutResult("directory\ndirectory\n")},                                          // stat -c %F home: present
		{result: stdoutResult("torio-mcp:torio-mcp 700\n")},                                       // stat -c %U:%G %a home
		{result: stdoutResult("directory\ndirectory\n")},                                          // stat %F policy dir
		{result: stdoutResult("root:root 755\n")},                                                 // stat %U:%G %a policy dir
		{result: stdoutResult("atlassian.json root root 644 f\n")},                                // find policy documents
		{result: stdoutResult(validGuestPolicy)},                                                  // read and parse policy document
		{result: stdoutResult("regular file root:root 644\n")},                                    // stat managed settings
		{result: stdoutResult(`{"allowManagedMcpServersOnly":true}`)},                             // cat managed settings
		{result: stdoutResult("regular file root:root 644\n")},                                    // stat managed MCP
		{result: stdoutResult(managedRelayConfig)},                                                // cat managed MCP
		{result: exitResult(1, "directory\n", "agent config absent")},                             // agent-owned config
		{result: stdoutResult("directory root:root 755\ndirectory torio-mcp:torio-mcp 700\n")},    // private OAuth dir
		{result: stdoutResult("directory root:root 755\nregular file torio-mcp:torio-mcp 600\n")}, // atlassian session
		{result: stdoutResult("directory\ndirectory\n")},                                          // runtime is present
		{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},           // unit metadata
		{result: stdoutResult("enabled\n")},                                                       // unit enabled
		{result: stdoutResult("active\n")},                                                        // unit active
		{result: stdoutResult(string(mcpBrokerUnit()))},                                           // exact unit content
		{result: stdoutResult(effectiveUnitOutput())},                                             // effective unit
		{result: stdoutResult("directory\ndirectory\n")},                                          // stat %F socket dir
		{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
		{result: stdoutResult(validGuestPolicyDigest() + "\n")},
	}
}

// managedRelayConfig is a root-owned managed MCP document whose every server
// goes through the broker relay — the shape ADR-0004 §3 prescribes.
const managedRelayConfig = `{"mcpServers":{"atlassian":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["atlassian"],"env":{}}}}`

const validGuestPolicy = `{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`

// TestVerifyMCPBrokerProbesCustodyInOrder pins the argv and the order of the
// custody half of the boundary — the identities, their groups, their absence of
// sudo, and the broker's home. That prefix is the same on every backend, which
// is why it is pinned here rather than beside one backend's own config checks.
func TestVerifyMCPBrokerProbesCustodyInOrder(t *testing.T) {
	agent := testAgentIdentity()
	fr := &fakeRunner{script: okBrokerScript()}

	if _, err := New(fr).VerifyMCPBrokerFor(context.Background(), agent); err != nil {
		t.Fatalf("VerifyMCPBrokerFor: unexpected error: %v", err)
	}

	wantArgs := [][]string{
		brokerProbeArgs("id", "-u", TorioMCPUser),
		brokerProbeArgs("getent", "passwd", TorioMCPUser),
		brokerProbeArgs("id", "-gn", TorioMCPUser),
		brokerProbeArgs("id", "-nG", TorioMCPUser),
		brokerProbeArgs(sudoProbeArgv(TorioMCPUser)...),
		brokerProbeArgs("id", "-u", agent.GuestUser),
		brokerProbeArgs("getent", "group", TorioMCPClientsGroup),
		brokerProbeArgs("id", "-nG", agent.GuestUser),
		brokerProbeArgs("id", "-nG", agent.GuestUser),
		brokerProbeArgs(sudoProbeArgv(agent.GuestUser)...),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPHome),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%F", statControlPath, TorioMCPPolicyDir),
		brokerProbeArgs("sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPPolicyDir),
		brokerProbeArgs("sudo", "-n", "find", TorioMCPPolicyDir, "-mindepth", "1", "-maxdepth", "1", "-printf", `%f %u %g %m %y\n`),
		brokerProbeArgs("sudo", "-n", "cat", "--", TorioMCPPolicyDir+"/atlassian.json"),
	}
	if fr.callCount() < len(wantArgs) {
		t.Fatalf("probe count = %d, want at least %d", fr.callCount(), len(wantArgs))
	}
	for i, want := range wantArgs {
		if got := fr.callArgs(i); !equalArgs(got, want) {
			t.Errorf("probe %d argv = %v, want %v", i, got, want)
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

	rep, err := a.VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
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
		{result: stdoutResult(sudoDeniedFixture)},
	}}
	rep := &MCPBrokerReport{}

	if err := New(fr).verifyBrokerUserFor(context.Background(), rep, testUser); err == nil {
		t.Fatal("broker identity with docker authority was accepted")
	}
}

func TestVerifyAgentCustodyRejectsDockerMembership(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("claude torio-projects torio-mcp-clients docker\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyAgentNotBrokerOwner(context.Background(), rep, testUser); err == nil {
		t.Fatal("an agent with docker authority was accepted as unable to read broker credentials")
	}
}

func TestVerifyAgentCustodyRejectsSudoAuthority(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("claude torio-projects torio-mcp-clients\n")},
		{result: stdoutResult("User claude may run the following commands\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyAgentNotBrokerOwner(context.Background(), rep, testUser); err == nil {
		t.Fatal("an agent with sudo authority was accepted as unable to read broker credentials")
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
			rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
			if err == nil {
				t.Fatal("privileged numeric broker identity was accepted")
			}
			assertFailedCheck(t, rep, "broker_user")
		})
	}
}

func TestVerifyBrokerUserRejectsTheAgentUID(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("997\n")},
		{result: stdoutResult("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n")},
		{result: stdoutResult("torio-mcp\n")},
		{result: stdoutResult("torio-mcp torio-mcp-clients\n")},
		{result: stdoutResult(sudoDeniedFixture)},
		{result: stdoutResult("997\n")},
	}}
	rep := &MCPBrokerReport{}
	if err := New(fr).verifyBrokerUserFor(context.Background(), rep, testUser); err == nil {
		t.Fatal("broker identity sharing the agent numeric uid was accepted")
	}
}

// TestVerifyMCPBrokerBrokenBoundaryIsDrift is the other half of that
// distinction: a broker that exists but whose custody boundary is broken must
// classify as verification failure, so it reaches the operator as drift.
func TestVerifyMCPBrokerBrokenBoundaryIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[8] = scriptedResponse{result: stdoutResult("claude torio-mcp\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	_, err := a.VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("the agent in the torio-mcp group: expected a failure, got nil")
	}
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error %v is not a *lima.Error", err)
	}
	if lerr.Kind != KindVerificationFailed {
		t.Errorf("kind = %q, want %q", lerr.Kind, KindVerificationFailed)
	}
}

// TestVerifyMCPBrokerAgentNotClient covers the boundary from the other side: an
// installed broker the agent cannot reach is a broken install, not a safe one.
func TestVerifyMCPBrokerAgentNotClient(t *testing.T) {
	script := okBrokerScript()
	script[7] = scriptedResponse{result: stdoutResult("claude torio-projects\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("the agent outside torio-mcp-clients: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "claude_broker_client")
}

// TestVerifyMCPBrokerAgentOwnsBrokerIdentity is the custody invariant of
// ADR-0004. If the agent lands in the torio-mcp group the broker's home and its
// tokens become readable by the identity the agent has a shell as, and the whole
// decision is void — so this must fail closed, loudly.
func TestVerifyMCPBrokerAgentOwnsBrokerIdentity(t *testing.T) {
	script := okBrokerScript()
	script[8] = scriptedResponse{result: stdoutResult("claude torio-projects torio-mcp-clients torio-mcp\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("the agent in the torio-mcp group: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "claude_not_broker_owner")
}

func TestVerifyMCPBrokerHomeIsGroupReadable(t *testing.T) {
	script := okBrokerScript()
	script[11] = scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp 750\n")}
	fr := &fakeRunner{script: script}
	a := New(fr)

	rep, err := a.VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("group-readable broker home: expected a verification failure, got nil")
	}
	assertFailedCheck(t, rep, "path:"+TorioMCPHome)
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
