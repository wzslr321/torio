package codex

import (
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
)

// TestTheOrdinarySessionCannotReachARemote is the guarantee the second helper
// exists to protect. A session opened through this file can reach no remote at
// all, and the cheapest way to keep that true is to forbid the string that would
// undo it.
func TestTheOrdinarySessionCannotReachARemote(t *testing.T) {
	if strings.Contains(string(embeddedAgentSession), "SSH_AUTH_SOCK") {
		t.Fatal("the ordinary session helper mentions the forwarded agent socket")
	}
}

// TestBothHelpersSpeakThisBackendsOwnNames pins the substitutions that make the
// shared scripts this backend's. A helper carrying another backend's workspace
// would validate a path no project of this one lives under.
func TestBothHelpersSpeakThisBackendsOwnNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
	}{
		{"session", string(embeddedAgentSession)},
		{"push session", string(embeddedAgentPushSession)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range []string{
				"workspace='" + WorkspacePath + "'",
				"agent_user='" + User + "'",
				"agent_command='" + commandPath + "'",
			} {
				if !strings.Contains(tc.script, want) {
					t.Errorf("the %s helper does not declare %s", tc.name, want)
				}
			}
			// The host validates the project path too. This is the second half of
			// that, and it is here because the host is a caller rather than a
			// trusted input source.
			if !strings.Contains(tc.script, "project_id_pattern=") {
				t.Errorf("the %s helper does not re-validate the project id", tc.name)
			}
			if !strings.Contains(tc.script, "refusing to open an agent session as root") {
				t.Errorf("the %s helper would open a session as root", tc.name)
			}
		})
	}
}

// TestThePushHelperProvesTheSocketBeforeHandingItOver pins the checks that stand
// between a forwarded socket and the agent identity. Each one answers a
// different way of arriving at the wrong socket.
func TestThePushHelperProvesTheSocketBeforeHandingItOver(t *testing.T) {
	script := string(embeddedAgentPushSession)

	if !strings.Contains(script, `socket_pattern='^/tmp/torio-push-[0-9a-f]{32}\.sock$'`) {
		t.Error("the push helper accepts a socket path of a shape the host does not create")
	}
	for _, want := range []string{
		`[ ! -L "$socket" ]`, // a link would be accepted on the strength of its target
		`[ -S "$socket" ]`,   // not a socket at all
		`[ -O "$socket" ]`,   // somebody else's socket that got there first
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the push helper does not check %s", want)
		}
	}
	if !strings.Contains(script, `chgrp "$shared_group"`) {
		t.Error("the push helper does not hand the socket to the shared group, so the agent cannot use it")
	}
}

// TestLoginIsAConstantArgvThatLandsInTheAgentsOwnHome pins both halves of
// starting the agent outside a project. Nothing an operator typed reaches this
// command, and it must not be re-parsed into more than one word by the remote
// shell it is sent to.
func TestLoginIsAConstantArgvThatLandsInTheAgentsOwnHome(t *testing.T) {
	argv := New().Session().LoginArgv
	if len(argv) == 0 {
		t.Fatal("codex takes a credential of its own but declares no way to grant one")
	}

	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--chdir="+Home) {
		t.Error("login does not start in the agent's own home; -H sets HOME and not the working directory")
	}
	if argv[len(argv)-1] != "--device-auth" || !strings.Contains(joined, commandPath+" login") {
		t.Errorf("login argv is %v, want the device-code flow the pinned binary offers", argv)
	}
	// The box is headless and the operator is on another machine. A flow that
	// wanted a browser on the guest would need a forwarded port to work at all.
	for _, element := range argv {
		if strings.ContainsAny(element, "|&;<>()$`\\\"'\n") {
			t.Errorf("login argv element %q holds a metacharacter a remote shell would re-parse", element)
		}
	}
}

// TestTheRetrievalSkillIsAddressedToThisAgent pins that the skill names the
// tools this agent actually has. The other backend's skill names Grep, Glob and
// Read, which Codex does not have, so installing it here would tell the agent to
// call tools that do not exist against a directory that does not either.
func TestTheRetrievalSkillIsAddressedToThisAgent(t *testing.T) {
	skill := New().BrainSkill()
	if !skill.Installable() {
		t.Fatal("codex discovers skills but Torio installs none, so the vault has no retrieval surface")
	}
	if skill.Root != ProfilePath+"/skills" {
		t.Errorf("skill root is %q, want the directory Codex discovers skills in", skill.Root)
	}
	// Codex routes by reading each skill's description, so there is no static
	// index to hold a position in and no category to declare.
	if skill.Category != "" {
		t.Errorf("skill category is %q; this backend has no index that orders skills", skill.Category)
	}

	doc := string(skill.Payload)
	if !strings.Contains(doc, "name: "+backend.BrainSkillName) {
		t.Errorf("the skill is not named %s, which is what documentation and errors tell the operator to look for", backend.BrainSkillName)
	}
	if !strings.Contains(doc, BrainPath) {
		t.Error("the skill does not name this identity's vault path")
	}
	for _, foreign := range []string{"/home/claude", "`Grep`", "`Glob`", "`Read`"} {
		if strings.Contains(doc, foreign) {
			t.Errorf("the skill mentions %s, which belongs to another backend", foreign)
		}
	}
	// The guest image ships neither ripgrep nor anything else beyond the base
	// tools, so a skill that told the agent to run rg would fail on a real box.
	if strings.Contains(doc, "rg ") {
		t.Error("the skill tells the agent to run a tool the guest does not have")
	}
	if !strings.Contains(doc, "grep") {
		t.Error("the skill does not say how to search the vault with what the guest has")
	}
}
