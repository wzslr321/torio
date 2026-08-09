#!/bin/bash
#
# torio-waiting-marker — record, or clear, that this agent is waiting on a human.
#
# Bootstrap installs this file root-owned and 0755, and proves it on every run.
# Claude Code runs it from the managed settings as one of its hooks, so it runs
# as the agent identity and writes into that identity's own home. Root ownership
# buys the same thing it buys for the settings themselves: the agent cannot
# retune what its own hooks do between sessions. The agent can still forge or
# remove its own marker; this is an operational signal, not a security boundary.
#
# It takes exactly one argument, and that argument is matched against a fixed
# list. Claude Code 2.1.220 passes the session id on standard input beside fields
# that contain agent-written prose. jq extracts only that bounded identifier;
# neither the rest of the payload nor any string from it reaches the marker.
set -euo pipefail
umask 077

marker="$HOME/.torio-waiting.json"
lock="$HOME/.torio-waiting.lock"
session_process='claude'

die() {
  printf 'torio-waiting-marker: %s\n' "$1" >&2
  exit 64
}

[ "$#" -eq 1 ] || die 'expected exactly one argument: a marker kind, or clear'

case "$1" in
  clear)
    action='clear'
    kind=''
    ;;
  permission|notification)
    action='set'
    kind="$1"
    ;;
  *)
    die 'unrecognized argument'
    ;;
esac

# The hook document has prose-bearing fields, but the common session_id field is
# a bounded identifier. The filter emits that field alone and rejects anything
# outside the alphabet the marker schema accepts.
session_id="$(/usr/bin/jq -er '
  .session_id
  | select(type == "string" and length > 0 and length <= 128)
  | select(test("^[A-Za-z0-9_-]+$"))
')" || die 'hook input has no valid session id'

# Two sessions can fire hooks concurrently. The fixed lock serializes updates
# to the one fixed marker path, avoiding both a lost update and any need for the
# host poll to enumerate agent-controlled filenames.
exec 9>"$lock"
/usr/bin/flock -w 1 9 || die 'could not lock the waiting marker'

# Which session is waiting, found by walking up the process tree to the nearest
# ancestor that is the agent itself. A hook runs as a child of the session that
# fired it, so the ancestor is the answer. If Claude changes what a session runs
# as, no ancestor matches and the hook fails closed: a per-session wait without
# a live process cannot be ranked below liveness.
waiting_pid=0
if [ "$action" = 'set' ]; then
  p=$$
  while [ -n "$p" ] && [ "$p" -gt 1 ]; do
    if [ "$(/usr/bin/cat "/proc/$p/comm" 2>/dev/null)" = "$session_process" ]; then
      waiting_pid=$p
      break
    fi
    p="$(/usr/bin/awk '/^PPid:/{print $2}' "/proc/$p/status" 2>/dev/null)"
  done
  [ "$waiting_pid" -gt 0 ] || die 'could not identify the waiting session process'
fi

now=0
if [ "$action" = 'set' ]; then
  now="$(/usr/bin/date +%s)"
fi

# Written through a staging file in the same directory so a poll never reads a
# half-written document, and created 0600 from the start rather than tightened
# afterwards: the reader refuses a marker anyone but its owner could write, and
# a window in which that is true is a window in which the marker is ignored.
tmp="$(/usr/bin/mktemp "$HOME/.torio-waiting.XXXXXX")"
trap '/usr/bin/rm -f -- "$tmp"' EXIT
/usr/bin/chmod 0600 "$tmp"

jq_filter='
  def valid_session_id:
    type == "string" and length > 0 and length <= 128
    and test("^[A-Za-z0-9_-]+$");
  def valid_kind: . == "permission" or . == "notification";
  def valid_wait:
    type == "object"
    and keys == ["kind", "pid", "session_id", "since_unix"]
    and (.session_id | valid_session_id)
    and (.kind | valid_kind)
    and (.pid | type == "number" and . > 0 and floor == .)
    and (.since_unix | type == "number" and . > 0 and floor == .);
  def valid_doc:
    type == "object"
    and keys == ["schema_version", "waits"]
    and .schema_version == "2"
    and (.waits | type == "array" and all(.[]; valid_wait));

  if . == null then {"schema_version":"2","waits":[]}
  elif valid_doc then .
  else error("invalid existing waiting marker")
  end
  | .waits |= map(select(.session_id != $session_id))
  | if $action == "set" then
      .waits += [{
        "session_id": $session_id,
        "kind": $kind,
        "pid": $pid,
        "since_unix": $now
      }]
    else . end
'

if [ -e "$marker" ]; then
  /usr/bin/jq -e \
    --arg action "$action" --arg session_id "$session_id" --arg kind "$kind" \
    --argjson pid "$waiting_pid" --argjson now "$now" \
    "$jq_filter" "$marker" >"$tmp" || die 'existing waiting marker is invalid'
else
  /usr/bin/jq -ne \
    --arg action "$action" --arg session_id "$session_id" --arg kind "$kind" \
    --argjson pid "$waiting_pid" --argjson now "$now" \
    "null | $jq_filter" >"$tmp"
fi

/usr/bin/sync -f "$tmp"
/usr/bin/mv -T -- "$tmp" "$marker"
/usr/bin/sync -f "$HOME"
trap - EXIT
