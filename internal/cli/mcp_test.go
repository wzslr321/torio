package cli

import (
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// okMCPScript is the guest replying that every ADR-0022 boundary holds, probe
// by probe, in the order lima.VerifyMCPBroker issues them.
func okMCPScript() []scriptedResp {
	out := func(s string) scriptedResp { return scriptedResp{res: execx.Result{Stdout: []byte(s)}} }
	return []scriptedResp{
		out("997\n"),                                                     // id -u torio-mcp
		out("torio-mcp-clients:x:995:hermes\n"),                          // getent group
		out("hermes torio-projects torio-mcp-clients\n"),                 // id -nG hermes (client)
		out("hermes torio-projects torio-mcp-clients\n"),                 // id -nG hermes (not owner)
		out("directory\n"),                                               // stat %F broker home
		out("torio-mcp:torio-mcp 700\n"),                                 // stat %U:%G %a broker home
		{res: execx.Result{ExitCode: 1, Stderr: []byte("no such file")}}, // stat mcp-tokens: absent
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
	script[3] = scriptedResp{res: execx.Result{Stdout: []byte("hermes torio-mcp-clients torio-mcp\n")}}
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
	script[6] = scriptedResp{res: execx.Result{Stdout: []byte("directory\n")}}
	script = append(script, scriptedResp{res: execx.Result{Stdout: []byte("xxx")}})
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
	mutators := []string{"useradd", "groupadd", "usermod", "gpasswd", "install", "mkdir", "chown", "chmod", "rm", "tee", "systemctl"}
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
	fail := func(code int) scriptedResp { return scriptedResp{res: execx.Result{ExitCode: code}} }
	return []scriptedResp{
		fail(2),                          // getent group -> absent
		out(""),                          // groupadd
		fail(1),                          // id -u torio-mcp -> absent
		out(""),                          // useradd
		out("directory\n"),               // stat %F home
		out("torio-mcp:torio-mcp 755\n"), // stat %U:%G %a -> too open
		out(""),                          // chmod 700
		out("hermes torio-projects\n"),   // id -nG hermes -> not a client yet
		out(""),                          // usermod -aG
		fail(1),                          // stat %F policy dir -> absent
		out(""),                          // install -d
		out("997\n"),                     // verify: id -u
		out("torio-mcp-clients:x:995:hermes\n"),
		out("hermes torio-projects torio-mcp-clients\n"),
		out("hermes torio-projects torio-mcp-clients\n"),
		out("directory\n"),
		out("torio-mcp:torio-mcp 700\n"),
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
