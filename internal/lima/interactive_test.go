package lima

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// limaSSHConfigHost prepares the one host precondition every session spec has —
// a canonical Lima ssh config for the target instance — under a fake HOME, and
// returns that HOME.
//
// It is a fixture rather than the real host because these are unit tests: a spec
// builder that reads $HOME/.lima passes on any machine that happens to have the
// instance running and fails everywhere else, which is exactly how the agent
// session specs came to be green locally and red on CI.
func limaSSHConfigHost(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	cfgDir := filepath.Join(home, ".lima", InstanceName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("creating lima config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "ssh.config"), []byte("Host lima-"+InstanceName+"\n"), 0o600); err != nil {
		t.Fatalf("writing lima ssh config: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

// operatorShellHost adds the second precondition an operator session has and an
// agent session deliberately has not: a running SSH agent. They are separate so
// that a spec which started requiring an agent socket it should never have
// would fail rather than be handed one by a shared fixture.
func operatorShellHost(t *testing.T) string {
	t.Helper()

	home := limaSSHConfigHost(t)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(home, "agent.sock"))
	return home
}

// TestOperatorShellSpecBuildsThePromotedArgv pins the exact argv proven against
// Lima 2.2.0 and OpenSSH 10.2p1: -F first, then the -o overrides, then -A. That
// order is what kept agent forwarding working when a stale ControlMaster socket
// was already open; reversed, Lima's own ssh.config wins and the session gets no
// agent. The remote side is a fixed guest helper plus the project path — two
// argv elements, never a command string.
func TestOperatorShellSpecBuildsThePromotedArgv(t *testing.T) {
	home := operatorShellHost(t)

	spec, err := OperatorShellSpec(testWorkspacePath, "/home/agent/projects/demo")
	if err != nil {
		t.Fatalf("OperatorShellSpec: unexpected error: %v", err)
	}

	if spec.Name != "ssh" {
		t.Errorf("Name = %q, want %q", spec.Name, "ssh")
	}
	want := []string{
		"-F", filepath.Join(home, ".lima", InstanceName, "ssh.config"),
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ForwardAgent=yes",
		"-A",
		"-t",
		"lima-" + InstanceName,
		OperatorShellHelper,
		"/home/agent/projects/demo",
	}
	if !equalArgs(spec.Args, want) {
		t.Fatalf("argv = %v, want %v", spec.Args, want)
	}
}

// TestProjectEnterSpecBuildsAnInteractiveSessionWithoutAgentForwarding pins the
// ordinary workspace-session boundary: it gets a TTY and the fixed enter
// helper, but explicitly disables agent forwarding and multiplexing so it
// cannot reuse a push-capable operator-shell connection.
func TestProjectEnterSpecBuildsAnInteractiveSessionWithoutAgentForwarding(t *testing.T) {
	home := operatorShellHost(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	spec, err := ProjectEnterSpec(testWorkspacePath, testWorkspacePath+"/demo")
	if err != nil {
		t.Fatalf("ProjectEnterSpec: unexpected error: %v", err)
	}

	want := []string{
		"-F", filepath.Join(home, ".lima", InstanceName, "ssh.config"),
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ForwardAgent=no",
		"-a",
		"-t",
		"lima-" + InstanceName,
		ProjectEnterHelper,
		testWorkspacePath + "/demo",
	}
	if spec.Name != "ssh" || !equalArgs(spec.Args, want) {
		t.Fatalf("command = %s %v, want ssh %v", spec.Name, spec.Args, want)
	}
	if spec.Env != nil {
		t.Fatalf("Env = %v, want nil", spec.Env)
	}
}

func TestMCPLoginSpecForwardsOnlyTheLoopbackCallbackAndRunsFixedBrokerLogin(t *testing.T) {
	home := limaSSHConfigHost(t)
	t.Setenv("SSH_AUTH_SOCK", "/must/not/be/used")

	spec, err := MCPLoginSpec("atlassian")
	if err != nil {
		t.Fatalf("MCPLoginSpec: %v", err)
	}
	want := []string{
		"-F", filepath.Join(home, ".lima", InstanceName, "ssh.config"),
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ForwardAgent=no",
		"-o", "ExitOnForwardFailure=yes",
		"-a",
		"-L", "127.0.0.1:43119:127.0.0.1:43119",
		"lima-" + InstanceName,
		"sudo", "-n", "-u", TorioMCPUser, "--",
		TorioMCPBrokerPath, "login", "atlassian",
	}
	if spec.Name != "ssh" || !equalArgs(spec.Args, want) {
		t.Fatalf("command = %s %v, want ssh %v", spec.Name, spec.Args, want)
	}
	if strings.Contains(strings.Join(spec.Args, " "), "ForwardAgent=yes") {
		t.Fatal("MCP login forwards the operator's SSH agent")
	}
}

func TestMCPLoginSpecRejectsAnInvalidServiceBeforeReadingHostState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := MCPLoginSpec("../../escape"); err == nil {
		t.Fatal("invalid service was accepted")
	}
}

// TestOperatorShellSpecPutsOverridesAfterTheConfigFlag pins the ordering rule:
// -F first, every -o after it. Lima's generated ssh.config enables
// ControlMaster/ControlPersist, so an override placed before -F loses, a stale
// multiplexing socket is reused, and agent forwarding is silently poisoned.
func TestOperatorShellSpecPutsOverridesAfterTheConfigFlag(t *testing.T) {
	operatorShellHost(t)

	spec, err := OperatorShellSpec(testWorkspacePath, testWorkspacePath+"/demo")
	if err != nil {
		t.Fatalf("OperatorShellSpec: unexpected error: %v", err)
	}

	configAt := -1
	for i, arg := range spec.Args {
		switch {
		case arg == "-F" && configAt < 0:
			configAt = i
		case arg == "-o":
			if configAt < 0 {
				t.Fatalf("argv has an -o override at %d before -F: %v", i, spec.Args)
			}
		}
	}
	if configAt < 0 {
		t.Fatalf("argv does not pass -F at all: %v", spec.Args)
	}
	for _, override := range []string{"ControlMaster=no", "ControlPath=none", "ForwardAgent=yes"} {
		if !containsArg(spec.Args, override) {
			t.Errorf("argv is missing the %q override: %v", override, spec.Args)
		}
	}
	if !containsArg(spec.Args, "-A") {
		t.Errorf("argv does not forward the agent: %v", spec.Args)
	}
}

// TestOperatorShellSpecNeverBackgroundsTheSession pins the other half of the
// ssh rule: -n is required only for a backgrounded session, and it
// redirects stdin from /dev/null. This session is the operator's foreground
// terminal, so -n (and its relatives) must never appear.
func TestOperatorShellSpecNeverBackgroundsTheSession(t *testing.T) {
	operatorShellHost(t)

	spec, err := OperatorShellSpec(testWorkspacePath, testWorkspacePath+"/demo")
	if err != nil {
		t.Fatalf("OperatorShellSpec: unexpected error: %v", err)
	}
	for _, forbidden := range []string{"-n", "-N", "-f"} {
		if containsArg(spec.Args, forbidden) {
			t.Errorf("argv detaches the session with %q: %v", forbidden, spec.Args)
		}
	}
	if !containsArg(spec.Args, "-t") {
		t.Errorf("argv does not force a TTY for the remote helper: %v", spec.Args)
	}
}

// TestOperatorShellSpecInheritsTheOperatorEnvironment proves Torio does not
// compose an environment for the session. A nil Env is what carries the
// operator's SSH_AUTH_SOCK, TERM and locale into ssh; building one here would
// mean copying credential-bearing values through Torio.
func TestOperatorShellSpecInheritsTheOperatorEnvironment(t *testing.T) {
	operatorShellHost(t)

	spec, err := OperatorShellSpec(testWorkspacePath, testWorkspacePath+"/demo")
	if err != nil {
		t.Fatalf("OperatorShellSpec: unexpected error: %v", err)
	}
	if spec.Env != nil {
		t.Errorf("Env = %v, want nil so the session inherits the operator's environment", spec.Env)
	}
}

// TestOperatorShellSpecNeverBuildsARemoteCommandString proves the remote side
// is two argv elements — the fixed helper and the validated project path — and
// that no element is a concatenated command. There is no `sh -c`, no quoting,
// and nothing for a caller to inject a command into.
func TestOperatorShellSpecNeverBuildsARemoteCommandString(t *testing.T) {
	operatorShellHost(t)

	path := testWorkspacePath + "/demo"
	spec, err := OperatorShellSpec(testWorkspacePath, path)
	if err != nil {
		t.Fatalf("OperatorShellSpec: unexpected error: %v", err)
	}

	remote := spec.Args[len(spec.Args)-2:]
	if remote[0] != OperatorShellHelper || remote[1] != path {
		t.Errorf("remote argv = %v, want [%q %q]", remote, OperatorShellHelper, path)
	}
	for i, arg := range spec.Args {
		if strings.ContainsAny(arg, " \t\n;&|$`'\"") {
			t.Errorf("argv[%d] = %q looks like a command string, not a single token", i, arg)
		}
	}
}

// TestOperatorShellSpecRequiresARunningSSHAgent proves the session is refused
// when the host has no agent to forward. Without it -A forwards nothing: the
// operator would land in the project with no write capability and only find
// out when the push fails (ADR-0003 — write capability comes from the macOS
// agent, and only for the duration of the session).
func TestOperatorShellSpecRequiresARunningSSHAgent(t *testing.T) {
	operatorShellHost(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, err := OperatorShellSpec(testWorkspacePath, "/home/agent/projects/demo")
	if err == nil {
		t.Fatalf("OperatorShellSpec = nil error, want a refusal when no agent is running")
	}
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error = %v, want a *lima.Error", err)
	}
	if lerr.Op != operatorShellOp {
		t.Errorf("Op = %q, want %q", lerr.Op, operatorShellOp)
	}
	if lerr.Kind != KindNotFound {
		t.Errorf("Kind = %q, want %q", lerr.Kind, KindNotFound)
	}
	if !strings.Contains(lerr.Error(), "SSH_AUTH_SOCK") {
		t.Errorf("error = %q, want it to name the missing precondition", lerr.Error())
	}
}

// TestOperatorShellSpecRequiresTheCanonicalSSHConfig proves a missing instance
// ssh config is refused up front and named. Without it ssh would fall back to
// the operator's own ~/.ssh/config and resolve "lima-torio" against whatever
// that file happens to say — the session must be built against Lima's own
// generated config or not at all.
func TestOperatorShellSpecRequiresTheCanonicalSSHConfig(t *testing.T) {
	home := operatorShellHost(t)
	cfg := filepath.Join(home, ".lima", InstanceName, "ssh.config")
	if err := os.Remove(cfg); err != nil {
		t.Fatalf("removing the lima ssh config: %v", err)
	}

	_, err := OperatorShellSpec(testWorkspacePath, "/home/agent/projects/demo")
	if err == nil {
		t.Fatalf("OperatorShellSpec = nil error, want a refusal when the instance ssh config is missing")
	}
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error = %v, want a *lima.Error", err)
	}
	if lerr.Kind != KindNotFound {
		t.Errorf("Kind = %q, want %q", lerr.Kind, KindNotFound)
	}
	if !strings.Contains(lerr.Error(), cfg) {
		t.Errorf("error = %q, want it to name the expected config path %q", lerr.Error(), cfg)
	}
}

// TestOperatorShellSpecRejectsInvalidProjectPaths proves the only caller-shaped
// input is constrained to exactly one project directory directly under the
// guest workspace, with a strict identifier. The remote side hands this value
// to the guest helper, so a traversal, a flag-shaped name, whitespace or a
// shell metacharacter must never get that far — and neither must a path
// pointing anywhere outside /home/agent/projects.
func TestOperatorShellSpecRejectsInvalidProjectPaths(t *testing.T) {
	operatorShellHost(t)

	cases := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", "demo"},
		{"outside the workspace", "/etc/passwd"},
		{"the workspace root itself", testWorkspacePath},
		{"trailing slash", testWorkspacePath + "/demo/"},
		{"nested below a project", testWorkspacePath + "/demo/src"},
		{"parent traversal", testWorkspacePath + "/../.ssh"},
		{"dot segment", testWorkspacePath + "/./demo"},
		{"flag-shaped id", testWorkspacePath + "/-oProxyCommand=touch /tmp/pwned"},
		{"space in id", testWorkspacePath + "/de mo"},
		{"shell metacharacters", testWorkspacePath + "/demo;rm -rf /"},
		{"command substitution", testWorkspacePath + "/demo$(id)"},
		{"newline", testWorkspacePath + "/demo\ntouch /tmp/pwned"},
		{"quote", testWorkspacePath + "/de'mo"},
		{"overlong id", testWorkspacePath + "/" + strings.Repeat("a", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := OperatorShellSpec(testWorkspacePath, tc.path)
			if err == nil {
				t.Fatalf("OperatorShellSpec(testWorkspacePath, %q) = %v, nil error; want a refusal", tc.path, spec.Args)
			}
			var lerr *Error
			if !errors.As(err, &lerr) {
				t.Fatalf("error = %v, want a *lima.Error", err)
			}
			if lerr.Kind != KindVerificationFailed {
				t.Errorf("Kind = %q, want %q", lerr.Kind, KindVerificationFailed)
			}
			if len(spec.Args) != 0 || spec.Name != "" {
				t.Errorf("a rejected path still produced a command: %s %v", spec.Name, spec.Args)
			}
		})
	}
}

// TestOperatorShellSpecAcceptsWellFormedProjectIDs is the positive control for
// the identifier rule: ordinary repository names must keep working.
func TestOperatorShellSpecAcceptsWellFormedProjectIDs(t *testing.T) {
	operatorShellHost(t)

	for _, id := range []string{"demo", "torio-box", "a.b_c-1", "A1", strings.Repeat("a", 64)} {
		path := testWorkspacePath + "/" + id
		spec, err := OperatorShellSpec(testWorkspacePath, path)
		if err != nil {
			t.Errorf("OperatorShellSpec(testWorkspacePath, %q) = %v, want it accepted", path, err)
			continue
		}
		if got := spec.Args[len(spec.Args)-1]; got != path {
			t.Errorf("last argv element = %q, want the project path %q", got, path)
		}
	}
}

// TestVMShellSpecOpensALoginShellWithoutForwardingOrACommand pins the shape of
// an operator shell into the box itself: the no-agent transport, a TTY, and no
// remote command at all, so sshd opens the login identity's own shell. There is
// no caller-shaped value anywhere in the argv, which is why no guest helper is
// needed — a helper exists to stop a host-composed value from becoming a
// remote command, and here there is none.
func TestVMShellSpecOpensALoginShellWithoutForwardingOrACommand(t *testing.T) {
	home := limaSSHConfigHost(t)

	spec, err := VMShellSpec()
	if err != nil {
		t.Fatalf("VMShellSpec: unexpected error: %v", err)
	}
	if spec.Name != "ssh" {
		t.Errorf("Name = %q, want %q", spec.Name, "ssh")
	}
	want := []string{
		"-F", filepath.Join(home, ".lima", InstanceName, "ssh.config"),
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ForwardAgent=no",
		"-a",
		"-t",
		"lima-" + InstanceName,
	}
	if !equalArgs(spec.Args, want) {
		t.Fatalf("argv = %v, want %v", spec.Args, want)
	}
	if spec.Env != nil {
		t.Errorf("Env = %v, want nil so the operator's terminal and locale pass through", spec.Env)
	}
}

// TestVMShellSpecFailsClosedWithoutTheBox names the remedy: a box that was
// never started has no ssh config, and the answer is the command that makes one.
func TestVMShellSpecFailsClosedWithoutTheBox(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := VMShellSpec()
	if err == nil {
		t.Fatal("VMShellSpec opened a session with no lima ssh config")
	}
	if !strings.Contains(err.Error(), "torio vm start") {
		t.Errorf("error = %v, want it to name `torio vm start`", err)
	}
}
