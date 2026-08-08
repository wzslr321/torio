#!/bin/bash
#
# torio-agent-session — an agent session inside an attached project.
#
# Bootstrap installs this file root-owned and 0755, and proves it on every run.
# The host-side transport disables authentication-agent forwarding and SSH
# multiplexing before invoking this fixed helper, so no session opened here can
# reach a Git remote: the agent commits, a human pushes.
#
# The host supplies exactly one value — the project path — and it is validated
# here as well as there. The host is a caller, not a trusted input source, and
# the command this helper runs is a constant below rather than something the
# caller composed.
set -euo pipefail

workspace='/home/claude/projects'
agent_user='claude'
agent_command='/usr/local/bin/claude'
project_id_pattern='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'

die() {
  printf 'torio-agent-session: %s\n' "$1" >&2
  exit 64
}

[ "$#" -eq 1 ] || die 'expected exactly one argument: the project path'

project="$1"
case "$project" in
  "$workspace"/*) ;;
  *) die "project path is not a project directly under $workspace" ;;
esac

project_id="${project#"$workspace"/}"
if [[ ! $project_id =~ $project_id_pattern ]]; then
  die 'project id is not an allowed single path segment'
fi

[ ! -L "$project" ] || die 'project path is a symlink'
[ -d "$project" ] || die 'project directory does not exist'
[ "$(id -u)" -ne 0 ] || die 'refusing to open an agent session as root'
[ -x "$agent_command" ] || die 'the pinned agent binary is missing; run torio vm bootstrap'

cd -- "$project"

printf 'torio: agent session in %s, running as %s.\n' "$project_id" "$agent_user"
printf 'torio: no SSH agent is forwarded. The agent can commit here; pushing is yours, from torio project shell.\n'

# -H sets HOME to the agent identity's own home, so its credential, settings and
# skills resolve. The working directory is inherited, which is how the agent
# learns which project this is. The controlling terminal came from the operator's
# ssh -t and survives the exec, so the agent gets a real TTY.
exec sudo -n -u "$agent_user" -H -- "$agent_command"
