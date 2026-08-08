#!/bin/bash
#
# torio-waiting-marker — record, or clear, that this agent is waiting on a human.
#
# Bootstrap installs this file root-owned and 0755, and proves it on every run.
# Claude Code runs it from the managed settings as one of its hooks, so it runs
# as the agent identity and writes into that identity's own home. Root ownership
# buys the same thing it buys for the settings themselves: the agent cannot
# retune what its own hooks do between sessions.
#
# It takes exactly one argument, and that argument is matched against a fixed
# list. Nothing a hook passes on standard input is read: Claude Code feeds hooks
# a JSON document that carries the session's own text, and this file's whole
# purpose is that a status surface can render it without rendering anything an
# agent wrote.
set -euo pipefail

marker="$HOME/.torio-waiting.json"

die() {
  printf 'torio-waiting-marker: %s\n' "$1" >&2
  exit 64
}

[ "$#" -eq 1 ] || die 'expected exactly one argument: a marker kind, or clear'

case "$1" in
  clear)
    rm -f -- "$marker"
    exit 0
    ;;
  permission|notification)
    kind="$1"
    ;;
  *)
    die 'unrecognized argument'
    ;;
esac

# Written through a staging file in the same directory so a poll never reads a
# half-written document, and created 0600 from the start rather than tightened
# afterwards: the reader refuses a marker anyone but its owner could write, and
# a window in which that is true is a window in which the marker is ignored.
tmp="$(mktemp "$HOME/.torio-waiting.XXXXXX")"
trap 'rm -f -- "$tmp"' EXIT
chmod 0600 "$tmp"
printf '{"schema_version":"1","kind":"%s"}\n' "$kind" >"$tmp"
mv -T -- "$tmp" "$marker"
trap - EXIT
