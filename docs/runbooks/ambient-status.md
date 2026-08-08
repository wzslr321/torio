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

Every recipe below calls `torio` by name and assumes the one on your `PATH` is
this build. Check before you debug the recipe — an older `torio` has no `status`
subcommand, exits 2, and every one of these renders as an empty string with no
error anywhere you would look:

```console
$ torio status >/dev/null && echo ok
ok
```

## The line, in plain text

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

Which on a two-box host renders:

```text
torio —  │  torio-claude-code NEEDS YOU
```

The `—` is Hermes reporting that a session is not a thing it has, not a box with
nothing running. Keeping the two apart is the point of the glyph; if you collapse
them into `0` you will eventually read a box you cannot see into as quiet.

Where that line goes depends on what you run. A terminal with a status bar of
its own — tmux, zellij, a multiplexer — has somewhere to put it. A terminal
without one does not, and the prompt is the only always-visible surface you
have; use the zsh recipe below, not the tmux one.

## In tmux

A status bar is glanced at, not read. The line above works there, but every box
arrives in the same colour, so finding the one that wants you means reading all
of them — which is the cost the bar existed to remove. tmux interprets its own
style sequences in the output of `#()`, so let the state arrive as colour and
shape and let the words be the confirmation:

```jq
# ~/.config/torio/tmux.jq
def age($s):
  if   $s < 60    then "\($s)s"
  elif $s < 3600  then "\(($s / 60)    | floor)m"
  elif $s < 86400 then "\(($s / 3600)  | floor)h"
  else                 "\(($s / 86400) | floor)d" end;

# Every box on this bar has "torio" in its name, so the derived half carries no
# information; where the name is one Torio derived, the backend is the half
# worth the width. A box named some other way keeps its own name.
def shortname:
  (.backend.name // "") as $b
  | if $b != "" and (.instance == "torio" or .instance == "torio-" + $b)
    then $b else .instance end;

def chip:
  shortname as $n
  | if .waiting.state == "known" and .waiting.waiting then
      "#[fg=#141927,bg=#f0b24a,bold] \($n) needs you \(age(.waiting.age_seconds // 0)) #[default]"
    elif .box != "running" then
      "#[fg=#4a5268]○ \($n) off#[default]"
    elif .session.state == "known" and (.session.sessions | length) > 0 then
      "#[fg=#5fd48a]●#[fg=#ccd3e8] \($n) \(.session.sessions | length)#[default]"
    elif .session.state == "known" then
      "#[fg=#4a5268]○ \($n)#[default]"
    elif .session.state == "not-applicable" and .last_progress.state == "known" then
      "#[fg=#8fb3ff]·#[fg=#8a93ad] \($n) \(age(.last_progress.age_seconds // 0))#[default]"
    elif .session.state == "not-applicable" then
      "#[fg=#4a5268]— \($n)#[default]"
    else
      "#[fg=#f0b24a]?#[fg=#8a93ad] \($n)#[default]"
    end;

[ .data.instances[] | chip ] | join("#[fg=#3a4159]  #[default]")
```

```tmux
# ~/.tmux.conf
set -g status-interval 15
set -g status-right-length 120
set -g status-right "#(torio status --json | jq -rf ~/.config/torio/tmux.jq)"
```

Exactly one state is loud: a box that wants you inverts — dark text on amber,
bold — so it is found without reading. Everything else recedes in proportion to
how little it asks of you: a live agent gets a green dot and its count, a box
that is merely working gets a muted middot and how long ago, a stopped or idle
box is barely there, and only a genuine unknown gets amber back.

The words stay because colour alone is not an answer. `needs you 7m` says how
long it has been waiting; a bar that only turned orange would leave you guessing
whether it just happened.

`status-right-length` is not optional decoration: tmux truncates the right-hand
status at forty characters by default, and two boxes already exceed that — the
end of the line, which is where the second box's state is, disappears without
saying so.

Nothing an agent wrote reaches these sequences. Every value in the document is
an instance name, a backend name, an enumerated kind or a number, which is a
property of the schema rather than of this recipe — see
[`../contracts/status.md`](../contracts/status.md).

Fifteen seconds is a reasonable interval. The poll costs one host-side
`limactl list` plus a handful of small guest commands per running box, and takes
well under a second on two boxes — but it is a poll, so pick an interval you are
happy running forever rather than the smallest one that works.

## In the prompt, with no multiplexer

A bare terminal has no status bar, so the prompt is the surface. The catch is
that a prompt is rendered synchronously: a command in it stalls the shell for as
long as it runs, and this one enters VMs.

So it does not run in the prompt. It runs after each command, in the background,
into a file the prompt reads — the prompt shows the state as of your last
command and never waits for anything:

```zsh
# ~/.zshrc
TORIO_STATUS_CACHE=${TMPDIR:-/tmp}/torio-status.txt

torio_status_refresh() {
  ( torio status --json 2>/dev/null \
      | jq -rf ~/.config/torio/status.jq > "$TORIO_STATUS_CACHE.tmp" 2>/dev/null \
      && mv -f "$TORIO_STATUS_CACHE.tmp" "$TORIO_STATUS_CACHE" ) &!
}
autoload -Uz add-zsh-hook
add-zsh-hook precmd torio_status_refresh

RPROMPT='%F{244}$(cat "$TORIO_STATUS_CACHE" 2>/dev/null)%f'
```

`&!` is zsh for "run it detached and do not report on it", which is what keeps a
slow poll from printing a job notice into your prompt. The rename is what keeps
the prompt from ever reading a half-written line.

If you would rather see nothing on a quiet day, swap the jq file for the shorter
question — which boxes want you — and let the prompt stay empty the rest of the
time:

```zsh
RPROMPT='%F{214}$(cat "$TORIO_STATUS_CACHE" 2>/dev/null)%f'
```

```jq
# ~/.config/torio/waiting.jq
[ .data.instances[] | select(.waiting.state == "known" and .waiting.waiting) | .instance ] | join(" ")
```

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

## Which session is it

`SESSION` counts every agent process on the box, so two open sessions read as
`2`, and `WAITING` names the one that spoke:

```console
$ torio status
INSTANCE           BOX      BACKEND      SESSION  WAITING                    PROGRESS
torio-claude-code  running  claude-code  2        notification 7m pid 11673  —
```

Match that pid against the sessions to find it by how long it has been open,
which is usually how you remember which window it is:

```console
$ torio status --json | jq -r '.data.instances[]
    | select(.waiting.state=="known" and .waiting.waiting)
    | .waiting.pid as $p
    | .session.sessions[] | select(.pid == $p)
    | "waiting session: pid \(.pid), open for \(.age_seconds)s"'
waiting session: pid 11673, open for 683s
```

One caveat, and it is the reason to read the count and the flag together. There
is one marker per box: two sessions waiting at once are one marker, the second
to speak overwrites the first, and answering in either clears it for both. So a
box with one session waiting and one being answered reports not-waiting until
the waiting one next speaks.

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
