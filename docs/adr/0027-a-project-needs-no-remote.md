# ADR-0027: A project needs no remote

- Status: Accepted
- Date: 2026-08-15
- Applies to: `internal/config`, `internal/projects`, `internal/cli`,
  `internal/tui`, `docs/contracts`

## Decision

**A recorded project may have no remote. The empty remote is the record of a
local project**: one that lives in the guest it was made in and is on no forge.
The registry shape does not change; the registry schema version does, from 1 to
2, so an older Torio refuses the document as newer than it knows rather than as
invalid.

**Three ways a project comes into being, each an explicit decision:**

- `project add <name> <remote>` — clone from the remote. Unchanged.
- `project add <name> --local` — an empty repository, initialized in the guest.
- `project add <name> --from-bundle <file>` — a Git bundle on the host, carried
  in over the one-shot transport and cloned from there.

**A deploy key belongs to a remote, so it is minted when a remote first
exists.** `project set-remote <id> <remote>` on a local project provisions the
key, attaches the origin, and fails closed carrying the public half exactly as
`add` does. Creating a project asks for no authorization at all.

**Agreement, for a local project, is an origin that is not there.** The
comparison is the one drift always made — what the record says the tree points
at — so a local checkout that has grown an origin is `origin_mismatch`, in the
vocabulary drift already had.

### Premises

- P1. A repository with no network remote is ordinary, not exotic: the content
  repository beside this one is local by intent, and had no way in at all.
- P2. A Git bundle is a single file that `git clone` reads as a source, leaving
  no remote configured (ADR-0025 P3), and `limactl copy` carries a file in one
  shot (P2 there).
- P3. The deploy key exists to read one remote (ADR-0018). Where there is no
  remote there is nothing for a key to authorize against.
- P4. The empty remote and a filesystem path are different claims: the first
  says there is nothing to reach, the second names something one machine can
  reach. Only the first is a project, so `validateRemote` keeps refusing paths.

## Walkthrough

Before: a repository that is not on a forge cannot be attached. Two independent
gates refuse it — the record needs a remote, and the remote must answer — so the
only route was to publish the repository to a forge in order to satisfy a
validator.

After: `torio project add notes --local` makes it, or `torio project add
marketing --from-bundle ~/marketing.bundle` carries it in. Sessions open in it,
the Brain retrieves from beside it, and `project show` reports it as ordinary.
If it ever gets a remote, `torio project set-remote` records it and provisions
the key then.

## Context

Attaching a private repository was narrowed to one authorization by ADR-0018,
and the operator feedback that opened this record is that the authorization is
in the wrong place for a project that has no forge behind it at all: creating a
project should not require a credential act, and for a local repository there is
no act to perform. Issue #36 had recorded the same gap from the other side, with
`git bundle` already identified as the transport that works right up to the
validator.

## Consequences

The registry can now hold a record whose remote is absent, and the duplicate
remote rule skips it: two local projects do not share a remote, they each have
none.

A local project lives where it was made. The registry is shared, so it is listed
on every backend, and opening it on a box that does not hold it refuses with
both ways forward named — carry it with a bundle, or give it a remote. This is
the honest answer rather than the convenient one: materializing needs something
to materialize from (ADR-0024), and inventing a source would mean guessing.

A bundle is a snapshot. What arrives is the history the operator bundled, and
refreshing means handing over a newer one; nothing tracks the host repository
afterwards, and nothing on the host is mounted.

The bundle path is the first host path this package reads. It is read once,
staged into a private directory, carried, and forgotten — no host path enters
the record, which keeps every record meaning the same thing on every machine
(ADR-0023).

`set-remote` will not remove a remote. A record may hold none, but not by
having one taken away centrally: other guests' checkouts still point at it, and
forgetting it here would strand them with nothing to say what they point at.

## Rejected

**Infer local from an absent remote.** No flag: `add <id>` with nothing else
would make an empty project. It also makes a mistyped id a new empty repository
rather than an error, and that same form already means "materialize the project
on record" — one input, two creations, chosen by whether a lookup happened to
miss.

**Record a filesystem path as the remote.** `git ls-remote /srv/git/repo` works,
so a path would satisfy both gates with no new shapes. It records something only
one machine can resolve, in a registry every machine reads, which is exactly the
failure ADR-0023 settled.

**Carry local projects between boxes through the host automatically.** The Brain
already does this (ADR-0025), and the machinery would transfer. It is deferred
rather than refused: a vault is one corpus with one owner, a project is a
working tree with branches, uncommitted work and merge conflicts an operator
must see. If moving local projects between boxes becomes ordinary, that is the
ADR to write.

**Mint the deploy key at creation.** Every project would then have a key ready
for the remote it might get. It provisions a credential for a remote that does
not exist, on the guess that one will, and it is the toll on creating a project
that this record exists to remove.
