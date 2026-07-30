package cli

import (
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	brokerpolicy "github.com/wzslr321/torio/internal/mcpbroker"
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

func cliTestPolicyDigest() string {
	policy, err := brokerpolicy.ParseDocuments(map[string][]byte{
		"atlassian.json": []byte(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`),
	})
	if err != nil {
		panic(err)
	}
	return policy.Digest()
}

// okMCPScript is the guest replying that every ADR-0022 boundary holds, probe
// by probe, in the order lima.VerifyMCPBroker issues them.
func okMCPScript() []scriptedResp {
	out := func(s string) scriptedResp { return scriptedResp{res: execx.Result{Stdout: []byte(s)}} }
	return []scriptedResp{
		out("997\n"), // id -u torio-mcp
		out("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n"),
		out("torio-mcp\n"),
		out("torio-mcp torio-mcp-clients\n"),
		{res: execx.Result{ExitCode: 1}},                 // no broker sudo grants
		out("1000\n"),                                    // id -u hermes
		out("torio-mcp-clients:x:995:hermes\n"),          // getent group
		out("hermes torio-projects torio-mcp-clients\n"), // id -nG hermes (client)
		out("hermes torio-projects torio-mcp-clients\n"), // id -nG hermes (not owner)
		{res: execx.Result{ExitCode: 1}},                 // no hermes sudo grants
		// Each `stat %F` probe names a control path first, so a present path
		// answers with two lines and an absent one with the control line alone.
		// An empty reply means the probe never ran, which fails closed.
		out("directory\ndirectory\n"),    // stat %F broker home
		out("torio-mcp:torio-mcp 700\n"), // stat %U:%G %a broker home
		{res: execx.Result{ExitCode: 1, Stdout: []byte("directory\n"), Stderr: []byte("no such file")}}, // stat mcp-tokens: absent
		out("directory\ndirectory\n"),           // stat %F policy dir
		out("root:root 755\n"),                  // stat %U:%G %a policy dir
		out("atlassian.json root root 644 f\n"), // find policy documents
		out(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`),
		out("directory\nregular file\n"), // stat %F hermes config.yaml
		out("mcp_servers:\n  atlassian:\n    command: /usr/local/bin/torio-mcp-connect\n"), // cat config.yaml
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
		out(cliTestPolicyDigest() + "\n"),
	}
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
	for _, want := range []string{"broker_user", "hermes_not_broker_owner", "hermes_mcp_tokens"} {
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

func TestMCPStatusAcceptsAProvisionedBoundaryWithoutTheUnreleasedDaemon(t *testing.T) {
	script := okMCPScript()
	script = append(script[:19], scriptedResp{res: execx.Result{
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
// broker exists, but hermes can read its credential store.
func TestMCPStatusBrokenCustodyIsVerification(t *testing.T) {
	script := okMCPScript()
	script[8] = scriptedResp{res: execx.Result{Stdout: []byte("hermes torio-mcp-clients torio-mcp\n")}}
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

// TestMCPStatusLeftoverTokensReportsCountNotNames guards the privacy rule at the
// surface an operator actually reads: the count is the finding, guest filenames
// are never printed.
func TestMCPStatusLeftoverTokensReportsCountNotNames(t *testing.T) {
	script := okMCPScript()
	script[12] = scriptedResp{res: execx.Result{Stdout: []byte("directory\ndirectory\n")}}
	// Spliced, not appended: the socket probe follows the token probes, so an
	// extra reply at the end would be consumed by the wrong check.
	script = append(script[:13], append([]scriptedResp{{res: execx.Result{Stdout: []byte("xxx")}}}, script[13:]...)...)
	code, stdout, _ := runVMWithFake(t, []string{"mcp", "status", "--json"}, &fakeLimaRunner{script: script})
	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d (verification)", code, int(ExitVerification))
	}
	if !strings.Contains(stdout, "3 credential files") {
		t.Errorf("output does not report the credential count: %q", stdout)
	}
	for _, leak := range []string{".json", "atlassian"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("output leaks %q; the surface reports a count, never names", leak)
		}
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
	return []scriptedResp{
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
		out("hermes torio-projects\n"),       // id -nG hermes -> not a client yet
		out(""),                              // usermod -aG
		out("directory\n"),                   // policy dir pre-staged by root
		out("997\n"),                         // verify broker identity
		out("torio-mcp:x:997:997::/home/torio-mcp:/usr/sbin/nologin\n"),
		out("torio-mcp\n"),
		out("torio-mcp torio-mcp-clients\n"),
		fail(1, ""),   // broker has no sudo grants
		out("1000\n"), // id -u hermes
		out("torio-mcp-clients:x:995:hermes\n"),
		out("hermes torio-projects torio-mcp-clients\n"),
		out("hermes torio-projects torio-mcp-clients\n"),
		fail(1, ""), // hermes has no sudo grants
		out("directory\ndirectory\n"),
		out("torio-mcp:torio-mcp 700\n"),
		out("directory\ndirectory\n"),           // verify policy directory path
		out("root:root 755\n"),                  // verify policy directory ownership
		out("atlassian.json root root 644 f\n"), // verify policy document metadata
		out(`{"schema_version":"1","service":"atlassian","upstream_endpoint":"https://api.atlassian.com/v1/mcp","tools":[{"name":"getJiraIssue","writes":false}]}`),
	}
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
		t.Errorf("data.restart_required = %v, want true: hermes only just joined the client group", data["restart_required"])
	}
}

func TestMCPInstallHelpDoesNotPromiseUnreleasedDaemonDeployment(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "install", "--help"}, &fakeLimaRunner{})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, unavailable := range []string{"Linux arm64", "systemd-analyze verify", "listening sockets"} {
		if strings.Contains(stdout, unavailable) {
			t.Errorf("help promises unreleased behavior %q: %q", unavailable, stdout)
		}
	}
	if !strings.Contains(stdout, "not installed or activated") {
		t.Errorf("help does not state the daemon boundary: %q", stdout)
	}
}

func TestMCPParentHelpDoesNotClaimTheUnreleasedBrokerIsServing(t *testing.T) {
	code, stdout, stderr := runVMWithFake(t, []string{"mcp", "--help"}, &fakeLimaRunner{})
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, falseClaim := range []string{"MCP servers are reached through a broker", "no upstream credential exists"} {
		if strings.Contains(stdout, falseClaim) {
			t.Errorf("parent help advertises unreleased behavior %q: %q", falseClaim, stdout)
		}
	}
}

func TestMCPInstallDoesNotDeployTheUnreleasedDaemon(t *testing.T) {
	fake := &fakeLimaRunner{script: freshMCPInstallScript()}
	code, _, stderr := runVMWithFake(t, []string{"mcp", "install"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	for _, argv := range fake.calls {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "systemctl") || strings.Contains(joined, lima.TorioMCPBrokerPath) || strings.Contains(joined, lima.TorioMCPRelayPath) {
			t.Fatalf("unreleased daemon deployment reached the guest: %s", joined)
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
	if !strings.Contains(stdout, "serve restart") {
		t.Errorf("human output does not tell the operator to restart the backend: %q", stdout)
	}
}
