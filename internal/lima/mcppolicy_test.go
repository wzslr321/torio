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
	policyProbeIndex = 12
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

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
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
	script[policyProbeIndex+2] = scriptedResponse{result: stdoutResult("atlassian.json " + testUser + " " + testUser + " 644 f\n")}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("agent-owned policy document accepted; expected drift")
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

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
	if err == nil {
		t.Fatal("symlinked policy document accepted; expected drift")
	}
	assertFailedCheck(t, rep, "policy_documents")
}

func TestVerifyPolicyDocumentsRejectsMalformedPolicyContent(t *testing.T) {
	script := okBrokerScript()
	script[policyProbeIndex+3] = scriptedResponse{result: stdoutResult(`{"schema_version":"1","service":"atlassian","tools":[]}`)}

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
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

	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), testAgentIdentity())
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
