# ADR-0016: An agent session may ask to push, and every signature stops at the operator

- Status: Accepted
- Date: 2026-08-09
- Supersedes: "Push stays human" in
  [ADR-0009](0009-backend-contract-and-claude-code.md), which deferred this
  design; and invariant 11 as written in `AGENTS.md`.
- Builds on: [ADR-0015](0015-mediated-agent-forwarding.md), without which this
  is the design ADR-0009 declined.
- Applies to: `internal/lima`, `internal/cli`, `internal/backend`,
  `internal/backend/claudecode`

## Context

ADR-0009 declined a session-scoped push grant and said why: it was "a per-session
grant handing the forwarded socket to the agent identity under an explicit flag."
Handing over a socket is handing over a key. The flag was the whole control, and
a flag is not a control.

That is no longer the only available shape. ADR-0015 put Torio's own agent in
front of the operator's: one pinned key, and every signature waiting on a dialog
on the operator's Mac before it is made, with the decision recorded first. What a
session reaches through that socket is not a key. It is the ability to ask.

Meanwhile the cost of the split is paid every day and by a human. The agent works
in the checkout; the operator opens a second terminal, runs
`torio project shell`, reviews, pushes, exits. `torio` is a host binary and the
agent is inside the VM, so the agent cannot invoke it: the terminal switch is
structural, not friction that better output could remove.

## Decision

**`torio project agent <id> --push-grant` opens the agent session with the
mediated agent reachable, and refuses without a pinned key.**

The transport still forwards no SSH agent: `ForwardAgent` stays off, `-a` stays
on, multiplexing stays disabled. Nothing about the operator's keyring reaches the
session and it still cannot inherit a connection that could. What it gains is one
remote-forwarded Unix socket whose far end is the ADR-0015 proxy.

- **No pin, no grant.** `--push-grant` without `operator_key` is a precondition
  failure, not a weaker grant. Without the proxy this is the design ADR-0009
  rejected, and it must not be reachable by leaving a config field unset.
- **A second guest helper**, `torio-agent-push-session`. The ordinary helper is
  provably free of `SSH_AUTH_SOCK` — a test forbids the string — and a session
  opened through it can reach no remote at all. Widening that file would have
  spent the guarantee for every session to add the capability to some.
- **The socket is handed over deliberately, and no wider.** sshd creates the
  forwarded socket owned by the operator and unreadable by anyone else. The
  helper, running as that operator, `chgrp`s it to the shared group both
  identities already belong to and sets `0770`. That is why no sshd setting is
  loosened: `StreamLocalBindMask` would have relaxed the mode of every Unix
  socket the machine ever forwards, to fix the one.
- **The path is unguessable and single-use**: `/tmp/torio-push-<32 hex>.sock`,
  minted per session on the host, validated against the identical pattern on
  both sides. sshd refuses to bind over an existing file and the transport sets
  `ExitOnForwardFailure`, so a squatted path fails the session instead of
  silently handing the agent somebody else's socket. The helper additionally
  proves the path is not a symlink, is a socket, and is owned by this session.
- **The variable crosses `sudo` as an argument, not a grant.** `env
  SSH_AUTH_SOCK=… ` run as the agent identity, never `--preserve-env`, which
  would need a sudoers entry: buying an environment variable with standing
  authority is the wrong trade.

## What changes, stated plainly

Invariant 11 said push was human-only and outside the CLI. It is now: **a push
may be initiated inside the CLI, and every signature it needs is human-only.** The
human act moved from typing `git push` to approving the signature that push
requires. Nothing signs without a person, which is what invariant 11 was
protecting; where the command is typed was the proxy for that, not the thing
itself.

Invariant 8 is unchanged in substance and narrower in fact: write capability
still reaches the guest only for the length of a session an operator opened, and
still ends with it. The grant is not durable, is not a credential, and cannot be
stolen from the guest — there is nothing there to steal but a socket that answers
only after a dialog.

`SECURITY.md`'s "automatic merge, push or release" stays unacceptable and stays
prevented. This is not automatic. A grant with no one at the Mac denies every
signature by timeout.

## Consequences

- The backend contract gains `PushHelperPath` and `PushHelper`. A backend that
  declares neither offers no such session, and `--push-grant` against it is a
  precondition failure rather than a silently ordinary session.
- Bootstrap proves both helpers, root-owned `0755`, installing from embedded
  bytes when absent and reporting — never repairing — drift. The shared body is
  the same one that already proved the first.
- The grant is per invocation. There is no config field that turns it on, and no
  memory of the last time it was used: an operator asks for it each session, by
  typing it.
- What the agent can do with the grant is bounded by what the operator approves,
  and by nothing else. A confused or injected agent can put a dialog on the
  operator's screen, repeatedly. That is a nuisance channel and is named here as
  one; the answer to a dialog nobody expected is Deny, and the log will say what
  asked.
- Torio still makes no claim about what was pushed. The decision log says what
  was allowed to be signed, which is a different and smaller statement.

## Rejected alternatives

- **`--push-grant` implying a key when exactly one is loaded.** Convenient, and
  it is what ADR-0015 already does for the pin. Here it would mean the grant
  works without the operator ever having chosen a key, which is the property that
  distinguishes this from the design ADR-0009 rejected.
- **A `push_grant: true` config field.** It makes the grant standing, which turns
  a per-session act into a property of the installation — durable capability with
  extra steps.
- **Loosening sshd's `StreamLocalBindMask`.** One global setting affecting every
  forwarded socket, forever, to avoid two lines in a helper that already runs as
  the socket's owner.
- **Letting the agent run `torio`.** It would need the host binary, the host
  config and a route back out of the VM. The VM edge is the boundary; a hole
  through it for convenience is not a smaller version of this decision.
