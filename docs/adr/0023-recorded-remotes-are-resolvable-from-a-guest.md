# ADR-0023: The remote on record names a host a guest can resolve, and a wrong one is corrected in place

- Status: Accepted
- Date: 2026-08-15
- Applies to: `internal/projects`, `internal/config`, `internal/cli`,
  `internal/tui`, `docs/contracts`

## Decision

**A recorded remote is a public address, not a name one machine happens to
know.** Torio does not predict which names qualify. It asks the guest, once, on
the run that needs the remote, and reports what the guest answered: a host the
guest cannot resolve is named as that, with the host it could not resolve,
and is no longer reported as a missing authorization.

**A record is corrected in place.** `torio project set-remote <id> <remote>`
rewrites the remote of a registered project and repoints a matching checkout,
without removing the entry, the checkouts on other guests, or their deploy
keys. The hub offers the same correction on the project screen. Removing and
re-adding stays a way to change a remote; it is no longer the only one.

### Premises

- P1. A guest resolves a host through DNS. It does not read the operator's
  `~/.ssh/config`, and Torio never copies one in: the boundary is the edge of
  the VM (AGENTS.md invariant 2).
- P2. An SSH alias that a host stanza defines resolves on the machine holding
  that stanza and nowhere else. Verified: `getent hosts gh-lean-triage` fails
  in a Torio guest while `github.com` resolves.
- P3. `git ls-remote` against an unresolvable host fails with
  `ssh: Could not resolve hostname <host>`, which is distinguishable from every
  authorization failure. Verified in a `codex` guest, 2026-08-15.
- P4. The project registry is shared by every instance (ADR-0009), so a record
  only one machine can read is a record the other backends cannot materialize.
- P5. A remote is not a secret. It is already printed by `project list`,
  `project show` and the hub, so naming it in an error adds no exposure.

## Walkthrough

Before: a project whose record holds `git@gh-lean-triage:leancodepl/…` attaches
on the machine whose SSH configuration defines `gh-lean-triage`. Materializing
it in a second backend's guest fails, and the failure says the guest cannot read
the remote yet and offers a deploy key to authorize. Authorizing that key
changes nothing, because the guest never reached the forge. Correcting the
record means `project remove` and `project add`, which drops the entry and then
stops on the checkout the first guest still holds.

After: the same run reports that the guest cannot resolve `gh-lean-triage`, and
names `torio project set-remote lean-triage <remote>`. The operator runs it, or
presses `e` on the project in the hub, and types the address the forge actually
answers to. The entry, every checkout and every deploy key stay where they are.

## Context

The registry moved to the config root so that a project is attached once and
listed by every backend (ADR-0009). What each backend still needs for itself is
a checkout, and `project add <id>` is how it gets one. That step reaches the
remote from inside a guest, which is the first time anything asks whether the
recorded address means anything there.

Nothing had ever asked. `add` validated a remote's shape, not its reach, so a
host-local alias entered the record and stayed. Issue #32 collected what that
cost: one recorded remote cannot serve two backends, and the same repository
ends up attached twice under two ids so that each has a remote that works,
after which `project list` no longer answers what was attached.

## Consequences

The refusal is now the guest's answer rather than a rule about names, so a host
Torio has no opinion about is never refused for looking wrong. The cost is that
it arrives one guest round trip later than a shape check would, and only on a
run that reaches the remote.

`set-remote` is a mutation of the shared registry, which every instance reads.
It repoints a checkout only where the origin still matches the record it is
replacing; anything else it reports and leaves alone, because Torio does not
repoint a working tree it cannot vouch for.

Deploy keys are per project and per guest (ADR-0018). Correcting a remote does
not move them, and a guest that has never read the corrected host still has to
have its own key authorized once.

This decision does not settle where a remote comes from at `add` time. Resolving
an operator's own SSH aliases into canonical form before they enter the record
would stop the record going wrong in the first place, and is left to its own
change; the invariant it would serve is the one stated here.

## Rejected

**Refuse a remote whose host has no dot.** It is one line and it would have
caught every case in the registry that prompted this. It also refuses a host
that a private network resolves and Torio knows nothing about, and it teaches
the operator a rule about spelling rather than the fact that the guest is the
one that has to reach the address. A guest answer is available and is the truth;
a heuristic that agrees with it most of the time is not worth preferring.

**Record a remote per backend.** Issue #32 lists it. It makes the registry
answer "what did I attach" with a table rather than a name, and it invites the
two entries to drift into two different repositories under one id. The address
of a repository does not depend on who is reading it; the transport does, and
transport is already derived where it is used (ADR-0018).

**Amend by removing and re-adding.** Already possible, and already the thing
operators do. It drops the entry before anything replaces it, so an interrupted
correction loses the record; it stops on checkouts other guests hold; and it
discards the deploy keys those guests had authorized.
