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
	// Drop the default socket probe: each test supplies its own socket story.
	return append(base[:len(base)-1], extra...)
}

// TestVerifySocketsAbsentDirIsNotDrift: the daemon is a separate install. A
// guest that has the custody boundary but no daemon yet is not drifted, and
// saying so would train the operator to ignore the word.
func TestVerifySocketsAbsentDirIsNotDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: exitResult(1, "", "no such file")}, // stat /run/torio-mcp
	)}
	rep, err := New(fr).VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := findCheck(t, rep, "broker_sockets")
	if !c.OK || !strings.Contains(c.Detail, "absent") {
		t.Errorf("check = %+v, want OK naming the absence", c)
	}
}

// TestVerifySocketsStaleSocketIsDrift is the gap this check exists to close. A
// socket file left behind by a broker that died satisfies every owner, group and
// mode test while refusing every connection, so ownership alone would report a
// boundary that holds on a machine where nothing runs.
func TestVerifySocketsStaleSocketIsDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/user/1000/systemd/private 1 * 0\n")},
	)}
	rep, err := New(fr).VerifyMCPBroker(context.Background())
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
		scriptedResponse{result: stdoutResult("directory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 660\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
	)}
	rep, err := New(fr).VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("live socket rejected: %v", err)
	}
	c := findCheck(t, rep, "broker_sockets")
	if !c.OK || !strings.Contains(c.Detail, "atlassian") {
		t.Errorf("check = %+v, want OK naming the listening service", c)
	}
}

// TestVerifySocketsWrongModeIsDrift: 0660 owned torio-mcp:torio-mcp-clients IS
// the access control. A widened mode hands the socket to identities the client
// group was supposed to bound.
func TestVerifySocketsWrongModeIsDrift(t *testing.T) {
	fr := &fakeRunner{script: socketScript(
		scriptedResponse{result: stdoutResult("directory\n")},
		scriptedResponse{result: stdoutResult("torio-mcp:torio-mcp-clients 750\n")},
		scriptedResponse{result: stdoutResult("atlassian.sock torio-mcp torio-mcp-clients 666\n")},
		scriptedResponse{result: stdoutResult("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n")},
	)}
	rep, err := New(fr).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("world-writable socket accepted")
	}
	if c := findCheck(t, rep, "broker_sockets"); c.OK {
		t.Error("world-writable socket recorded as OK")
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
