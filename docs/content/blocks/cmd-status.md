## Command surface — `torio status` {#status}

The one command that answers across boxes. Every other command addresses the
single instance the invocation selected; this one polls every box Torio owns.

| Command | What it does |
| --- | --- |
| `torio status` | Report each box Torio owns: whether it is running, which backend it was provisioned for, what that backend has running, whether anything there is waiting on a human, and when it last provably did work. Accepts `--json` and `--format`. |
| `torio status setup tmux` | Print the configuration that puts the one-line form on a tmux status bar. It prints; it writes no file. |
| `torio status setup zsh` | Print the configuration that puts the one-line form on a zsh prompt. It prints; it writes no file. The hub shows the same recipes on `t` on its dashboard. |

Every field is a proven value, `?` for a question that was asked and could not
be answered, or `—` for one that backend does not answer at all. Absence is
never rendered as a zero, because a surface that cannot distinguish "quiet" from
"unreadable" is one you stop looking at.

It exits **0 whenever the poll completes**. A box that could not be reached, a
config document that could not be read, a fact that could not be proven — each
costs one field and nothing else. Only failing to list the boxes at all is an
error, because then there is nothing to report on.

The poll covers the default box, every box whose name Torio derived from a
backend, and the box `TORIO_INSTANCE` names for this invocation. `--config` does
not redirect it: each box's backend is read from the document that box owns.

`--format tmux` and `--format prompt` collapse the same report onto one line for
a surface that is glanced at rather than read. Asking for a line and `--json` at
once is a usage error — the envelope is the machine contract and a line is a
rendering of it. A poll that could not complete prints `torio: ?` on the line
and still exits non-zero, because something refreshing a bar on a timer shows
whatever arrives, and an empty line there reads as a quiet host.

`setup` prints and nothing else, and no flag will make it write. A dotfile
belongs to the operator; this is the same line `torio vm bootstrap` holds about
a managed file it did not install. The snippet calls the binary by the path of
the executable that printed it rather than by name, because an older `torio`
earlier on `PATH` exits 2 and every such surface renders that as an empty line.

`torio status` does not say whether one box is healthy. `torio backend status`
walks a box's bootstrap checks, and `torio serve status` proves whether its
guest service is answering.
