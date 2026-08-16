package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

const cliTestMCPBrokerUnit = `[Unit]
Description=Torio MCP credential broker
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=torio-mcp
Group=torio-mcp-clients
UMask=0077
RuntimeDirectory=torio-mcp
RuntimeDirectoryMode=0750
ExecStart=/usr/local/bin/torio-mcp-broker
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/home/torio-mcp

[Install]
WantedBy=multi-user.target
`

// cliTestPolicyDocs are the policy documents the scripted guest serves. Keyed by
// filename because that is what the guest hands back and what ParseDocuments
// takes, so a test's idea of the grant and the broker's cannot diverge.
var cliTestPolicyDocs = map[string]string{
	"atlassian.json": `{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`,
	"linear.json":    `{"schema_version":"1","service":"linear","upstream_endpoint":"https://mcp.linear.app/sse","tools":[{"name":"list_issues","writes":false}]}`,
	"slack.json":     `{"schema_version":"1","service":"slack","upstream_endpoint":"https://slack.com/api/mcp","tools":[{"name":"slack_read_channel","writes":false},{"name":"slack_search_public","writes":false}]}`,
}

func TestMCPLoginRunsTheFixedSessionThenActivatesTheBroker(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	runner := &fakeInteractiveRunner{}
	want := execx.InteractiveCommand{Name: "ssh", Args: []string{"fixed-login"}}
	a := &app{
		stdout: &stdout, stderr: &stderr, build: testBuild(),
		newLima: func() *lima.Adapter { return lima.New(&fakeLimaRunner{}) },
		newMCPLoginSpec: func(service string) (execx.InteractiveCommand, error) {
			if service != "atlassian" {
				t.Fatalf("service = %q", service)
			}
			return want, nil
		},
		newInteractive: func() execx.InteractiveRunner { return runner },
		activateMCP: func(_ context.Context, _ *lima.Adapter, identity backend.Identity) (lima.MCPBrokerActivationReport, error) {
			if identity.Name != backend.DefaultName {
				t.Fatalf("backend = %q, want %q", identity.Name, backend.DefaultName)
			}
			return lima.MCPBrokerActivationReport{Pending: 1}, nil
		},
	}
	if code := runWithApp(context.Background(), a, []string{"mcp", "login", "atlassian"}); code != int(ExitOK) {
		t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
	}
	if len(runner.cmds) != 1 || runner.cmds[0].Name != want.Name || strings.Join(runner.cmds[0].Args, " ") != strings.Join(want.Args, " ") {
		t.Fatalf("interactive commands = %+v, want %+v", runner.cmds, want)
	}
	if !strings.Contains(stdout.String(), "1 policy service(s) still require login") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMCPLoginRejectsJSONBeforeOpeningASession(t *testing.T) {
	code, _, _ := runVMWithFake(t, []string{"mcp", "login", "atlassian", "--json"}, &fakeLimaRunner{})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want usage", code)
	}
}

func cliTestPolicyDigest(files ...string) string {
	documents := make(map[string][]byte, len(files))
	for _, f := range files {
		documents[f] = []byte(cliTestPolicyDocs[f])
	}
	policy, err := lima.ParseDocuments(documents)
	if err != nil {
		panic(err)
	}
	return policy.Digest()
}

// okMCPScript is the guest replying that every ADR-0004 boundary holds, probe
// by probe, in the order lima.VerifyMCPBrokerFor issues them.
func okMCPScript() []scriptedResp {
	out := func(s string) scriptedResp { return scriptedResp{res: execx.Result{Stdout: []byte(s)}} }
	return []scriptedResp{
		out("997\n"), // id -u torio-mcp
		out("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n"),
		out("torio-mcp\n"),
		out("torio-mcp torio-mcp-clients\n"),
		out(cliSudoDenied),                               // broker holds no sudo
		out("1000\n"),                                    // id -u the agent
		out("torio-mcp-clients:x:995:claude\n"),          // getent group
		out("claude torio-projects torio-mcp-clients\n"), // id -nG agent (client)
		out("claude torio-projects torio-mcp-clients\n"), // id -nG agent (not owner)
		out(cliSudoDenied),                               // the agent holds no sudo
		// Each `stat %F` probe names a control path first, so a present path
		// answers with two lines and an absent one with the control line alone.
		// An empty reply means the probe never ran, which fails closed.
		out("directory\ndirectory\n"),           // stat %F broker home
		out("torio-mcp:torio-mcp 700\n"),        // stat %U:%G %a broker home
		out("directory\ndirectory\n"),           // stat %F policy dir
		out("root:root 755\n"),                  // stat %U:%G %a policy dir
		out("atlassian.json root root 644 f\n"), // find policy documents
		out(cliTestPolicyDocs["atlassian.json"]),
		out("regular file root:root 644\n"),        // stat managed settings
		out(`{"allowManagedMcpServersOnly":true}`), // cat managed settings
		out("regular file root:root 644\n"),        // stat managed MCP
		out(`{"mcpServers":{"atlassian":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["atlassian"],"env":{}}}}`),
		{res: execx.Result{ExitCode: 1, Stdout: []byte("directory\n"), Stderr: []byte("agent config absent")}}, // agent-owned config
		out("directory root:root 755\ndirectory torio-mcp:torio-mcp 700\n"),                                    // private OAuth dir
		out("directory root:root 755\nregular file torio-mcp:torio-mcp 600\n"),                                 // atlassian session
		out("directory\ndirectory\n"), // runtime is present
		out("directory root:root 755\nregular file root:root 644\n"),
		out("enabled\n"),
		out("active\n"),
		out(cliTestMCPBrokerUnit),
		out("FragmentPath=/etc/systemd/system/torio-mcp-broker.service\n" +
			"DropInPaths=\nNeedDaemonReload=no\n" +
			"User=torio-mcp\nGroup=torio-mcp-clients\nSupplementaryGroups=\nDynamicUser=no\n" +
			"Type=notify\nNotifyAccess=main\nRuntimeDirectory=torio-mcp\nRuntimeDirectoryMode=0750\n" +
			"UMask=0077\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=strict\n" +
			"ReadWritePaths=/home/torio-mcp\nAmbientCapabilities=\nRestart=on-failure\nRestartUSec=2s\n"),
		out("directory\ndirectory\n"),
		out("torio-mcp:torio-mcp-clients 750\n"),
		out("atlassian.sock torio-mcp torio-mcp-clients 660\n"),
		out("u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n"),
		out(cliTestPolicyDigest("atlassian.json") + "\n"),
	}
}

// Indices into okMCPScript. The probes run in a fixed order, so a reply spliced
// at a guessed offset would be consumed by a different check and prove nothing.
const (
	mcpPolicyListingIndex  = 14
	mcpPolicyDocumentIndex = 15
	mcpAgentConfigIndex    = 19
	mcpOAuthSessionIndex   = 22
	mcpSocketListingIndex  = 31
)

// multiServiceMCPScript is a guest whose policy directory holds three services
// rather than one, listed in the order `find` walked the directory — which is
// not sorted, and is not the order a report must present them in.
func multiServiceMCPScript() []scriptedResp {
	out := func(s string) scriptedResp { return scriptedResp{res: execx.Result{Stdout: []byte(s)}} }
	script := okMCPScript()

	script[mcpPolicyListingIndex] = out("slack.json root root 644 f\nlinear.json root root 644 f\natlassian.json root root 644 f\n")
	// One `cat` per document, in the listing's order.
	documents := []scriptedResp{
		out(cliTestPolicyDocs["slack.json"]),
		out(cliTestPolicyDocs["linear.json"]),
		out(cliTestPolicyDocs["atlassian.json"]),
	}
	script = append(script[:mcpPolicyDocumentIndex], append(documents, script[mcpPolicyDocumentIndex+1:]...)...)
	shift := len(documents) - 1

	script[mcpAgentConfigIndex+shift] = out(`{"mcpServers":{` +
		`"atlassian":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["atlassian"],"env":{}},` +
		`"linear":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["linear"],"env":{}},` +
		`"slack":{"type":"stdio","command":"/usr/local/bin/torio-mcp-connect","args":["slack"],"env":{}}}}`)

	// One private OAuth session probe per policy service, in the grant's order.
	session := out("directory root:root 755\nregular file torio-mcp:torio-mcp 600\n")
	sessionAt := mcpOAuthSessionIndex + shift
	script = append(script[:sessionAt], append([]scriptedResp{session, session}, script[sessionAt:]...)...)
	shift += 2

	socket := mcpSocketListingIndex + shift
	script[socket] = out("atlassian.sock torio-mcp torio-mcp-clients 660\n" +
		"linear.sock torio-mcp torio-mcp-clients 660\n" +
		"slack.sock torio-mcp torio-mcp-clients 660\n")
	script[socket+1] = out(
		"u_str LISTEN 0 4096 /run/torio-mcp/atlassian.sock 9 * 0\n" +
			"u_str LISTEN 0 4096 /run/torio-mcp/linear.sock 10 * 0\n" +
			"u_str LISTEN 0 4096 /run/torio-mcp/slack.sock 11 * 0\n")
	script[socket+2] = out(cliTestPolicyDigest("atlassian.json", "linear.json", "slack.json") + "\n")
	return script
}

func TestMCPNoSubcommandIsUsage(t *testing.T) {
	code, _, _ := runVMWithFake(t, []string{"mcp"}, &fakeLimaRunner{script: okMCPScript()})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, int(ExitUsage))
	}
}

func TestMCPUnknownSubcommandIsUsage(t *testing.T) {
	code, _, _ := runVMWithFake(t, []string{"mcp", "grant"}, &fakeLimaRunner{script: okMCPScript()})
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, int(ExitUsage))
	}
}

func TestMCPStatusHappyPathHumanAndJSON(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "status"}, &fakeLimaRunner{script: okMCPScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"broker_user", "claude_not_broker_owner", "claude_mcp_servers"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human output does not mention check %q: %q", want, stdout)
		}
	}

	code, stdout, _ = runVMWithFake(t, []string{"mcp", "status", "--json"}, &fakeLimaRunner{script: okMCPScript()})
	if code != int(ExitOK) {
		t.Fatalf("json exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "mcp.status" {
		t.Fatalf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	checks, _ := data["checks"].([]any)
	if len(checks) == 0 {
		t.Fatalf("data carries no checks: %v", data)
	}
	if data["broker_user"] != "torio-mcp" {
		t.Errorf("data.broker_user = %v, want torio-mcp", data["broker_user"])
	}
}

// TestMCPStatusReportsTheGrantItVerified is the reporting half of ADR-0004 §4.
// The command surface is provider-agnostic — a service is a policy document, not
// a code path — so the answer to "what is granted" has to be enumerated from the
// documents rather than known in advance, and the count of granted write tools
// has to be a number rather than a sentence.
func TestMCPStatusReportsTheGrantItVerified(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "status", "--json"}, &fakeLimaRunner{script: multiServiceMCPScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", code, stderr, stdout)
	}
	env := decodeOneEnvelope(t, stdout)
	data, _ := env["data"].(map[string]any)
	policy, _ := data["policy"].(map[string]any)
	if digest, _ := policy["digest"].(string); len(digest) != 64 {
		t.Errorf("policy.digest = %v, want the 64-character generation the broker publishes", policy["digest"])
	}
	services, _ := policy["services"].([]any)
	if len(services) != 3 {
		t.Fatalf("policy.services carries %d entries, want 3: %v", len(services), policy)
	}

	// Ordered by name, not by the order the guest's `find` happened to return.
	// Two reports of one policy that differ only in order are two reports a
	// caller has to diff by hand.
	want := []map[string]any{
		{"name": "atlassian", "upstream_endpoint": "https://api.atlassian.com/v1/mcp", "tools": 1.0, "write_tools": 0.0},
		{"name": "linear", "upstream_endpoint": "https://mcp.linear.app/sse", "tools": 1.0, "write_tools": 0.0},
		{"name": "slack", "upstream_endpoint": "https://slack.com/api/mcp", "tools": 2.0, "write_tools": 0.0},
	}
	for i, w := range want {
		got, _ := services[i].(map[string]any)
		for field, value := range w {
			if got[field] != value {
				t.Errorf("policy.services[%d].%s = %v, want %v", i, field, got[field], value)
			}
		}
	}
}

// TestMCPStatusHumanOutputNamesEachServiceAndItsUpstream: an operator asking
// what is granted is also asking where the data goes, and the endpoint is the
// only place that is written down.
func TestMCPStatusHumanOutputNamesEachServiceAndItsUpstream(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "status"}, &fakeLimaRunner{script: multiServiceMCPScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"atlassian", "https://api.atlassian.com/v1/mcp",
		"linear", "https://mcp.linear.app/sse",
		"slack", "https://slack.com/api/mcp",
		"Granted policy (generation ",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human output does not carry %q: %q", want, stdout)
		}
	}
}

// TestMCPStatusReportsNoWriteToolOnTheReleasedSurface pins what the released
// command may show. Nothing in the shipped policy grants a write, so a report
// claiming one would either be counting wrong or describing a guest provisioned
// outside the documented surface. The count exists because ADR-0004 §4 requires
// it to; it is not a capability this binary can use.
func TestMCPStatusReportsNoWriteToolOnTheReleasedSurface(t *testing.T) {
	code, stdout, _ := runVMWithFake(t, []string{"mcp", "status", "--json"}, &fakeLimaRunner{script: multiServiceMCPScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	data, _ := env["data"].(map[string]any)
	policy, _ := data["policy"].(map[string]any)
	services, _ := policy["services"].([]any)
	for i, entry := range services {
		s, _ := entry.(map[string]any)
		if s["write_tools"] != 0.0 {
			t.Errorf("policy.services[%d] grants %v write tools; the released surface grants none", i, s["write_tools"])
		}
	}
}

func TestMCPStatusAcceptsAProvisionedBoundaryWithoutTheUnreleasedDaemon(t *testing.T) {
	script := okMCPScript()
	// No private OAuth session yet, so no runtime is expected either: this is
	// the state between `mcp install` and the first `mcp login`.
	script[22] = scriptedResp{res: execx.Result{Stdout: []byte("directory root:root 755\n")}}
	script = append(script[:23], scriptedResp{res: execx.Result{
		ExitCode: 1,
		Stdout:   []byte("directory\n"),
	}})
	code, _, stderr := runVMWithFake(t, []string{"mcp", "status"}, &fakeLimaRunner{script: script})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
}

// TestMCPStatusNotInstalledIsPrecondition separates "you have not installed this
// yet" from "a guarantee was broken". An operator on a guest that predates the
// broker must get the precondition class, not a security-drift alarm.
func TestMCPStatusNotInstalledIsPrecondition(t *testing.T) {
	script := okMCPScript()
	script[0] = scriptedResp{res: execx.Result{ExitCode: 1, Stderr: []byte("no such user")}}
	code, stdout, _ := runVMWithFake(t, []string{"mcp", "status", "--json"}, &fakeLimaRunner{script: script})
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d (precondition)", code, int(ExitPrecondition))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Fatalf("expected an error envelope, got %v", env)
	}
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Errorf("error code = %v, want NOT_FOUND", errObj["code"])
	}
}

// TestMCPStatusBrokenCustodyIsVerification is the alarm that must be loud: the
// broker exists, but the agent can read its credential store.
func TestMCPStatusBrokenCustodyIsVerification(t *testing.T) {
	script := okMCPScript()
	script[8] = scriptedResp{res: execx.Result{Stdout: []byte("claude torio-mcp-clients torio-mcp\n")}}
	code, stdout, _ := runVMWithFake(t, []string{"mcp", "status", "--json"}, &fakeLimaRunner{script: script})
	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d (verification)", code, int(ExitVerification))
	}
	env := decodeOneEnvelope(t, stdout)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "VERIFICATION_FAILED" {
		t.Errorf("error code = %v, want VERIFICATION_FAILED", errObj["code"])
	}
	// The checks recorded before the failure must survive into details, so the
	// operator sees which boundary broke without re-running anything.
	details, _ := errObj["details"].(map[string]any)
	checks, _ := details["checks"].([]any)
	if len(checks) == 0 {
		t.Fatalf("error details carry no checks: %v", details)
	}
}

// TestMCPStatusIsReadOnly pins that status proves and never repairs: every guest
// command it issues must be a probe, so a drifted machine is never silently
// "fixed" by the command an operator ran to inspect it.
func TestMCPStatusIsReadOnly(t *testing.T) {
	fake := &fakeLimaRunner{script: okMCPScript()}
	if code, _, _ := runVMWithFake(t, []string{"mcp", "status"}, fake); code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	mutators := []string{"useradd", "groupadd", "usermod", "gpasswd", "install", "mkdir", "chown", "chmod", "rm", "tee"}
	for _, argv := range fake.calls {
		joined := strings.Join(argv, " ")
		for _, m := range mutators {
			if strings.Contains(joined, " "+m+" ") || strings.HasSuffix(joined, " "+m) {
				t.Errorf("status issued a mutating guest command %q (contains %q)", joined, m)
			}
		}
	}
}

// freshMCPInstallScript is a guest with nothing provisioned: probes say absent,
// mutations succeed, and the closing identity verification sees the result.
func freshMCPInstallScript() []scriptedResp {
	out := func(s string) scriptedResp { return scriptedResp{res: execx.Result{Stdout: []byte(s)}} }
	fail := func(code int, stdout string) scriptedResp {
		return scriptedResp{res: execx.Result{ExitCode: code, Stdout: []byte(stdout)}}
	}
	return append([]scriptedResp{
		fail(2, ""),                          // getent group -> absent
		out(""),                              // groupadd
		fail(1, ""),                          // id -u torio-mcp -> absent
		out(""),                              // useradd
		out("torio-mcp\n"),                   // id -nG torio-mcp -> not a client yet
		out(""),                              // usermod -aG
		out("torio-mcp torio-mcp-clients\n"), // verify broker membership
		out("directory\n"),                   // stat %F home
		out("torio-mcp:torio-mcp 755\n"),     // stat %U:%G %a -> too open
		out(""),                              // chmod 700
		out("claude torio-projects\n"),       // id -nG agent -> not a client yet
		out(""),                              // usermod -aG
		out("directory\n"),                   // policy dir pre-staged by root
		out("997\n"),                         // verify broker identity
		out("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n"),
		out("torio-mcp\n"),
		out("torio-mcp torio-mcp-clients\n"),
		out(cliSudoDenied), // broker holds no sudo
		out("1000\n"),      // id -u the agent
		out("torio-mcp-clients:x:995:claude\n"),
		out("claude torio-projects torio-mcp-clients\n"),
		out("claude torio-projects torio-mcp-clients\n"),
		out(cliSudoDenied), // the agent holds no sudo
		out("directory\ndirectory\n"),
		out("torio-mcp:torio-mcp 700\n"),
		out("directory\ndirectory\n"),           // verify policy directory path
		out("root:root 755\n"),                  // verify policy directory ownership
		out("atlassian.json root root 644 f\n"), // verify policy document metadata
		out(cliTestPolicyDocs["atlassian.json"]),
	}, mcpPayloadInstallResponses()...)
}

// mcpPayloadInstallResponses is the tail of a fresh install: the three release
// files land and are verified from the digest that was sent, the backend's MCP
// declaration is reconciled, and the runtime stays dormant because no policy
// service has a private OAuth session yet.
func mcpPayloadInstallResponses() []scriptedResp {
	out := func(s string) scriptedResp { return scriptedResp{res: execx.Result{Stdout: []byte(s)}} }
	fail := func(code int, stdout string) scriptedResp {
		return scriptedResp{res: execx.Result{ExitCode: code, Stdout: []byte(stdout)}}
	}
	// One file lands in eight calls: prove absent, write, chown, chmod, rename,
	// fsync, then re-stat and re-digest what actually landed.
	install := func(mode, digest string) []scriptedResp {
		return []scriptedResp{
			fail(1, ""), // stat: absent
			out(""),     // dd
			out(""),     // chown
			out(""),     // chmod
			out(""),     // mv -T
			out(""),     // sync -f
			out("root:root " + mode + " regular file\n"),
			out(digest + "  installed\n"),
		}
	}
	var script []scriptedResp
	script = append(script, install("755", mcpPayloadDigest(testProfile.MCPBrokerArtifact()))...)
	script = append(script, install("755", mcpPayloadDigest(testProfile.MCPRelayArtifact()))...)
	script = append(script, install("644", cliTestMCPBrokerUnitDigest())...)
	script = append(script,
		out(""),                          // systemctl daemon-reload, after the unit landed
		out("changed\n"),                 // reconcile the backend's managed MCP declaration
		out("unchanged\n"),               // scrub any agent-owned native declaration
		out("directory root:root 755\n"), // no private OAuth directory yet
		fail(1, "disabled\n"),            // unit is not enabled
		fail(3, "inactive\n"),            // unit is not active
	)
	return script
}

func cliTestMCPBrokerUnitDigest() string {
	sum := sha256.Sum256([]byte(cliTestMCPBrokerUnit))
	return hex.EncodeToString(sum[:])
}

func mcpInstallEmptyPolicyScript() []scriptedResp {
	script := append([]scriptedResp{}, freshMCPInstallScript()[:28]...)
	script[27] = scriptedResp{res: execx.Result{}} // policy directory has no documents
	return script
}

func TestMCPInstallFreshReportsChangeAndRestart(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "install", "--json"}, &fakeLimaRunner{script: freshMCPInstallScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q stdout=%q", code, stderr, stdout)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "mcp.install" {
		t.Fatalf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["changed"] != true {
		t.Errorf("data.changed = %v, want true on a fresh guest", data["changed"])
	}
	if data["restart_required"] != true {
		t.Errorf("data.restart_required = %v, want true: the agent only just joined the client group", data["restart_required"])
	}
}

func TestMCPInstallJSONErrorReportsPartialChangeAndRestart(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "install", "--json"}, &fakeLimaRunner{script: mcpInstallEmptyPolicyScript()})
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d; stderr=%q stdout=%q", code, int(ExitPrecondition), stderr, stdout)
	}
	env := decodeOneEnvelope(t, stdout)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("error.code = %v, want NOT_FOUND", errObj["code"])
	}
	details, _ := errObj["details"].(map[string]any)
	if details["changed"] != true {
		t.Errorf("error.details.changed = %v, want true", details["changed"])
	}
	if details["restart_required"] != true {
		t.Errorf("error.details.restart_required = %v, want true", details["restart_required"])
	}
}

func TestMCPInstallHumanErrorReportsPartialChangeAndRestart(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "install"}, &fakeLimaRunner{script: mcpInstallEmptyPolicyScript()})
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d; stderr=%q", code, int(ExitPrecondition), stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty human error output", stdout)
	}
	for _, want := range []string{"guest was partially changed", "open a new one", "re-run `torio mcp install`"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %q", want, stderr)
		}
	}
}

func TestMCPInstallHelpDescribesReleasedDaemonDeployment(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "install", "--help"}, &fakeLimaRunner{})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"broker, relay, and systemd unit", "torio mcp login <service>", "selected backend"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not describe released behavior %q: %q", want, stdout)
		}
	}
}

func TestMCPParentHelpNamesTheReleasedBrokerFlow(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "--help"}, &fakeLimaRunner{})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, want := range []string{"install", "login", "status"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("parent help does not list %q: %q", want, stdout)
		}
	}
}

func TestMCPInstallHumanOutputNamesTheRestartStep(t *testing.T) {
	code, stdout, _ := runVMWithFake(t, []string{"mcp", "install"}, &fakeLimaRunner{script: freshMCPInstallScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	// A broker the agent cannot reach, with no stated reason, is the failure mode
	// this line exists to prevent.
	if !strings.Contains(stdout, "open a new one") {
		t.Errorf("human output does not tell the operator how to pick up the new membership: %q", stdout)
	}
	// The restart is what is left to do, so it stays last. A grant printed after
	// it pushes the one actionable line off the bottom of the output.
	if grant := strings.Index(stdout, "Granted policy"); grant < 0 || grant > strings.Index(stdout, "open a new one") {
		t.Errorf("the grant must be printed before the session step: %q", stdout)
	}
}

// TestMCPInstallReportsTheGrantItProvisioned: install proves the same policy
// boundary status does, so an operator who has just provisioned a guest does not
// need a second command to learn what they granted.
func TestMCPInstallReportsTheGrantItProvisioned(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "install", "--json"}, &fakeLimaRunner{script: freshMCPInstallScript()})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	data, _ := env["data"].(map[string]any)
	policy, _ := data["policy"].(map[string]any)
	if digest, _ := policy["digest"].(string); digest != cliTestPolicyDigest("atlassian.json") {
		t.Errorf("policy.digest = %v, want the generation of the documents install verified", policy["digest"])
	}
	services, _ := policy["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("policy.services carries %d entries, want 1: %v", len(services), policy)
	}
	svc, _ := services[0].(map[string]any)
	for field, want := range map[string]any{
		"name":              "atlassian",
		"upstream_endpoint": "https://api.atlassian.com/v1/mcp",
		"tools":             1.0,
		"write_tools":       0.0,
	} {
		if svc[field] != want {
			t.Errorf("policy.services[0].%s = %v, want %v", field, svc[field], want)
		}
	}
}

// cliSudoDenied is what a guest prints for an identity that holds no sudo, asked
// by a caller that already holds root. The fixtures it replaces carried a bare
// exit 1, which sudo does not produce for this question.
const cliSudoDenied = "User torio-mcp is not allowed to run sudo on lima-guest.\n"
