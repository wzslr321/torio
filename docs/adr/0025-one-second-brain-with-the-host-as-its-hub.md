# ADR-0025: There is one Second Brain, and the host holds the copy the boxes agree through

- Status: Accepted
- Date: 2026-08-15
- Applies to: `internal/brain`, `internal/lima`, `internal/cli`, `internal/tui`,
  `docs/contracts`
- Supersedes in part: ADR-0003 (the canonical vault location, and the refusal to
  carry Brain data out of a guest)

## Decision

**One Second Brain, held on the host, replicated into every backend's guest.**
The canonical vault is a Git worktree at
`${XDG_DATA_HOME:-~/.local/share}/torio/brain/vault`, private to the operator.
Each guest keeps the vault it already keeps, at its identity's own
`BrainPath`, and that copy is now a replica.

**`torio brain sync` reconciles the bound guest with the hub, both ways**, by
carrying Git bundles over `limactl copy`. Transport stays one-shot and bounded,
and no vault content reaches stdout, logs or evidence. **No vault holds a
network remote**, host or guest: a bundle is a file passed to `git fetch`, never
a configured remote, so the existing drift rule stands unchanged. A merge that
conflicts stops that direction, reports counts and the hub path, and is resolved
by the operator with ordinary Git in the host vault.

### Premises

- P1. Guests are separate Lima instances with no shared mount (ADR-0002) and no
  network remote on a vault, so two vaults have no way to agree without
  something outside both carrying bytes between them.
- P2. `limactl copy` moves files in both directions, one shot per invocation.
- P3. A Git bundle is a single file that `git fetch` and `git clone` read as a
  source. Reading one configures no remote and leaves none behind.
- P4. Each guest vault is a Git repository already, scaffolded by `brain init`,
  so the histories exist and only need to be joined.
- P5. Two vaults scaffolded independently have unrelated histories, so the first
  join in either direction is a merge of unrelated roots and every later one is
  not.

## Walkthrough

Before: a note written on the hermes box is on the hermes box. Opening a codex
session and asking its retrieval skill about it finds nothing, because the skill
reads `/home/codex/brain`, which is a different vault that has never seen the
note. The vault comes back out of a guest only through a `limactl copy` the
operator composes.

After: `torio brain sync`, or `y` on the hub's Brain tab, on each box. The note
travels through the host vault and is in both. `brain status` says how far the
bound replica is ahead of or behind the hub.

## Context

ADR-0003 put the vault on the guest's native filesystem and kept it there,
against host mounts and against cloud sync. That decision was made when there
was one box. ADR-0009 added a second backend and a third followed, each with its
own identity, its own home, and therefore its own vault. Nothing said whether
those were one Brain or three, and in practice they were three: the retrieval
skill in each box tells the agent it is reading "the same vault in every project
and every session", which is true inside one box and false across them.

ADR-0003 also removed `brain export` and named the condition for its return: "if
a full Brain backup ever becomes a product requirement it deserves its own ADR".
This is that record.

## Consequences

The host now holds Brain data. `internal/config`'s claim that Torio writes no
runtime state on the host stops being true, and the vault is created 0700 and
kept out of the config root so that an operator can find it, back it up and read
it with ordinary tools.

Sync is an operator action, not a daemon. Two boxes agree when both have synced,
so a note written on one box and read on another before a sync is not there.
Saying that plainly is why `brain status` reports how far the replica is from
the hub rather than only whether it is healthy.

A conflict stops one direction and leaves the other alone. It is resolved in the
host vault with Git, which is a tool the operator already has and Torio does not
wrap: the vault is Markdown files in a Git repository, and that is the whole
reason this format was chosen (ADR-0003).

This commits future work to keeping the two shapes joinable. A change to what a
vault contains has to make sense on both sides of a merge, because from here a
vault is something two machines edit.

## Rejected

**A private Git remote.** Every guest pushes and pulls one repository on a
forge, and the boxes agree without the host being involved. It also hands every
guest a credential for the operator's private notes, sends the vault to a third
party, and reverses the rule that a remote on a vault is drift. The trust
boundary ADR-0004 draws around credentials would have to be redrawn to hold the
one credential that reaches everything the operator has ever written down.

**A shared host mount.** One directory, mounted into every box, and there is
nothing to sync. It is the mount ADR-0002 forbids, and the reason stands: a
mount makes the host filesystem reachable from inside a guest, which is the
boundary the whole product is built on.

**Hermes as the owner, the others as read-only replicas.** Fewer merges, and one
place where writes happen. It also means an agent working in a codex session
cannot write down what it learned, which is most of what a Second Brain is for.
