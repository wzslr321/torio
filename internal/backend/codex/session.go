package codex

import (
	_ "embed"

	"github.com/wzslr321/torio/internal/backend"
)

// AgentSessionHelper is the fixed guest entry point of an agent session. Like
// the other session helpers it is root-owned and takes exactly one validated
// project path: the host is a caller, not a trusted input source, and the
// command that runs inside the session is a constant in the helper rather than
// something composed on the host and sent over.
const AgentSessionHelper = "/usr/local/bin/torio-agent-session"

//go:embed templates/torio-agent-session.sh
var embeddedAgentSession []byte

// AgentPushSessionHelper is the entry point of a session that may ask to push.
//
// It is a second file rather than a flag on the first. The ordinary helper is
// provably free of SSH_AUTH_SOCK, a test forbids the string from appearing in
// it, and a session opened through it can reach no remote at all. Widening that
// file would have spent the guarantee for every session to add the capability to
// some (ADR-0015).
const AgentPushSessionHelper = "/usr/local/bin/torio-agent-push-session"

//go:embed templates/torio-agent-push-session.sh
var embeddedAgentPushSession []byte

func (codexBackend) Session() *backend.SessionSpec {
	return &backend.SessionSpec{
		HelperPath:     AgentSessionHelper,
		Helper:         embeddedAgentSession,
		LoginArgv:      loginArgv(),
		PushHelperPath: AgentPushSessionHelper,
		PushHelper:     embeddedAgentPushSession,
	}
}

// loginArgv starts the credential flow in the agent's own home. Every element is
// a constant: nothing an operator typed reaches this command, and no element
// holds a metacharacter, because this is an argv sent over ssh and re-parsed by
// a remote shell.
//
// The device-code flow is the one asked for rather than the default. Signing in
// through a browser starts a callback server on the guest's loopback, which
// nothing on the operator's machine can reach without a forwarded port; asking
// for a code instead moves the browser to where the operator already is. An API
// key remains available for an operator who prefers one, through `codex login
// --with-api-key`, which reads the key from standard input; it is not this argv
// because a key is not something to put on a command line.
//
// Both halves of "its own home" have to be said, because -H says only one. It
// sets HOME, and the working directory is inherited from the ssh session, which
// lands in the operator's home, a directory this identity is deliberately
// forbidden to traverse. `env --chdir` rather than `sudo -D`: sudo refuses the
// latter unless sudoers grants a working directory, and buying a cwd with a
// sudoers grant would be paying in authority for a convenience.
func loginArgv() []string {
	return []string{"sudo", "-n", "-u", User, "-H", "--", "env", "--chdir=" + Home, "--",
		commandPath, "login", "--device-auth"}
}

// BrainSkill installs the retrieval skill into the directory Codex discovers
// skills in, `$CODEX_HOME/skills`, where one copy is visible from every checkout
// the agent is started in.
//
// There is no category. Codex routes by reading each skill's description, so a
// skill is not competing for a position in a static index the way it is on the
// Hermes backend, which is the entire reason that mechanism exists there and the
// reason it is absent here.
//
// The payload is this backend's own text. It names the tools this agent has,
// which are the shell and what the guest image ships, and the vault path this
// identity owns. The other process backend's skill names neither: it tells the
// agent to call Grep, Glob and Read against a home that does not exist here.
func (codexBackend) BrainSkill() backend.BrainSkill {
	return backend.BrainSkill{Root: ProfilePath + "/skills", Payload: embeddedBrainSkill}
}

//go:embed templates/skill/SKILL.md
var embeddedBrainSkill []byte
