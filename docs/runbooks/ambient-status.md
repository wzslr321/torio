# Keeping an eye on several agents

You run more than one box. One is working, one finished ten minutes ago and is
waiting for you, one is a VM you stopped last week. You want to know which is
which without asking.

`torio status` answers that in one line per box. Everything below is
configuration for tools you already run — a status bar, a shell prompt, a
notifier. None of it is Torio, and none of it needs to be: the command emits a
document, and the document is the whole interface.

The document's shape is in [`../contracts/status.md`](../contracts/status.md).
Read that before writing a recipe of your own, above all the part about `state`:
a field with `"state": "unknown"` still carries a `false` beside it, and a recipe
that reads the `false` will tell you an agent is fine when nobody could reach it.

## A tmux status line

```jq
# ~/.config/torio/status.jq
def cell:
  if   .box != "running"                              then "\(.instance) off"
  elif .waiting.state == "known" and .waiting.waiting  then "\(.instance) NEEDS YOU"
  elif .session.state == "known"                       then "\(.instance) \(.session.sessions | length)"
  elif .session.state == "not-applicable"              then "\(.instance) —"
  else                                                      "\(.instance) ?" end;
[ .data.instances[] | cell ] | join("  │  ")
```

```tmux
# ~/.tmux.conf
set -g status-interval 15
set -g status-right "#(torio status --json | jq -rf ~/.config/torio/status.jq)"
```

Which on a two-box host reads:

```text
torio —  │  torio-claude-code NEEDS YOU
```

The `—` is Hermes reporting that a session is not a thing it has, not a box with
nothing running. Keeping the two apart is the point of the glyph; if you collapse
them into `0` you will eventually read a box you cannot see into as quiet.

Fifteen seconds is a reasonable interval. The poll costs one host-side
`limactl list` plus a handful of small guest commands per running box, and takes
well under a second on two boxes — but it is a poll, so pick an interval you are
happy running forever rather than the smallest one that works.

## A shell prompt segment

Same document, shorter output. This one prints nothing at all when nothing wants
you, so the prompt stays quiet on a normal day:

```bash
torio_waiting() {
  torio status --json 2>/dev/null \
    | jq -r '[.data.instances[] | select(.waiting.state=="known" and .waiting.waiting) | .instance] | join(" ")'
}
```

A prompt runs this on every command, so put it behind a cache if your boxes are
many or your machine is busy — a file written by the tmux poll above, read by the
prompt, is enough.

## Being told, rather than looking

The poll tells you within one interval. If you want to know sooner, have
something ring a bell when a box changes:

```bash
#!/usr/bin/env bash
# torio-watch — notify when a box starts waiting for you
set -euo pipefail
previous=""
while sleep 20; do
  current=$(torio status --json | jq -rc '[.data.instances[]
    | select(.waiting.state=="known" and .waiting.waiting) | .instance]')
  [ "$current" = "$previous" ] && continue
  previous=$current
  [ "$current" = "[]" ] && continue
  osascript -e "display notification \"$current\" with title \"Torio: an agent needs you\""
done
```

There is deliberately no `torio status --watch`. A foreground watcher would be
the first long-lived Torio process outside an interactive session, and it buys
freshness rather than correctness — so it belongs in a script you own, next to
the notifier you already use.

## Reading a row

| Column | `—` means | `?` means |
|---|---|---|
| `SESSION` | this backend has no such thing as a session process | the box could not be read |
| `WAITING` | this backend answers no waiting question | it has no way to say, or its marker was too old, or not private, or unreadable |
| `PROGRESS` | this backend writes no evidence of work | nothing has been written yet, or the box could not be read |

A stopped box reports `0` sessions and `no` waiting — proven, without entering
it, because nothing runs in a VM that is not running — and `?` for progress,
because that evidence is inside it.

`torio status` says nothing about whether one box is healthy. For that, ask the
box: `torio backend status` walks its bootstrap checks, and `torio serve status`
proves whether its guest service is actually answering.

## When everything says `?`

Ask again with `--verbose`. Each field that could not be proven is logged with
the box it belongs to and the reason, on stderr, without disturbing the document
on stdout:

```console
$ torio status --verbose --json >/dev/null
time=2026-08-08T22:28:55+02:00 level=DEBUG msg="status fact unproven" instance=torio-claude-code fact=processes reason="bounded guest output was truncated"
```

The `fact` names which of the readings failed: `clock`, `processes`, `paths` or
`waiting`.

A whole row of `?` usually means the box is running but not reachable — try
`torio vm ssh -- true` against it.

## Waiting never appears on a Claude Code box

The marker is written by hooks that `torio vm bootstrap` installs. A box
bootstrapped before those existed keeps the previous managed settings, and
bootstrap refuses to overwrite them — drift is reported, never repaired in
place. Remove the file it names and run bootstrap again:

```console
$ torio vm bootstrap --backend claude-code
torio: lima bootstrap: verification_failed: claude_managed_settings: managed settings
content has drifted from the version Torio installs (inspect
/etc/claude-code/managed-settings.json; if it is the previous version Torio shipped,
remove it and re-run `torio vm bootstrap` to install the current one)
```

On Hermes the field reads `?` and will keep reading `?`. Hermes knows truthfully
when it is blocked on an approval, but the answer lives in the memory of the
running process and is written nowhere a poll can read. Until it is exported,
unknown is the honest answer — and it is deliberately not `no`, because an
operator told an agent is not waiting stops looking at it.
