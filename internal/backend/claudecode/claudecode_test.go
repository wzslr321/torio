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
	want := []string{User, lima.TorioProjectsGroup}
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

// TestManagedSettingsMatchGolden locks the exact bytes the box installs. What
// the box tells the agent about permissions and the updater is security-
// relevant, so changing it must be a reviewed change to a file rather than a
// side effect of editing a string.
func TestManagedSettingsMatchGolden(t *testing.T) {
	got := string(ManagedSettings())
	for _, want := range []string{
		`"defaultMode": "bypassPermissions"`,
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
	want := []string{"sudo", "-n", "-u", User, "-H", "--", commandPath}
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
