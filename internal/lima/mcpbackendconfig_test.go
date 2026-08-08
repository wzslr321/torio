package lima

import (
	"context"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
)

func TestHermesMCPConfigMustNameEachPolicyServiceAsItsRelayArgument(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}
	good := "mcp_servers:\n  atlassian:\n    command: /usr/local/bin/torio-mcp-connect\n    args:\n    - atlassian\n    enabled: true\n"
	if err := hermesMCPConfigExact(good, grant); err != nil {
		t.Fatalf("exact config rejected: %v", err)
	}
	for _, bad := range []string{
		"mcp_servers:\n  atlassian:\n    command: /usr/local/bin/torio-mcp-connect\n    args:\n    - linear\n",
		"mcp_servers:\n  extra:\n    command: /usr/local/bin/torio-mcp-connect\n    args:\n    - extra\n",
	} {
		if err := hermesMCPConfigExact(bad, grant); err == nil {
			t.Fatalf("config outside the policy was accepted:\n%s", bad)
		}
	}
}

func TestVerifyMCPBrokerForClaudeProvesManagedRelayAndReportsPendingOAuth(t *testing.T) {
	script := identityVerificationScript()
	script[5] = scriptedResponse{result: stdoutResult("1001\n")}
	script[6] = scriptedResponse{result: stdoutResult("torio-mcp-clients:x:995:claude\n")}
	script[7] = scriptedResponse{result: stdoutResult("claude torio-projects torio-mcp-clients\n")}
	script[8] = scriptedResponse{result: stdoutResult("claude torio-projects torio-mcp-clients\n")}
	script = append(script,
		scriptedResponse{result: stdoutResult("directory\ndirectory\n")},
		scriptedResponse{result: stdoutResult("root:root 755\n")},
		scriptedResponse{result: stdoutResult("atlassian.json root root 644 f\n")},
		scriptedResponse{result: stdoutResult(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`)},
		scriptedResponse{result: stdoutResult("regular file root:root 644\n")},
		scriptedResponse{result: stdoutResult(`{"allowManagedMcpServersOnly":true}`)},
		scriptedResponse{result: stdoutResult("regular file root:root 644\n")},
		scriptedResponse{result: stdoutResult(`{"mcpServers":{"atlassian":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["atlassian"],"env":{}}}}`)},
		scriptedResponse{result: exitResult(1, "directory\n", "agent config absent")},
		scriptedResponse{result: exitResult(1, "directory root:root 755\n", "oauth absent")},
		scriptedResponse{result: exitResult(1, "directory\n", "runtime absent")},
	)
	identity := backend.Identity{Name: "claude-code", GuestUser: "claude", Home: "/home/claude"}
	rep, err := New(&fakeRunner{script: script}).VerifyMCPBrokerFor(context.Background(), identity)
	if err != nil {
		t.Fatalf("VerifyMCPBrokerFor: %v", err)
	}
	if rep.AgentUser != "claude" {
		t.Fatalf("AgentUser = %q", rep.AgentUser)
	}
	if len(rep.Checks) == 0 || rep.Checks[len(rep.Checks)-2].Name != "oauth_sessions" {
		t.Fatalf("checks do not report pending OAuth before runtime: %+v", rep.Checks)
	}
}

func TestClaudeManagedMCPConfigIsExactAndCredentialFree(t *testing.T) {
	grant := PolicyGrant{Services: []PolicyService{{Name: "atlassian"}}}
	good := `{"mcpServers":{"atlassian":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["atlassian"],"env":{}}}}`
	if err := claudeManagedMCPExact(good, grant); err != nil {
		t.Fatalf("exact managed config rejected: %v", err)
	}
	bad := `{"mcpServers":{"atlassian":{"type":"http","url":"https://example.test","headers":{"Authorization":"[REDACTED]"}}}}`
	if err := claudeManagedMCPExact(bad, grant); err == nil {
		fatal := "direct remote Claude MCP config was accepted"
		t.Fatal(fatal)
	}
}

func TestClaudeManagedSettingsMustExcludeUnmanagedMCPServers(t *testing.T) {
	if !claudeManagedOnly(`{"allowManagedMcpServersOnly":true,"permissions":{}}`) {
		t.Fatal("managed-only setting was not recognized")
	}
	for _, doc := range []string{`{}`, `{"allowManagedMcpServersOnly":false}`, `{"allowManagedMcpServersOnly":"true"}`} {
		if claudeManagedOnly(doc) {
			t.Fatalf("non-enforcing managed setting was accepted: %s", doc)
		}
	}
}

func TestClaudeAgentOwnedConfigMustContainNoNativeMCPEntry(t *testing.T) {
	if !claudeAgentMCPEmpty(`{"projects":{"/workspace":{"lastSession":"x"}}}`) {
		t.Fatal("credential-free agent config was rejected")
	}
	for _, doc := range []string{
		`{"mcpServers":{"direct":{"type":"http"}}}`,
		`{"projects":{"/workspace":{"mcpServers":{"direct":{"command":"npx"}}}}}`,
		`not json`,
	} {
		if claudeAgentMCPEmpty(doc) {
			t.Fatalf("native or unreadable MCP config was accepted: %s", doc)
		}
	}
}
