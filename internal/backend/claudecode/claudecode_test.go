package claudecode

import (
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/lima"
)

// TestBackendDeclaresTheShapeItActuallyHas is the contract test that matters
// for a process backend: it declares no registry and no service, and a nil
// there is what the rest of Torio reads to avoid inventing a check or a
// failure. Declaring one it does not have would be worse than declaring none.
func TestBackendDeclaresTheShapeItActuallyHas(t *testing.T) {
	b := New()
	if b.Registry() != nil {
		t.Error("Registry() is non-nil: Claude Code keeps no project registry")
	}
	if b.Service() != nil {
		t.Error("Service() is non-nil: Claude Code is a per-session process, not a daemon")
	}
	if b.Session() == nil {
		t.Fatal("Session() is nil: the backend's only surface is an interactive session")
	}
	if got := b.Identity().Name; got != "claude-code" {
		t.Errorf("Identity().Name = %q, want %q", got, "claude-code")
	}
}

// TestTheAgentIdentityIsNeverTheOperator pins the decision the whole custody
// story rests on. The Lima login user holds passwordless root on the guest, so
// an agent running as it would sit above every control the guest enforces —
// including the checks in this package.
func TestTheAgentIdentityIsNeverTheOperator(t *testing.T) {
	id := New().Identity()
	if id.GuestUser == "" {
		t.Fatal("the backend declares no guest user")
	}
	for _, forbidden := range []string{"root", "lima", "ubuntu"} {
		if id.GuestUser == forbidden {
			t.Errorf("guest user is %q, which is not a dedicated agent identity", forbidden)
		}
	}
	// Every path the backend owns must live under its own home, so nothing it
	// writes lands where another identity's state is.
	for _, p := range []string{id.ProfilePath, id.BrainPath, id.WorkspacePath} {
		if !strings.HasPrefix(p, id.Home+"/") {
			t.Errorf("path %q is not under the identity's home %q", p, id.Home)
		}
	}
}

// TestAllowedGroupsAreExactlyTheOnesNeeded pins the group set as a closed list.
// The check that reads it refuses anything not named here, so this test is
// where an added group has to be argued for rather than noticed later.
func TestAllowedGroupsAreExactlyTheOnesNeeded(t *testing.T) {
	got := allowedGroups()
	want := []string{User, lima.TorioProjectsGroup, lima.TorioMCPClientsGroup}
	if len(got) != len(want) {
		t.Fatalf("allowedGroups() = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allowedGroups() = %v, want %v", got, want)
		}
	}
}

// TestRequiredPathsKeepTheCredentialDirectoryPrivate pins that the profile
// directory may be tightened but never widened. Claude Code chmods it when it
// writes a credential; a spec that demanded exact equality would leave every
// box permanently unbootstrapped after its first login, which is the lesson the
// Hermes profile path already taught once.
func TestRequiredPathsKeepTheCredentialDirectoryPrivate(t *testing.T) {
	for _, spec := range New().RequiredPaths() {
		switch spec.Path {
		case ProfilePath, BrainPath:
			if !spec.AllowStricter {
				t.Errorf("%s does not accept a stricter mode; first use will unbootstrap the box", spec.Path)
			}
			if spec.Group != User {
				t.Errorf("%s group = %q, want the identity's own group: nothing else may read it", spec.Path, spec.Group)
			}
		case WorkspacePath:
			if spec.Group != lima.TorioProjectsGroup {
				t.Errorf("%s group = %q, want %q so the operator can work the same tree", spec.Path, spec.Group, lima.TorioProjectsGroup)
			}
			if spec.AllowStricter {
				t.Errorf("%s accepts a stricter mode; the operator's access through the shared group is load-bearing", spec.Path)
			}
		}
	}
}

// TestProvisionScriptCreatesRootOwnedDirectoriesForRootOwnedThings pins that
// the two directories holding root-owned content are created root-owned. If the
// agent owned either, the install pin and the managed settings would both be
// files the agent could replace between sessions.
func TestProvisionScriptCreatesRootOwnedDirectoriesForRootOwnedThings(t *testing.T) {
	script := New().ProvisionScript()
	for _, dir := range []string{managedSettingsDir, installDir} {
		want := "install -d -o root -g root -m 0755 " + dir
		if !strings.Contains(script, want) {
			t.Errorf("provision script does not create %s root-owned (want %q)", dir, want)
		}
	}
	if !strings.Contains(script, "gpasswd -d "+User+" docker") {
		t.Error("provision script does not remove the agent identity from the docker group")
	}
}

// The root-owned hook helper parses Claude's hook document with jq. That
// parser is a runtime dependency of the backend guardrail and must be
// provisioned deliberately rather than inherited accidentally from one image.
func TestProvisionScriptInstallsTheWaitingMarkerParser(t *testing.T) {
	script := New().ProvisionScript()
	if !strings.Contains(script, "apt-get install -y --no-install-recommends jq") {
		t.Error("provision script does not install jq for the waiting-marker helper")
	}
}

// TestManagedSettingsMatchGolden locks the exact bytes the box installs. What
// the box tells the agent about permissions and the updater is security-
// relevant, so changing it must be a reviewed change to a file rather than a
// side effect of editing a string.
func TestManagedSettingsMatchGolden(t *testing.T) {
	got := string(ManagedSettings())
	for _, want := range []string{
		`"defaultMode": "bypassPermissions"`,
		`"allowManagedMcpServersOnly": true`,
		`"DISABLE_AUTOUPDATER": "1"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("managed settings do not carry %s:\n%s", want, got)
		}
	}
	// The updater is disabled here as a guardrail; the boundary is that the
	// binary lives in a root-owned directory the agent cannot write. If this
	// ever becomes the only thing stopping a self-update, the install is wrong.
	if !strings.Contains(installDir, "/usr/local/lib/") {
		t.Errorf("install directory %q is not a root-owned system location", installDir)
	}
}

// TestAgentSessionHelperRunsAFixedCommandAsTheAgent pins the helper's shape.
// It is root-owned on the guest and takes one validated path; what it runs, and
// as whom, must be constants in the file rather than anything the host sent.
func TestAgentSessionHelperRunsAFixedCommandAsTheAgent(t *testing.T) {
	script := string(AgentSession())
	for _, want := range []string{
		`exec sudo -n -u "$agent_user" -H -- "$agent_command"`,
		`agent_user='` + User + `'`,
		`agent_command='` + commandPath + `'`,
		`workspace='` + WorkspacePath + `'`,
		`[ "$(id -u)" -ne 0 ] || die 'refusing to open an agent session as root'`,
		`[ ! -L "$project" ] || die 'project path is a symlink'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("agent session helper is missing %q", want)
		}
	}
	if strings.Contains(script, "ForwardAgent=yes") || strings.Contains(script, "SSH_AUTH_SOCK") {
		t.Error("agent session helper touches agent forwarding; the session must carry no push capability")
	}
}

// TestLoginArgvCarriesNoCallerInput is why the login path needs no root-owned
// helper: a helper exists to stop a host-composed value from becoming a remote
// command, and there is no such value here.
func TestLoginArgvCarriesNoCallerInput(t *testing.T) {
	want := []string{"sudo", "-n", "-u", User, "-H", "--", "env", "--chdir=" + Home, "--", commandPath}
	got := loginArgv()
	if len(got) != len(want) {
		t.Fatalf("loginArgv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("loginArgv() = %v, want %v", got, want)
		}
	}
}

// TestLoginStartsTheAgentInItsOwnHome pins the rule -H does not cover. -H sets
// HOME; the working directory is inherited from the ssh session, which lands in
// the operator's home — a directory this identity cannot traverse. Claude Code
// resolves project-scoped settings from the working directory, so starting it
// there made it report two unreadable settings files and offer to repair them,
// on a box where nothing was wrong.
func TestLoginStartsTheAgentInItsOwnHome(t *testing.T) {
	argv := loginArgv()
	var chdir string
	for _, a := range argv {
		if strings.HasPrefix(a, "--chdir=") {
			chdir = strings.TrimPrefix(a, "--chdir=")
		}
	}
	if chdir != Home {
		t.Fatalf("login argv starts the agent in %q, want its own home %q: %v", chdir, Home, argv)
	}
	// After sudo, so the chdir happens as the agent identity into a directory
	// it owns, rather than depending on the operator being able to reach it.
	sudo, chdirAt := -1, -1
	for i, a := range argv {
		if a == "sudo" && sudo < 0 {
			sudo = i
		}
		if strings.HasPrefix(a, "--chdir=") {
			chdirAt = i
		}
	}
	if sudo < 0 || chdirAt < sudo {
		t.Fatalf("the working directory is chosen before the identity is: %v", argv)
	}
}

// TestLoginArgvSurvivesARemoteShell pins the constraint that rules out the
// obvious fix. This argv is joined with spaces by ssh and re-parsed by the
// operator's login shell on the guest, so a `cd X && exec Y` — which is exactly
// what the agent session helper does, because it is a script and not an argv —
// would be re-parsed rather than run.
func TestLoginArgvSurvivesARemoteShell(t *testing.T) {
	for _, arg := range loginArgv() {
		if strings.ContainsAny(arg, "&|;<>()$`\\\"' \t\n*?[]{}!~#") {
			t.Errorf("login argv element %q carries a character a remote shell re-parses", arg)
		}
	}
}

// TestAgentPushSessionHelperIsTheOnlyOneThatTouchesTheSocket keeps the split
// that justifies having two helpers at all. The default helper's guarantee — a
// session that can reach no remote, provable by the absence of a string — is
// only worth something while the default helper is the one every ordinary
// session opens.
func TestAgentPushSessionHelperIsTheOnlyOneThatTouchesTheSocket(t *testing.T) {
	if strings.Contains(string(AgentSession()), "SSH_AUTH_SOCK") {
		t.Error("the default agent session helper gained SSH_AUTH_SOCK; the grant belongs in the second helper")
	}
	if !strings.Contains(string(AgentPushSession()), "SSH_AUTH_SOCK") {
		t.Error("the push session helper does not hand the agent a socket")
	}
	if AgentSessionHelper == AgentPushSessionHelper {
		t.Error("both sessions resolve to one guest path; bootstrap could not tell them apart")
	}
}

// TestAgentPushSessionHelperValidatesWhatTheHostSent pins the guest-side half of
// the two-sided validation. The host is a caller, not a trusted input source,
// and the socket it names decides which agent the session talks to.
func TestAgentPushSessionHelperValidatesWhatTheHostSent(t *testing.T) {
	script := string(AgentPushSession())
	for _, want := range []string{
		`socket_pattern='^/tmp/torio-push-[0-9a-f]{32}\.sock$'`,
		`[ "$#" -eq 2 ] || die 'expected exactly two arguments: the project path and the agent socket'`,
		`[[ ! $socket =~ $socket_pattern ]]`,
		`[ ! -L "$socket" ] || die 'agent socket path is a symlink'`,
		`[ -S "$socket" ] || die 'agent socket is not a socket'`,
		`[ -O "$socket" ] || die 'agent socket is not owned by this session'`,
		`chgrp "$shared_group" -- "$socket"`,
		`chmod 0770 -- "$socket"`,
		`exec sudo -n -u "$agent_user" -H -- env "SSH_AUTH_SOCK=$socket" "$agent_command"`,
		`[ "$(id -u)" -ne 0 ] || die 'refusing to open an agent session as root'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("push session helper is missing %q", want)
		}
	}
	// The two negative checks read the executable lines only. The helper's
	// comments explain which alternatives were rejected and why, and a guard that
	// could not tell an explanation from an instruction would force those
	// explanations out of the file to stay green.
	code := shellCode(script)
	// The socket is handed to the shared group and no wider. World access would
	// put it in reach of anything else that ever runs on the guest.
	if strings.Contains(code, "chmod 0777") || strings.Contains(code, "chmod a+rw") {
		t.Error("push session helper opens the socket beyond the shared group")
	}
	// --preserve-env would need a sudoers grant; buying an environment variable
	// with standing authority is the trade this deliberately does not make.
	if strings.Contains(code, "--preserve-env") || strings.Contains(code, "SETENV") {
		t.Error("push session helper buys the environment variable with sudo authority")
	}
}

// shellCode drops comment and blank lines, leaving what the script executes.
func shellCode(script string) string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
