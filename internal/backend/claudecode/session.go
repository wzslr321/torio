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
//
// Both halves of "its own home" have to be said, because -H says only one. It
// sets HOME, and the working directory is inherited from the ssh session, which
// lands in the *operator's* home — a directory this identity is deliberately
// forbidden to traverse. Claude Code resolves project-scoped settings from the
// working directory, so the first thing the operator saw was the agent failing
// to stat two files inside a home that is not its own, offering to repair them.
// Nothing was broken; it had been started somewhere it cannot look.
//
// The chdir runs after sudo, as the agent identity into its own home, so it
// does not depend on the operator being able to reach that directory either.
// `env --chdir` rather than `sudo -D`: sudo refuses the latter unless sudoers
// grants a working directory, and buying a cwd with a sudoers grant would be
// paying in authority for a convenience.
//
// The agent session helper has the same requirement and meets it with a plain
// `cd`, because it is a script. This is an argv sent over ssh and re-parsed by
// a remote shell, so it must hold no metacharacter to be re-parsed.
func loginArgv() []string {
	return []string{"sudo", "-n", "-u", User, "-H", "--", "env", "--chdir=" + Home, "--", commandPath}
}
