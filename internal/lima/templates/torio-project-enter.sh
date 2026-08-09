#!/bin/bash
#
# torio-project-enter — ordinary interactive work in an attached project.
#
# The Torio Lima template installs this file root-owned and 0755. The host-side
# transport explicitly disables authentication-agent forwarding and SSH
# multiplexing before invoking this fixed helper.
set -euo pipefail

workspace='__TORIO_WORKSPACE_ROOT__'
group='torio-projects'
project_id_pattern='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'

die() {
  printf 'torio-project-enter: %s\n' "$1" >&2
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
[ "$(id -u)" -ne 0 ] || die 'refusing to open a project session as root'
command -v sg >/dev/null 2>&1 || die 'sg is missing on this guest'

cd -- "$project"
export PS1="torio:${project_id}"':\W\$ '

printf 'torio: project %s. No SSH agent is forwarded; Git remote write capability is not enabled by this session.\n' "$project_id"

exec sg "$group" -c 'exec bash --norc -i'
