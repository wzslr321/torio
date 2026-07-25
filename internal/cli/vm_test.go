package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"hermes-box.local/hb/internal/execx"
	"hermes-box.local/hb/internal/lima"
)

// scriptedResp is one canned (Result, error) pair for the fake runner.
type scriptedResp struct {
	res execx.Result
	err error
}

// fakeLimaRunner is a local execx.Runner double for the cli package: it records
// the exact argv it received and replays a scripted response per call. It never
// spawns a process, so vm command tests never touch a real Lima VM.
type fakeLimaRunner struct {
	calls  [][]string
	script []scriptedResp
}

func (f *fakeLimaRunner) Run(_ context.Context, cmd execx.Command) (execx.Result, error) {
	i := len(f.calls)
	f.calls = append(f.calls, append([]string{cmd.Name}, cmd.Args...))
	if i < len(f.script) {
		return f.script[i].res, f.script[i].err
	}
	return execx.Result{}, nil
}

// runVMWithFake runs `hb <args>` with isolated XDG dirs and the given fake
// runner wired behind the Lima adapter seam, returning exit code + streams.
func runVMWithFake(t *testing.T, args []string, fake execx.Runner) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:  &stdout,
		stderr:  &stderr,
		build:   testBuild(),
		newLima: func() *lima.Adapter { return lima.New(fake) },
	}
	code := runWithApp(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

// decodeOneEnvelope asserts stdout is exactly one JSON envelope and returns it.
func decodeOneEnvelope(t *testing.T, stdout string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a JSON document: %v; got %q", err, stdout)
	}
	if err := dec.Decode(new(map[string]any)); err != io.EOF {
		t.Fatalf("expected exactly one JSON document, got %v; stdout=%q", err, stdout)
	}
	return env
}

func listJSON(name, status string) execx.Result {
	return execx.Result{ExitCode: 0, Stdout: []byte(`{"name":"` + name + `","status":"` + status + `"}` + "\n")}
}

// --- vm status ---

func TestVMStatusHuman(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("hermes-box", "Stopped")}}}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "status"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if strings.TrimSpace(stdout) != "hermes-box: stopped" {
		t.Errorf("stdout = %q, want %q", stdout, "hermes-box: stopped\n")
	}
}

func TestVMStatusJSONSingleEnvelope(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("hermes-box", "Running")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "status", "--json"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "vm.status" {
		t.Errorf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["name"] != "hermes-box" || data["state"] != "running" {
		t.Errorf("data = %v, want name=hermes-box state=running", data)
	}
}

func TestVMStatusLimaErrorIsExternal(t *testing.T) {
	// A runner spawn failure (binary unavailable) → KindBinaryUnavailable → exit 8.
	fake := &fakeLimaRunner{script: []scriptedResp{{err: errors.New("exec: \"limactl\": not found")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "status"}, fake)
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d (external)", code, int(ExitExternal))
	}
	if stdout != "" {
		t.Errorf("human error mode: stdout should be empty, got %q", stdout)
	}
}

// --- vm start ---

func TestVMStartSuccessHumanAndJSON(t *testing.T) {
	// Human: Stopped → start → Running.
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: listJSON("hermes-box", "Stopped")},
		{res: execx.Result{ExitCode: 0}},
		{res: listJSON("hermes-box", "Running")},
	}}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "start"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if strings.TrimSpace(stdout) != "hermes-box: running" {
		t.Errorf("stdout = %q, want %q", stdout, "hermes-box: running\n")
	}

	// JSON: same three-step script.
	fake = &fakeLimaRunner{script: []scriptedResp{
		{res: listJSON("hermes-box", "Stopped")},
		{res: execx.Result{ExitCode: 0}},
		{res: listJSON("hermes-box", "Running")},
	}}
	code, stdout, _ = runVMWithFake(t, []string{"vm", "start", "--json"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("json exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "vm.start" {
		t.Errorf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["name"] != "hermes-box" || data["state"] != "running" {
		t.Errorf("data = %v, want name=hermes-box state=running", data)
	}
}

func TestVMStartNotFoundIsPrecondition(t *testing.T) {
	// list shows a different instance → target not found → KindNotFound → exit 3.
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("other", "Running")}}}
	code, _, _ := runVMWithFake(t, []string{"vm", "start"}, fake)
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d (precondition)", code, int(ExitPrecondition))
	}
}

func TestVMStartBinaryUnavailableIsExternal(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{err: errors.New("exec: \"limactl\": not found")}}}
	code, _, _ := runVMWithFake(t, []string{"vm", "start"}, fake)
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d (external)", code, int(ExitExternal))
	}
}

// --- vm ssh ---

func TestVMSSHPassesExactTokenArray(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: execx.Result{ExitCode: 0}}}}
	code, _, stderr := runVMWithFake(t, []string{"vm", "ssh", "--", "uname", "-a"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	want := []string{"limactl", "shell", "--tty=false", "hermes-box", "--", "uname", "-a"}
	if len(fake.calls) != 1 || !reflect.DeepEqual(fake.calls[0], want) {
		t.Fatalf("argv = %v, want %v", fake.calls, want)
	}
}

func TestVMSSHNoTokensIsUsage(t *testing.T) {
	fake := &fakeLimaRunner{}
	code, _, _ := runVMWithFake(t, []string{"vm", "ssh"}, fake)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, int(ExitUsage))
	}
	if len(fake.calls) != 0 {
		t.Errorf("no tokens must not reach the adapter, got calls %v", fake.calls)
	}
}

func TestVMSSHHumanRoutesStdoutAndStderr(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte("hello-out"), Stderr: []byte("hello-err")}},
	}}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "ssh", "--", "echo", "hi"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "hello-out") {
		t.Errorf("remote stdout must go to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "hello-err") {
		t.Errorf("remote stderr must go to stderr, got %q", stderr)
	}
	if strings.Contains(stdout, "hello-err") {
		t.Errorf("stdout must not carry remote stderr: %q", stdout)
	}
}

func TestVMSSHJSONSingleEnvelope(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 0, Stdout: []byte("out"), Stderr: []byte("")}},
	}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "ssh", "--json", "--", "echo", "hi"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "vm.ssh" {
		t.Errorf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["exit_code"].(float64) != 0 || data["stdout"] != "out" {
		t.Errorf("data = %v, want exit_code=0 stdout=out", data)
	}
}

func TestVMSSHRemoteNonZeroIsNotSuccess(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 1, Stderr: []byte("boom")}},
	}}
	// Human mode: exit non-zero, remote stderr still routed to stderr.
	code, _, stderr := runVMWithFake(t, []string{"vm", "ssh", "--", "false"}, fake)
	if code == int(ExitOK) {
		t.Fatalf("remote non-zero must not be reported as success (exit 0)")
	}
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d (external)", code, int(ExitExternal))
	}
	if !strings.Contains(stderr, "boom") {
		t.Errorf("remote stderr should still be routed to stderr, got %q", stderr)
	}

	// JSON mode: exactly one envelope, ok=false.
	fake = &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 1, Stderr: []byte("boom")}},
	}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "ssh", "--json", "--", "false"}, fake)
	if code != int(ExitExternal) {
		t.Fatalf("json exit = %d, want %d", code, int(ExitExternal))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
}

// --- JSON error envelopes carry the concrete command name ---

func TestVMStatusJSONErrorHasConcreteCommand(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{err: errors.New("exec: \"limactl\": not found")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "status", "--json"}, fake)
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d", code, int(ExitExternal))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	if env["command"] != "vm.status" {
		t.Errorf("command = %v, want vm.status", env["command"])
	}
}

func TestVMStartJSONPreconditionHasConcreteCommand(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("other", "Running")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "start", "--json"}, fake)
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d", code, int(ExitPrecondition))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	if env["command"] != "vm.start" {
		t.Errorf("command = %v, want vm.start", env["command"])
	}
}

func TestVMSSHJSONRemoteNonZeroHasConcreteCommand(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: execx.Result{ExitCode: 1, Stderr: []byte("boom")}},
	}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "ssh", "--json", "--", "false"}, fake)
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d", code, int(ExitExternal))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	if env["command"] != "vm.ssh" {
		t.Errorf("command = %v, want vm.ssh", env["command"])
	}
}

// --- vm stop ---

func TestVMStopSuccessHumanAndJSON(t *testing.T) {
	// Human: Running → stop → Stopped.
	fake := &fakeLimaRunner{script: []scriptedResp{
		{res: listJSON("hermes-box", "Running")},
		{res: execx.Result{ExitCode: 0}},
		{res: listJSON("hermes-box", "Stopped")},
	}}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "stop"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if strings.TrimSpace(stdout) != "hermes-box: stopped" {
		t.Errorf("stdout = %q, want %q", stdout, "hermes-box: stopped\n")
	}

	// JSON: same three-step script.
	fake = &fakeLimaRunner{script: []scriptedResp{
		{res: listJSON("hermes-box", "Running")},
		{res: execx.Result{ExitCode: 0}},
		{res: listJSON("hermes-box", "Stopped")},
	}}
	code, stdout, _ = runVMWithFake(t, []string{"vm", "stop", "--json"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("json exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "vm.stop" {
		t.Errorf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["name"] != "hermes-box" || data["state"] != "stopped" {
		t.Errorf("data = %v, want name=hermes-box state=stopped", data)
	}
}

func TestVMStopAlreadyStoppedIsIdempotent(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("hermes-box", "Stopped")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "stop"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0 (idempotent stopped)", code)
	}
	if strings.TrimSpace(stdout) != "hermes-box: stopped" {
		t.Errorf("stdout = %q, want stopped", stdout)
	}
	if len(fake.calls) != 1 {
		t.Errorf("callCount = %d, want 1 (no `limactl stop` when already stopped)", len(fake.calls))
	}
}

func TestVMStopNotFoundIsPrecondition(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("other", "Running")}}}
	code, _, _ := runVMWithFake(t, []string{"vm", "stop"}, fake)
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d (precondition)", code, int(ExitPrecondition))
	}
}

func TestVMStopBinaryUnavailableIsExternal(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{err: errors.New("exec: \"limactl\": not found")}}}
	code, _, _ := runVMWithFake(t, []string{"vm", "stop"}, fake)
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d (external)", code, int(ExitExternal))
	}
}

func TestVMStopJSONErrorHasConcreteCommand(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("other", "Running")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "stop", "--json"}, fake)
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d", code, int(ExitPrecondition))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false || env["command"] != "vm.stop" {
		t.Errorf("envelope = %v, want ok=false command=vm.stop", env)
	}
}

// --- vm bootstrap ---

// bootstrapHappyResp is the ordered CLI-runner script for a fully-reconciled,
// fully-verified target (mirrors internal/lima's bootstrapHappyScript).
func bootstrapHappyResp() []scriptedResp {
	out := func(s string) execx.Result { return execx.Result{ExitCode: 0, Stdout: []byte(s)} }
	return []scriptedResp{
		{res: listJSON("hermes-box", "Running")},                  // list precondition
		{res: out("hermes docker\n")},                             // id -nG hermes
		{res: execx.Result{ExitCode: 0}},                          // test -x target
		{res: out("/home/hermes/hermes-agent/venv/bin/hermes\n")}, // readlink shim (correct)
		{res: out("aarch64\n")},                                   // uname -m
		{res: out("Hermes Agent v0.19.0 (2026.7.20)\n")},          // hermes --version
		{res: out("29.6.2\n")},                                    // docker server version
		{res: out("git version 2.43.0\n")},                        // git --version
		{res: out("directory\n")},                                 // stat path0
		{res: out("ext4 /dev/vda1\n")},                            // findmnt path0
		{res: out("directory\n")},                                 // stat path1
		{res: out("ext4 /dev/vda1\n")},                            // findmnt path1
		{res: execx.Result{ExitCode: 1}},                          // findmnt host-shares (none)
	}
}

func TestVMBootstrapSuccessJSON(t *testing.T) {
	fake := &fakeLimaRunner{script: bootstrapHappyResp()}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "bootstrap", "--json", "--timeout", "5m"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "vm.bootstrap" {
		t.Fatalf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["instance"] != "hermes-box" {
		t.Errorf("instance = %v, want hermes-box", data["instance"])
	}
	if data["kb_path"] != "/home/hermes/.hermes" {
		t.Errorf("kb_path = %v, want /home/hermes/.hermes", data["kb_path"])
	}
	checks, _ := data["checks"].([]any)
	if len(checks) == 0 {
		t.Fatalf("checks empty; want the full verified set")
	}
	for _, c := range checks {
		m, _ := c.(map[string]any)
		if m["ok"] != true {
			t.Errorf("check %v not ok", m["name"])
		}
	}
}

func TestVMBootstrapSuccessHumanHasHandoff(t *testing.T) {
	fake := &fakeLimaRunner{script: bootstrapHappyResp()}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "bootstrap", "--timeout", "5m"}, fake)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	// The handoff must identify the persistent KB location for the operator.
	if !strings.Contains(stdout, "/home/hermes/.hermes") {
		t.Errorf("human output must surface the persistent KB path; got %q", stdout)
	}
}

func TestVMBootstrapNotRunningIsPrecondition(t *testing.T) {
	fake := &fakeLimaRunner{script: []scriptedResp{{res: listJSON("hermes-box", "Stopped")}}}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "bootstrap", "--json"}, fake)
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d (precondition)", code, int(ExitPrecondition))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false || env["command"] != "vm.bootstrap" {
		t.Errorf("envelope = %v, want ok=false command=vm.bootstrap", env)
	}
}

func TestVMBootstrapVerificationFailureIsExit6(t *testing.T) {
	// Arch mismatch → verification failure → exit 6, with the failing check
	// surfaced in the redacted error details.
	s := bootstrapHappyResp()
	s[4] = scriptedResp{res: execx.Result{ExitCode: 0, Stdout: []byte("x86_64\n")}}
	fake := &fakeLimaRunner{script: s}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "bootstrap", "--json", "--timeout", "5m"}, fake)
	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d (verification)", code, int(ExitVerification))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false || env["command"] != "vm.bootstrap" {
		t.Errorf("envelope = %v, want ok=false command=vm.bootstrap", env)
	}
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "VERIFICATION_FAILED" {
		t.Errorf("error.code = %v, want VERIFICATION_FAILED", errObj["code"])
	}
}

func TestVMBootstrapHumanErrorNoStdoutContamination(t *testing.T) {
	s := bootstrapHappyResp()
	s[4] = scriptedResp{res: execx.Result{ExitCode: 0, Stdout: []byte("x86_64\n")}}
	fake := &fakeLimaRunner{script: s}
	code, stdout, stderr := runVMWithFake(t, []string{"vm", "bootstrap", "--timeout", "5m"}, fake)
	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d", code, int(ExitVerification))
	}
	if stdout != "" {
		t.Errorf("human error mode: stdout must be empty, got %q", stdout)
	}
	if !strings.Contains(stderr, "arch") {
		t.Errorf("stderr should carry the failing check diagnostic, got %q", stderr)
	}
}

// --- vm parent / unknown subcommands ---

func TestVMNoSubcommandIsUsage(t *testing.T) {
	fake := &fakeLimaRunner{}
	code, _, _ := runVMWithFake(t, []string{"vm"}, fake)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, int(ExitUsage))
	}
}

func TestVMUnknownSubcommandIsUsageWithJSONEnvelope(t *testing.T) {
	fake := &fakeLimaRunner{}
	code, stdout, _ := runVMWithFake(t, []string{"vm", "bogus", "--json"}, fake)
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, int(ExitUsage))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
}
