## Watch several agents at once {#watch-several-agents}

Running more than one box, the question stops being "what is this agent doing"
and becomes "which of them needs me". `torio status` answers it in one row per
box:

```console
$ torio status
INSTANCE           BOX      BACKEND      SESSION  WAITING                    PROGRESS
torio              running  hermes       —        ?                          14s
torio-claude-code  running  claude-code  2        notification 7m pid 11673  —
```

Asking is still asking, though, and the answer is worth having without asking.
Put it on your status bar:

```console
$ torio status setup tmux >> ~/.tmux.conf
$ tmux source-file ~/.tmux.conf
```

The command prints the configuration to stdout and writes nothing; the redirect
above is your decision. On a terminal with no multiplexer, use
`torio status setup zsh >> ~/.zshrc` instead — the prompt is then the surface,
and the snippet keeps the poll out of it so your shell never waits on a VM.
Each shell writes to its own private temporary file and the prompt reads only a
completed refresh. A very short command can leave the previous refresh visible;
the next one catches up without ever placing a guest poll in prompt expansion.

One chip per box arrives, and exactly one state is loud: the box that wants you
inverts, so it is found without reading. A live agent gets a dot and its count,
a backend that keeps no session process gets how long ago it last did work, and
a stopped box is barely there.

The bar says *that* something wants you; `torio status` says *which* — `WAITING`
names the session's pid, and matching it against `SESSION` tells you which
window by how long it has been open. Both surfaces and a notifier recipe are in
[the ambient status runbook](https://github.com/wzslr321/torio/blob/main/docs/runbooks/ambient-status.md).
