package lima

import (
	"context"
	"strings"
	"testing"
)

// policyProbeIndex is where the policy-directory probes sit in the verification
// order, and configProbeIndex where the agent-config probes do. Both are pinned
// rather than searched for: the checks run in a fixed order and a test that
// guessed would feed its reply to a different check.
const (
	policyProbeIndex = 13
	configProbeIndex = 17
)

func TestVerifyPolicyDocumentsRejectsAnEmptyGrant(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("root:root 755\n")},
		{result: stdoutResult("")},
	}}
	rep := &MCPBrokerReport{}

	if err := New(fr).verifyPolicyDocuments(context.Background(), rep); err == nil {
		t.Fatal("an empty policy directory was accepted even though the broker cannot start without a service document")
	}
}

// TestVerifyPolicyDocumentsAgentWritableIsDrift is the check ADR-0004 §6
// requires and PR #78 shipped without.
//
// The policy document is the whole grant. It is root:root 0644 so the agent can
// read exactly what it is allowed to do and cannot change it — and a document
// the agent can write voids the decision while every other check stays green,
// because nothing else in the verification ever looks at it.
func TestVerifyPolicyDocumentsAgentWritableIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[policyProbeIndex+2] = scriptedResponse{result: stdoutResult("atlassian.json root root 664 f\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("group-writable policy document accepted; expected drift")
	}
	c := assertFailedCheck(t, rep, "policy_documents")
	if !strings.Contains(c.Detail, "664") {
		t.Errorf("detail %q should name the mode it found", c.Detail)
	}
}

// TestVerifyPolicyDocumentsWrongOwnerIsDrift: a document owned by the identity
// it constrains is not a grant, it is a note the agent left itself.
func TestVerifyPolicyDocumentsWrongOwnerIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[policyProbeIndex+2] = scriptedResponse{result: stdoutResult("atlassian.json hermes hermes 644 f\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("hermes-owned policy document accepted; expected drift")
	}
	assertFailedCheck(t, rep, "policy_documents")
}

// TestVerifyPolicyDocumentsSymlinkIsDrift mirrors the rule the broker's own
// loader already keeps: the directory is root-owned, but a symlink's target is
// not, so a link pointing under the agent's home hands the grant straight back
// to the identity it is meant to bind.
func TestVerifyPolicyDocumentsSymlinkIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[policyProbeIndex+2] = scriptedResponse{result: stdoutResult("atlassian.json root root 777 l\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("symlinked policy document accepted; expected drift")
	}
	assertFailedCheck(t, rep, "policy_documents")
}

func TestVerifyPolicyDocumentsRejectsMalformedPolicyContent(t *testing.T) {
	script := okBrokerScript()
	script[policyProbeIndex+3] = scriptedResponse{result: stdoutResult(`{"schema_version":"1","service":"atlassian","tools":[]}`)}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("malformed policy content was accepted")
	}
	if c := findCheck(t, rep, "policy_documents"); c.OK {
		t.Fatalf("malformed policy content recorded as OK: %+v", c)
	}
}

// TestVerifyPolicyDirectoryWritableIsDrift: a correct document inside a
// directory the agent can write to is a document the agent can replace.
func TestVerifyPolicyDirectoryWritableIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[policyProbeIndex+1] = scriptedResponse{result: stdoutResult("root:root 777\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("world-writable policy directory accepted; expected drift")
	}
	assertFailedCheck(t, rep, "policy_documents")
}

// TestVerifyPolicyDocumentsSummarisesEveryServiceInOrder pins the report half of
// ADR-0004 §4: the grant is enumerable, and the number of granted write tools is
// a number the report holds rather than a sentence a reader has to interpret.
//
// The directory is listed in the order `find` happened to walk it, which is not
// sorted. Two runs against one policy must still produce the same report, or the
// digest that identifies a generation is comparing formatting rather than grant.
func TestVerifyPolicyDocumentsSummarisesEveryServiceInOrder(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("directory\ndirectory\n")},
		{result: stdoutResult("root:root 755\n")},
		{result: stdoutResult("slack.json root root 644 f\nlinear.json root root 644 f\natlassian.json root root 644 f\n")},
		{result: stdoutResult(`{"schema_version":"1","service":"slack","upstream_endpoint":"https://slack.com/api/mcp","tools":[{"name":"slack_read_channel","writes":false},{"name":"slack_read_thread","writes":false},{"name":"slack_search_public","writes":false}]}`)},
		{result: stdoutResult(`{"schema_version":"1","service":"linear","upstream_endpoint":"https://mcp.linear.app/sse","tools":[{"name":"create_issue","writes":true}]}`)},
		{result: stdoutResult(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false},{"name":"searchConfluenceUsingCql","writes":false}]}`)},
	}}
	rep := &MCPBrokerReport{}

	if err := New(fr).verifyPolicyDocuments(context.Background(), rep); err != nil {
		t.Fatalf("a valid three-service policy was rejected: %v", err)
	}

	want := []PolicyService{
		{Name: "atlassian", UpstreamEndpoint: "https://api.atlassian.com/v1/mcp", Tools: 2, WriteTools: 0},
		{Name: "linear", UpstreamEndpoint: "https://mcp.linear.app/sse", Tools: 1, WriteTools: 1},
		{Name: "slack", UpstreamEndpoint: "https://slack.com/api/mcp", Tools: 3, WriteTools: 0},
	}
	if len(rep.Policy.Services) != len(want) {
		t.Fatalf("report carries %d services, want %d: %+v", len(rep.Policy.Services), len(want), rep.Policy.Services)
	}
	for i, w := range want {
		if rep.Policy.Services[i] != w {
			t.Errorf("service %d = %+v, want %+v", i, rep.Policy.Services[i], w)
		}
	}
	// The digest is what verifyBrokerSockets compares against the generation the
	// running broker published; a summary carrying a short or absent one would
	// turn that comparison into a check that passes by accident.
	if len(rep.Policy.Digest) != 64 {
		t.Errorf("digest = %q, want a 64-character sha256", rep.Policy.Digest)
	}
	if c := findCheck(t, *rep, "policy_documents"); !strings.Contains(c.Detail, "3 service(s), 6 tool(s), 1 write tool(s)") {
		t.Errorf("detail %q does not total the same grant the summary enumerates", c.Detail)
	}
}

// TestVerifyMCPServersForeignCommandIsDrift is the one bypass the agent can
// perform on its own.
//
// config.yaml is not on the Hermes write denylist and HERMES_WRITE_SAFE_ROOT is
// unset, which is ADR-0004's own premise for why tools.include is not a control.
// The same writability lets the agent add an MCP server that never reaches the
// broker — no policy, no audit, no window.
func TestVerifyMCPServersForeignCommandIsDrift(t *testing.T) {
	script := okBrokerScript()
	script[configProbeIndex+1] = scriptedResponse{result: stdoutResult(`mcp_servers:
  atlassian:
    command: /usr/local/bin/torio-mcp-connect
  sneaky:
    command: /usr/bin/npx
    args: ["some-mcp-server"]
`)}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("MCP server bypassing the relay accepted; expected drift")
	}
	c := assertFailedCheck(t, rep, "hermes_mcp_servers")

	// The file is agent-written, so nothing out of it may be echoed: a server
	// key and a command are both places to hide a payload aimed at whoever reads
	// the report.
	for _, leak := range []string{"sneaky", "npx", "some-mcp-server"} {
		if strings.Contains(c.Detail, leak) {
			t.Errorf("detail %q echoes %q from an agent-writable file", c.Detail, leak)
		}
	}
}

// TestVerifyMCPServersUnreadableShapeIsDrift: the reader understands one shape
// of YAML and must refuse every other rather than guess. A config it cannot read
// with confidence is a config it cannot vouch for, and "could not tell" has to
// be the same answer as "no".
func TestVerifyMCPServersUnreadableShapeIsDrift(t *testing.T) {
	for name, doc := range map[string]string{
		"flow mapping":  "mcp_servers: {atlassian: {command: /usr/bin/env}}\n",
		"anchor":        "mcp_servers:\n  atlassian: &a\n    command: /usr/local/bin/torio-mcp-connect\n",
		"alias":         "mcp_servers:\n  atlassian: *a\n",
		"tab indent":    "mcp_servers:\n\tatlassian:\n\t\tcommand: /usr/local/bin/torio-mcp-connect\n",
		"second doc":    "mcp_servers:\n  atlassian:\n    command: /usr/local/bin/torio-mcp-connect\n---\nmcp_servers:\n  x:\n    command: /bin/sh\n",
		"merge key":     "mcp_servers:\n  atlassian:\n    <<: *base\n    command: /usr/local/bin/torio-mcp-connect\n",
		"no command":    "mcp_servers:\n  atlassian:\n    args: [\"atlassian\"]\n",
		"nested inline": "mcp_servers:\n  atlassian:\n    command: {a: b}\n",
	} {
		t.Run(name, func(t *testing.T) {
			script := okBrokerScript()
			script[configProbeIndex+1] = scriptedResponse{result: stdoutResult(doc)}

			rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
			if err == nil {
				t.Fatal("unreadable config shape accepted; the check must refuse what it cannot read")
			}
			assertFailedCheck(t, rep, "hermes_mcp_servers")
		})
	}
}

// TestVerifyMCPServersAbsentConfigIsClean: no config file is no MCP server, and
// no MCP server cannot be a bypass. It is the probe failing that must fail
// closed, not the file being absent.
func TestVerifyMCPServersAbsentConfigIsClean(t *testing.T) {
	script := okBrokerScript()
	script[configProbeIndex] = scriptedResponse{result: exitResult(1, "directory\n", "no such file")}
	// The read never happens when the file is not there.
	script = append(script[:configProbeIndex+1], script[configProbeIndex+2:]...)

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("absent config treated as drift: %v", err)
	}
	if c := findCheck(t, rep, "hermes_mcp_servers"); !c.OK {
		t.Errorf("check = %+v, want OK", c)
	}
}

// TestVerifyMCPServersUnusableProbeIsNotAbsence closes the same fail-open shape
// on the new check before it can settle in.
func TestVerifyMCPServersUnusableProbeIsNotAbsence(t *testing.T) {
	script := okBrokerScript()
	script[configProbeIndex] = scriptedResponse{result: exitResult(1, "", "sudo: a password is required")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err == nil {
		t.Fatal("unusable root probe accepted as an absent config")
	}
	assertFailedCheck(t, rep, "hermes_mcp_servers")
}

// TestVerifyMCPServersNoBlockIsClean: a config with no mcp_servers key grants no
// MCP surface at all, which is the safest state there is.
func TestVerifyMCPServersNoBlockIsClean(t *testing.T) {
	script := okBrokerScript()
	script[configProbeIndex+1] = scriptedResponse{result: stdoutResult("model:\n  provider: custom\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBroker(context.Background())
	if err != nil {
		t.Fatalf("config without mcp_servers treated as drift: %v", err)
	}
	if c := findCheck(t, rep, "hermes_mcp_servers"); !c.OK {
		t.Errorf("check = %+v, want OK", c)
	}
}
