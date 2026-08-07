package claudecode

import _ "embed"

// AgentSessionHelper is the fixed guest entry point of an agent session. Like
// the other session helpers it is root-owned and takes exactly one validated
// project path: the host is a caller, not a trusted input source, and the
// command that runs inside the session is a constant in the helper rather than
// something composed on the host and sent over.
const AgentSessionHelper = "/usr/local/bin/torio-agent-session"

//go:embed templates/torio-agent-session.sh
var embeddedAgentSession []byte

// AgentSession returns the helper's exact bytes, exported so a test can lock
// them: what an agent session runs, and as whom, is not something to discover
// from a diff.
func AgentSession() []byte { return embeddedAgentSession }

// loginArgv starts the agent in its own home so its login flow can run. Every
// element is a constant: nothing an operator typed reaches this command.
func loginArgv() []string {
	return []string{"sudo", "-n", "-u", User, "-H", "--", commandPath}
}
