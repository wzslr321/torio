---
output: docs/runbooks/ambient-status.md
---

# Keeping an eye on several agents

You run more than one box. One is working, one finished ten minutes ago and is
waiting for you, one is a VM you stopped last week. You want to know which is
which without asking.

`torio status` answers that in one line per box. This runbook is about the other
half: putting that answer somewhere you already look, so you stop asking.

## The bar, in one command

```console
$ torio status setup tmux >> ~/.tmux.conf
$ tmux source-file ~/.tmux.conf
```

That prints a tmux block and appends it. Read it first if you like — the command
only writes to stdout, and the redirect above is your decision, not its
behaviour. It never edits a file itself.

What lands on the bar is one chip per box:

```text
● claude-code 2    · hermes 14s
```

Exactly one state is loud. A box that wants you inverts — dark text on amber,
bold — so it is found without reading:

```text
 claude-code needs you 7m 
```

Everything else recedes in proportion to how little it asks of you: a live agent
gets a green dot and its count, a backend that keeps no session process gets a
muted middot and how long ago it last did work, a stopped or idle box is barely
there, and only a genuine unknown gets amber back.

The words stay because colour alone is not an answer. `needs you 7m` says how
long it has been waiting; a chip that only turned amber would leave you guessing
whether it just happened.

Three lines in that block are load-bearing, and worth knowing before you edit
them:

- `status-right-length 120` — tmux truncates the right-hand status at forty
  characters by default, and two boxes already exceed that. The end of the line,
  which is where the second box is, disappears without saying so.
- `status-interval 15` — the poll costs one host-side `limactl list` plus a few
  small guest commands per running box, well under a second on two boxes. It is
  still a poll: pick an interval you are content to run forever.
- `status-style` — the chips are drawn for a dark bar. If your theme sets its
  own ground, drop that line and the colours will land on yours instead.

The block calls Torio by the path of the binary that printed it, not by name.
That is deliberate: an older `torio` earlier on your `PATH` has no `status`
subcommand, exits 2, and a bar renders that as an empty string with no error
anywhere you would look for one. Run `torio status setup tmux` again after
moving or reinstalling Torio.

## Without a multiplexer, the prompt

A bare terminal has no status bar, so the prompt is the only always-visible
surface you have:

```console
$ torio status setup zsh >> ~/.zshrc
$ exec zsh
```

```text
hermes —  │  claude-code NEEDS YOU
```

The catch that block solves is that a prompt is rendered synchronously: a
command in it stalls the shell for as long as it runs, and this one enters VMs.
So it does not run in the prompt. A `preexec` hook starts one detached refresh
before each foreground command, while `precmd` only reads the last completed
result from a private per-shell cache. An initial refresh starts when the block
is loaded. The prompt therefore never waits for a VM; after a very fast command
it may show the preceding completed refresh for one more prompt.

The block assigns `RPROMPT` directly and does not enable `PROMPT_SUBST`. A
status value containing `%` is escaped before assignment, so neither the cache
path nor the bounded status text becomes prompt syntax.

The line carries no colour of its own. A prompt counts the characters it is
given to decide where the cursor goes, so the `%F{...}` in the snippet is where
colour belongs; change it there.

## Reading a row

The bar is the glance. `torio status` is the same answer at full width:

```console
$ torio status
INSTANCE           BOX      BACKEND      SESSION  WAITING                    PROGRESS
torio              running  hermes       —        ?                          14s
torio-claude-code  running  claude-code  2        notification 7m pid 11673 +1  —
```

| Column | `—` means | `?` means |
|---|---|---|
| `SESSION` | this backend has no such thing as a session process | the box could not be read |
| `WAITING` | this backend answers no waiting question | it has no way to say, or its marker was too old, or not private, or unreadable |
| `PROGRESS` | this backend writes no evidence of work | nothing has been written yet, or the box could not be read |

The `—` is a backend reporting that a question does not apply to it, not a box
with nothing running. Keeping the two apart is the point of the glyph; a surface
that collapsed them into `0` would eventually read a box you cannot see into as
quiet.

A stopped box reports `0` sessions and `no` waiting — proven, without entering
it, because nothing runs in a VM that is not running — and `?` for progress,
because that evidence is inside it. `broken` and unrecognized VM states do not
prove the box is stopped, so their session and waiting fields remain `?`.

A line that says `torio: ?` is not a box. It is Torio saying the poll itself
failed, on a surface that would otherwise render the failure as an empty bar.

## Which sessions are they

The bar says *that* something wants you and adds a count when several sessions
do. `SESSION` counts every agent process on the box, the table names one wait
and adds `+N`, and the JSON carries every live waiting session:

```console
$ torio status --json | jq -r '.data.instances[]
    | select(.waiting.state=="known" and .waiting.waiting)
    | .session.sessions as $sessions
    | .waiting.waits[] as $wait
    | $sessions[] | select(.pid == $wait.pid)
    | "waiting session: \($wait.session_id), pid \(.pid), open for \(.age_seconds)s"'
waiting session: 3c0122, pid 11673, open for 683s
waiting session: bd98af, pid 11802, open for 97s
```

Matching each pid against the sessions adds how long that process has been open,
which is usually how you remember which window it is. Torio still reads one
fixed marker path per box, but that document has an entry keyed by Claude's
validated session id. Answering one session clears only its entry; another
session waiting on the same box stays visible.

`torio status` says nothing about whether one box is healthy. For that, ask the
box: `torio backend status` walks its bootstrap checks, and `torio serve status`
proves whether its guest service is actually answering.

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

## Writing your own

The two shipped lines are opinions, not the interface. The interface is the
document, specified in [`../contracts/status.md`](../contracts/status.md), and
anything that can read JSON can render its own:

```jq
# ~/.config/torio/waiting.jq — just the boxes that want you, nothing the rest of the time
[ .data.instances[] | select(.waiting.state == "known" and .waiting.waiting) | .instance ] | join(" ")
```

```console
$ torio status --json | jq -rf ~/.config/torio/waiting.jq
```

Read the contract before writing one, above all the part about `state`: a field
with `"state": "unknown"` still carries a `false` beside it, and a recipe that
reads the `false` will tell you an agent is fine when nobody could reach it.

Two things the shipped renderers do that a recipe of your own has to do for
itself. It has to call the right binary — `torio` by name is whatever your
`PATH` resolves, and an older one exits 2 into an empty line. And it has to
survive a poll that failed: `torio status` exits non-zero only when it cannot
list the boxes at all, and a recipe that prints nothing there is
indistinguishable from a recipe reporting a quiet host.

Nothing an agent wrote reaches any of this. Every value in the document is an
instance name, a backend name, an enumerated kind or a number, which is a
property of the schema rather than of any one recipe — see
[`../contracts/status.md`](../contracts/status.md).

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
