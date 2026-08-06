#!/bin/bash
#
# torio-project-shell — the guest entry point of an operator project session.
#
# The Torio Lima template installs this file root-owned and 0755, and
# `torio project shell` invokes it as the fixed remote argv of an ssh session
# that forwards the operator's host agent:
#
#   ssh … lima-torio /usr/local/bin/torio-project-shell /home/hermes/projects/<id>
#
# The host validates the project path before it builds that argv
# (internal/lima.validateProjectPath). This script validates it again, because
# the host is a caller and not a trusted input source. Everything else about the
# session is fixed here rather than passed in: the operator's own identity under
# the shared project group, never sudo and never root. That shape was measured
# on a live guest: entering through `sg torio-projects` reached the checkout and
# wrote in it without sudo, and the checkout was left carrying no root-owned
# files afterwards, which is what keeps `hermes` able to keep working in it.
set -euo pipefail

workspace='/home/hermes/projects'
group='torio-projects'

# Mirrors internal/lima.projectIDPattern: exactly one path segment below the
# workspace, so no traversal, no leading dash, no whitespace, no shell syntax,
# and a bounded length.
project_id_pattern='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'

# A refusal names the rule it enforced and never the value it rejected: that
# value is unvalidated at this point and would carry terminal escape sequences
# straight into the operator's terminal.
die() {
  printf 'torio-project-shell: %s\n' "$1" >&2
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

# A symlink would take the session outside the workspace while the prompt kept
# claiming the project. Refuse it rather than follow it.
[ ! -L "$project" ] || die 'project path is a symlink'
[ -d "$project" ] || die 'project directory does not exist'

# The session carries the operator's own identity. A root session would leave
# root-owned files in a checkout that hermes has to keep working in.
[ "$(id -u)" -ne 0 ] || die 'refusing to open a project session as root'
command -v sg >/dev/null 2>&1 || die 'sg is missing on this guest'

cd -- "$project"

# The operator's own terminal and this session look identical otherwise, and
# only one of them can push. --norc below keeps a guest ~/.bashrc from
# overwriting this prompt.
export PS1="torio:${project_id}"':\W\$ '

printf 'torio: project %s. Push capability is forwarded from your Mac for this session only and ends when you exit.\n' "$project_id"

# sg's -c runs its argument through sh, so bash is forced explicitly. That
# argument is a fixed constant: the project reaches the session as the
# inherited working directory and the prompt as the exported PS1, so nothing
# derived from the argument is ever concatenated into a command.
exec sg "$group" -c 'exec bash --norc -i'
