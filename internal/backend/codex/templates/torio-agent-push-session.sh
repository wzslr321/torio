#!/bin/bash
#
# torio-agent-push-session, an agent session that may ask to push.
#
# Bootstrap installs this file root-owned and 0755, and proves it on every run.
# It is a second helper rather than a flag on torio-agent-session, and that is
# the point: the ordinary helper is provably free of SSH_AUTH_SOCK, a test
# forbids the string from appearing in it, and a session opened through it can
# reach no remote at all. Widening that file would have cost the guarantee for
# every session in order to add it to some.
#
# What the socket carries is not a key. The host end is Torio's own agent, which
# holds one pinned identity and asks the operator, on their Mac, before every
# signature (ADR-0015). The agent here can ask; it cannot answer.
#
# The host supplies two values, the project path and the socket path, and both
# are validated here as well as there. The host is a caller, not a trusted input
# source, and the command this helper runs is a constant below.
set -euo pipefail

workspace='/home/codex/projects'
agent_user='codex'
agent_command='/usr/local/bin/codex'
shared_group='torio-projects'
project_id_pattern='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
# The socket path is fixed in shape and unpredictable in content: the random
# component is chosen on the host per session, so nothing can sit waiting at the
# path. sshd refuses to bind over an existing file and the transport is opened
# with ExitOnForwardFailure, so a squatted path fails the session rather than
# quietly handing the agent somebody else's socket.
socket_pattern='^/tmp/torio-push-[0-9a-f]{32}\.sock$'

die() {
  printf 'torio-agent-push-session: %s\n' "$1" >&2
  exit 64
}

[ "$#" -eq 2 ] || die 'expected exactly two arguments: the project path and the agent socket'

project="$1"
socket="$2"

case "$project" in
  "$workspace"/*) ;;
  *) die "project path is not a project directly under $workspace" ;;
esac

project_id="${project#"$workspace"/}"
if [[ ! $project_id =~ $project_id_pattern ]]; then
  die 'project id is not an allowed single path segment'
fi
if [[ ! $socket =~ $socket_pattern ]]; then
  die 'agent socket path is not the shape this helper accepts'
fi

[ ! -L "$project" ] || die 'project path is a symlink'
[ -d "$project" ] || die 'project directory does not exist'
[ "$(id -u)" -ne 0 ] || die 'refusing to open an agent session as root'
[ -x "$agent_command" ] || die 'the pinned agent binary is missing; run torio vm bootstrap'

# -L before -S: the socket test follows symlinks, so a link would otherwise be
# accepted on the strength of whatever it points at. -O proves this session's own
# ssh created it and not another process that got there first.
[ ! -L "$socket" ] || die 'agent socket path is a symlink'
[ -S "$socket" ] || die 'agent socket is not a socket'
[ -O "$socket" ] || die 'agent socket is not owned by this session'

# sshd creates the forwarded socket owned by the operator and unreadable by
# anyone else, so the agent identity cannot connect to it as it stands. Handing
# it over is a deliberate act and this is it: the shared group both identities
# already belong to, and no wider. Doing it here, as the operator who owns the
# socket, is why no sshd setting has to be loosened for every forward the machine
# will ever carry.
chgrp "$shared_group" -- "$socket" || die 'could not hand the agent socket to the shared group'
chmod 0770 -- "$socket" || die 'could not set the agent socket mode'

cd -- "$project"

printf 'torio: agent session in %s, running as %s.\n' "$project_id" "$agent_user"
printf 'torio: this session may ask to push. Every signature stops at a dialog on your Mac and is recorded there.\n'

# The variable is set by env, run as the agent identity, rather than preserved
# across sudo: --preserve-env would need a sudoers grant, and buying an
# environment variable with standing authority is the wrong trade. -H sets HOME
# so the agent's credential and configuration resolve; the working directory is
# inherited from the cd above.
exec sudo -n -u "$agent_user" -H -- env "SSH_AUTH_SOCK=$socket" "$agent_command"
