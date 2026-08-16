package lima

import (
	"context"
	"strings"
	"testing"
)

// socketScript extends the settled boundary with the socket probes, so these
// tests exercise the check in the position status actually runs it.
func socketScript(extra ...scriptedResponse) []scriptedResponse {
	base := okBrokerScript()
	// Drop the default socket probes: each test supplies its own socket story.
	return append(base[:len(base)-5], extra...)
}

func TestVerifyMCPBrokerStoppedUnitIsDrift(t *testing.T) {
	base := okBrokerScript()
	script := append([]scriptedResponse{}, base[:len(base)-10]...)
	script = append(script,
		scriptedResponse{result: stdoutResult("directory root:root 755\nregular file root:root 644\n")},
		scriptedResponse{result: stdoutResult("enabled\n")},
		scriptedResponse{result: exitResult(3, "inactive\n", "")},
	)

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("stopped broker unit was accepted")
	}
	if c := findCheck(t, rep, "broker_unit"); c.OK {
		t.Fatal("stopped broker unit recorded as OK")
	}
}

func TestVerifyMCPBrokerAcceptsAbsentRuntimeWithAnInactiveDormantUnit(t *testing.T) {
	base := okBrokerScript()
	// No private OAuth session yet, so no runtime is expected to exist either:
	// this is the state between `mcp install` and the first `mcp login`.
	base[len(base)-12] = scriptedResponse{result: stdoutResult("directory root:root 755\n")}
	script := append([]scriptedResponse{}, base[:len(base)-11]...)
	script = append(script, scriptedResponse{result: exitResult(1, "directory\n", "no such file")})

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err != nil {
		t.Fatalf("absent runtime should be valid before daemon delivery: %v", err)
	}
	check := findCheck(t, rep, "broker_sockets")
	if !check.OK || !strings.Contains(check.Detail, "absent") {
		t.Fatalf("runtime check = %+v, want explicit absent success", check)
	}
}

func TestVerifyMCPBrokerRejectsRuntimeWithoutATrustedUnit(t *testing.T) {
	base := okBrokerScript()
	script := append([]scriptedResponse{}, base[:len(base)-11]...)
	script = append(script,
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("directory root:root 755\n")},
	)

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("runtime sockets without the trusted system unit were accepted")
	}
	if check := findCheck(t, rep, "broker_unit"); check.OK {
		t.Fatalf("missing trusted unit recorded as OK: %+v", check)
	}
}

func TestVerifySocketsAbsentRuntimeIsDriftForAnActiveUnit(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: exitResult(1, "directory\n", "no such file")}, // stat /run/torio-mcp
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("active broker with no runtime directory was accepted")
	}
	c := findCheck(t, rep, "broker_sockets")
	if c.OK || !strings.Contains(c.Detail, "absent") {
		t.Errorf("check = %+v, want failed check naming the absent runtime", c)
	}
}

func TestVerifySocketsRejectsAnEmptyRuntimeDirectory(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("an empty runtime directory was accepted even though the broker published no services")
	}
	if c := findCheck(t, rep, "broker_sockets"); c.OK {
		t.Fatal("empty runtime directory recorded as OK")
	}
}

// TestVerifySocketsStaleSocketIsDrift is the gap this check exists to close. A
// socket file left behind by a broker that died satisfies every owner, group and
// mode test while refusing every connection, so ownership alone would report a
// boundary that holds on a machine where nothing runs.
func TestVerifySocketsStaleSocketIsDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/user/1000/systemd/private 1 * 0\n")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("stale socket accepted; expected drift")
	}
	c := findCheck(t, rep, "broker_sockets")
	if c.OK {
		t.Fatal("stale socket recorded as OK")
	}
	if !strings.Contains(c.Detail, "atlassian") || !strings.Contains(strings.ToLower(c.Detail), "listen") {
		t.Errorf("detail %q should name the service and say nothing is listening", c.Detail)
	}
}

func TestVerifySocketsLiveSocketPasses(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err != nil {
		t.Fatalf("live socket rejected: %v", err)
	}
	c := findCheck(t, rep, "broker_sockets")
	if !c.OK || !strings.Contains(c.Detail, "atlassian") {
		t.Errorf("check = %+v, want OK naming the listening service", c)
	}
}

func TestVerifySocketsRejectsAStalePolicyGeneration(t *testing.T) {
	script := okBrokerScript()
	script[len(script)-1] = scriptedResponse{result: stdoutResult(strings.Repeat("0", 64) + "\n")}
	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("running broker with a stale effective-policy digest was accepted")
	}
	c := findCheck(t, rep, "broker_sockets")
	if c.OK || !strings.Contains(c.Detail, "policy generation") {
		t.Fatalf("check = %+v, want stale policy generation", c)
	}
}

func TestVerifySocketsRejectsAServiceSetDifferentFromParsedPolicy(t *testing.T) {
	script := okBrokerScript()
	script[len(script)-3] = scriptedResponse{result: stdoutResult("slack.sock torio-mcp torio-mcp-clients 660\n")}
	script[len(script)-2] = scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/slack.sock 9 * 0\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("listening socket set different from parsed policy was accepted")
	}
	if c := findCheck(t, rep, "broker_sockets"); c.OK {
		t.Fatalf("mismatched socket service set recorded as OK: %+v", c)
	}
}

// TestVerifySocketsWrongModeIsDrift: 0660 owned torio-mcp:torio-mcp-clients IS
// the access control. A widened mode hands the socket to identities the client
// group was supposed to bound.
func TestVerifySocketsWrongModeIsDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 666\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("world-writable socket accepted")
	}
	if c := findCheck(t, rep, "broker_sockets"); c.OK {
		t.Error("world-writable socket recorded as OK")
	}
}

// TestVerifySocketsWrongDirGroupIsDrift closes a check that reported OK on a
// guest where MCP cannot work at all.
//
// The socket is 0660 torio-mcp:torio-mcp-clients, so the agent reaches it only
// by traversing the directory above it — and at 0750 that traversal comes from
// the directory's group. A directory owned torio-mcp:torio-mcp 0750 therefore
// satisfies owner and mode while every connect from the agent fails, and `status`
// would print that the broker boundary holds.
func TestVerifySocketsWrongDirGroupIsDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("socket directory closed to the client group accepted; expected drift")
	}
	c := findCheck(t, rep, "broker_sockets")
	if c.OK {
		t.Fatal("unreachable socket directory recorded as OK")
	}
	if !strings.Contains(c.Detail, TorioMCPClientsGroup) {
		t.Errorf("detail %q should name the group the directory is missing", c.Detail)
	}
}

func TestVerifySocketsWorldTraversableDirectoryIsDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 755\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
		scriptedResponse{result: stdoutResult(validGuestPolicyDigest() + "\n")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("world-traversable broker runtime directory was accepted")
	}
	if c := findCheck(t, rep, "broker_sockets"); c.OK {
		t.Fatalf("world-traversable runtime directory recorded as OK: %+v", c)
	}
}

// TestVerifySocketsUnusableProbeIsNotAbsence: `sudo -n stat` exits non-zero for
// reasons that have nothing to do with the path — sudo wanting a password, sudo
// being gone, stat being gone. Reading any of them as "no daemon installed" is a
// security control reporting OK when it cannot tell, and one sudoers change
// turns the check green on a guest with a dead broker.
func TestVerifySocketsUnusableProbeIsNotAbsence(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: exitResult(1, "", "sudo: a password is required")},
	)}
	rep, err := New(fr).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("unusable root probe accepted as absence; expected the check to fail closed")
	}
	c := findCheck(t, rep, "broker_sockets")
	if c.OK {
		t.Fatal("unusable root probe recorded as OK")
	}
}

func findCheck(t *testing.T, rep MCPBrokerReport, name string) CheckResult {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("report has no check %q; checks: %+v", name, rep.Checks)
	return CheckResult{}
}
