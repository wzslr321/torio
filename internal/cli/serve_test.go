package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/serve"
)

// fakeServeGuest is a local serve.Guest double for the cli package. It routes on
// the joined argv, mirroring the guest surface serve issues, so serve-command
// envelope/exit-code wiring is tested without a real VM. Only the knobs the cli
// tests need are configurable; everything else is a clean success.
type fakeServeGuest struct {
	installed    bool
	lingerYes    bool
	verifyOK     bool
	active       string
	enabled      string
	endpointCode string
	endpointBody string
	transportErr error
}

func okServeGuest() *fakeServeGuest {
	return &fakeServeGuest{
		installed:    true,
		lingerYes:    true,
		verifyOK:     true,
		active:       "active",
		enabled:      "enabled",
		endpointCode: "200",
		endpointBody: `{"version":"0.19.0"}`,
	}
}

func (g *fakeServeGuest) route(argv []string) (execx.Result, error) {
	if g.transportErr != nil {
		return execx.Result{ExitCode: -1}, g.transportErr
	}
	j := strings.Join(argv, " ")
	switch {
	case strings.Contains(j, "id -u hermes"):
		return execx.Result{Stdout: []byte("1000\n")}, nil
	case strings.Contains(j, "loginctl show-user"):
		if g.lingerYes {
			return execx.Result{Stdout: []byte("Linger=yes\n")}, nil
		}
		return execx.Result{Stdout: []byte("Linger=no\n")}, nil
	case strings.Contains(j, "systemd-analyze --user verify"):
		if g.verifyOK {
			return execx.Result{}, nil
		}
		return execx.Result{ExitCode: 1, Stderr: []byte("bad unit\n")}, nil
	case strings.Contains(j, "test -f "):
		if g.installed {
			return execx.Result{}, nil
		}
		return execx.Result{ExitCode: 1}, nil
	case strings.Contains(j, "cat "):
		return execx.Result{ExitCode: 1, Stderr: []byte("No such file\n")}, nil // always fresh
	case strings.Contains(j, "is-enabled"):
		code := 0
		if g.enabled != "enabled" {
			code = 1
		}
		return execx.Result{ExitCode: code, Stdout: []byte(g.enabled + "\n")}, nil
	case strings.Contains(j, "is-active"):
		code := 0
		if g.active != "active" {
			code = 3
		}
		return execx.Result{ExitCode: code, Stdout: []byte(g.active + "\n")}, nil
	case strings.Contains(j, "journalctl"):
		return execx.Result{Stdout: []byte("-- No entries --\n")}, nil
	case strings.Contains(j, "curl"):
		return execx.Result{Stdout: []byte(g.endpointBody + "\n" + g.endpointCode)}, nil
	}
	// mkdir, tee, mv, rm, daemon-reload, enable, start/stop/restart → success.
	return execx.Result{}, nil
}

func (g *fakeServeGuest) SSH(_ context.Context, command []string) (execx.Result, error) {
	return g.route(command)
}
func (g *fakeServeGuest) SSHInput(_ context.Context, _ []byte, command []string) (execx.Result, error) {
	return g.route(command)
}

func runServeWithGuest(t *testing.T, args []string, g serve.Guest) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	a := &app{
		stdout:   &stdout,
		stderr:   &stderr,
		build:    testBuild(),
		newServe: func() *serve.Adapter { return serve.New(g, lima.Hermes()) },
	}
	code := runWithApp(context.Background(), a, args)
	return code, stdout.String(), stderr.String()
}

func TestServeNoSubcommandIsUsage(t *testing.T) {
	code, _, _ := runServeWithGuest(t, []string{"serve"}, okServeGuest())
	if code != int(ExitUsage) {
		t.Fatalf("exit = %d, want %d (usage)", code, int(ExitUsage))
	}
}

func TestServeInstallHumanAndJSON(t *testing.T) {
	code, stdout, stderr := runServeWithGuest(t, []string{"serve", "install"}, okServeGuest())
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, lima.HermesUnitName) || !strings.Contains(stdout, "enabled (boot): true") {
		t.Errorf("human install output unexpected: %q", stdout)
	}

	code, stdout, _ = runServeWithGuest(t, []string{"serve", "install", "--json"}, okServeGuest())
	if code != int(ExitOK) {
		t.Fatalf("json exit = %d, want 0", code)
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != true || env["command"] != "serve.install" {
		t.Errorf("unexpected envelope: %v", env)
	}
	data, _ := env["data"].(map[string]any)
	if data["enabled"] != true || data["validated"] != true {
		t.Errorf("data = %v, want enabled+validated", data)
	}
}

func TestServeInstallValidationFailureIsVerification(t *testing.T) {
	g := okServeGuest()
	g.verifyOK = false
	code, stdout, _ := runServeWithGuest(t, []string{"serve", "install", "--json"}, g)
	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d (verification)", code, int(ExitVerification))
	}
	env := decodeOneEnvelope(t, stdout)
	if env["ok"] != false {
		t.Errorf("expected an error envelope, got %v", env)
	}
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_FAILED" {
		t.Errorf("error code = %v, want VALIDATION_FAILED", errObj["code"])
	}
}

func TestServeStartReadyEnvelope(t *testing.T) {
	code, stdout, stderr := runServeWithGuest(t, []string{"serve", "start", "--json"}, okServeGuest())
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	env := decodeOneEnvelope(t, stdout)
	data, _ := env["data"].(map[string]any)
	if data["ready"] != true || data["endpoint_ready"] != true {
		t.Errorf("data = %v, want ready+endpoint_ready", data)
	}
	if data["version"] != "0.19.0" {
		t.Errorf("version = %v, want 0.19.0", data["version"])
	}
}

func TestServeStatusActiveButEndpointDeadIsVerification(t *testing.T) {
	g := okServeGuest()
	g.active = "active"
	g.endpointCode = "000" // endpoint down; status does not poll, so this is prompt
	code, stdout, _ := runServeWithGuest(t, []string{"serve", "status", "--json"}, g)
	if code != int(ExitVerification) {
		t.Fatalf("exit = %d, want %d (verification)", code, int(ExitVerification))
	}
	env := decodeOneEnvelope(t, stdout)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "ENDPOINT_UNREADY" {
		t.Errorf("error code = %v, want ENDPOINT_UNREADY", errObj["code"])
	}
	// The report must be surfaced in details so the operator sees the state.
	details, _ := errObj["details"].(map[string]any)
	if details["active"] != true {
		t.Errorf("details.active = %v, want true (process active but endpoint dead)", details["active"])
	}
}

func TestServeStatusNotInstalledIsPrecondition(t *testing.T) {
	g := okServeGuest()
	g.installed = false
	code, _, _ := runServeWithGuest(t, []string{"serve", "status"}, g)
	if code != int(ExitPrecondition) {
		t.Fatalf("exit = %d, want %d (precondition)", code, int(ExitPrecondition))
	}
}

func TestServeStopSuccess(t *testing.T) {
	g := okServeGuest()
	g.active = "inactive" // idempotent stop
	code, stdout, _ := runServeWithGuest(t, []string{"serve", "stop"}, g)
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "stopped") {
		t.Errorf("stop output unexpected: %q", stdout)
	}
}

func TestServeLogsSuccess(t *testing.T) {
	code, stdout, _ := runServeWithGuest(t, []string{"serve", "logs", "--lines", "10"}, okServeGuest())
	if code != int(ExitOK) {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "No entries") {
		t.Errorf("logs output unexpected: %q", stdout)
	}
}

func TestServeTransportErrorIsExternal(t *testing.T) {
	g := okServeGuest()
	g.transportErr = context.DeadlineExceeded
	code, _, _ := runServeWithGuest(t, []string{"serve", "status"}, g)
	if code != int(ExitExternal) {
		t.Fatalf("exit = %d, want %d (external)", code, int(ExitExternal))
	}
}

func TestMapServeErrorExitCodes(t *testing.T) {
	cases := []struct {
		kind serve.ErrorKind
		want ExitCode
	}{
		{serve.KindNotInstalled, ExitPrecondition},
		{serve.KindInactive, ExitPrecondition},
		{serve.KindPostconditionFailed, ExitPrecondition},
		{serve.KindEndpointUnready, ExitVerification},
		{serve.KindValidationFailed, ExitVerification},
		{serve.KindTransport, ExitExternal},
		{serve.KindTimeout, ExitExternal},
		{serve.KindCancelled, ExitExternal},
		{serve.KindGuestCommandFailed, ExitExternal},
	}
	for _, tc := range cases {
		err := &serve.Error{Op: "status", Kind: tc.kind}
		ce := mapServeError("serve.status", err)
		if ce.Exit != tc.want {
			t.Errorf("kind %s → exit %d, want %d", tc.kind, ce.Exit, tc.want)
		}
	}
}
