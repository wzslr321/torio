package lima

import (
	"strings"
	"testing"
)

const testAgentHelper = "/usr/local/bin/torio-agent-session"

// TestProjectAgentSpecCarriesNoPushCapability is the security property of the
// whole command. An agent session must not be able to reach a Git remote, and
// must not be able to inherit a connection that can — so the transport disables
// forwarding twice (the option and the flag) and disables multiplexing, exactly
// as the ordinary project session does.
//
// The flag order is load-bearing and pinned here for the same reason it is for
// the operator shell: Lima's own ssh.config sets ControlMaster/ControlPersist,
// and an override placed before -F loses to it. A session that then rode a
// multiplexed connection whose master was opened *with* forwarding would carry
// push capability nobody asked for.
func TestProjectAgentSpecCarriesNoPushCapability(t *testing.T) {
	limaSSHConfigHost(t)
	cmd, err := ProjectAgentSpec(testAgentHelper, testWorkspacePath, testWorkspacePath+"/demo")
	if err != nil {
		t.Fatalf("ProjectAgentSpec: %v", err)
	}
	if cmd.Name != "ssh" {
		t.Fatalf("Name = %q, want ssh", cmd.Name)
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"-o ControlMaster=no",
		"-o ControlPath=none",
		"-o ForwardAgent=no",
		"-a",
		"-t",
		testAgentHelper + " " + testWorkspacePath + "/demo",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv is missing %q: %v", want, cmd.Args)
		}
	}
	if strings.Contains(joined, "ForwardAgent=yes") || strings.Contains(joined, " -A") {
		t.Errorf("argv forwards the operator's SSH agent into an agent session: %v", cmd.Args)
	}
	// -F must come before the overrides, or Lima's own config wins.
	if got := indexOfArg(cmd.Args, "-F"); got != 0 {
		t.Errorf("-F is at position %d, want first: the -o overrides must follow it", got)
	}
}

// TestProjectAgentSpecRejectsAnythingButOneSegment pins that the single
// caller-shaped value in the argv is validated before it reaches a root-owned
// guest helper. The helper validates it again; neither side treats the other as
// a trusted input source.
func TestProjectAgentSpecRejectsAnythingButOneSegment(t *testing.T) {
	for _, p := range []string{
		"",
		"/etc/passwd",
		testWorkspacePath,
		testWorkspacePath + "/../etc",
		testWorkspacePath + "/nested/path",
		testWorkspacePath + "/-flag",
		testWorkspacePath + "/has space",
		testWorkspacePath + "/semi;colon",
	} {
		if _, err := ProjectAgentSpec(testAgentHelper, testWorkspacePath, p); err == nil {
			t.Errorf("ProjectAgentSpec accepted %q", p)
		}
	}
}

// TestProjectAgentSpecRefusesABackendWithNoHelper pins that a backend which
// declares no session cannot get one by default. An empty helper would
// otherwise render an ssh command whose remote argv is just a path.
func TestProjectAgentSpecRefusesABackendWithNoHelper(t *testing.T) {
	if _, err := ProjectAgentSpec("", testWorkspacePath, testWorkspacePath+"/demo"); err == nil {
		t.Fatal("ProjectAgentSpec built a session for a backend that declares none")
	}
}

// TestSessionSpecsValidateAgainstTheBackendsOwnWorkspace pins that the path
// validator is per-backend. A validator fixed to one backend's root would
// reject every legitimate path on the other — and the tempting fix, widening it
// to accept both, would let a path under one backend's workspace be handed to
// the other's helper.
func TestSessionSpecsValidateAgainstTheBackendsOwnWorkspace(t *testing.T) {
	const other = "/home/claude/projects"
	limaSSHConfigHost(t)

	if _, err := ProjectAgentSpec(testAgentHelper, other, other+"/demo"); err != nil {
		t.Fatalf("a path under the backend's own workspace was rejected: %v", err)
	}
	if _, err := ProjectAgentSpec(testAgentHelper, other, testWorkspacePath+"/demo"); err == nil {
		t.Error("a path under a different backend's workspace was accepted")
	}
	if _, err := ProjectAgentSpec(testAgentHelper, "", other+"/demo"); err == nil {
		t.Error("a spec was built with no workspace root at all")
	}
}

func indexOfArg(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}
