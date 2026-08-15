# ADR-0024: Opening a session materializes an absent checkout, and nothing else

- Status: Accepted
- Date: 2026-08-15
- Applies to: `internal/projects`, `internal/cli`, `internal/tui`,
  `docs/contracts`

## Decision

**Opening a session on a registered project whose only drift is that no
checkout is there materializes it first, then opens.** This covers the hub's
project screen and `project agent`, `project enter` and `project shell`. The
remote comes from the record, so nothing is retyped and nothing new is
attached. The operator is told it is happening while it happens.

**Exactly one drift is answered this way.** `checkout_absent`, alone, and
nothing else: a checkout that exists and disagrees with the record is a working
tree, and cloning over one is the act Torio refuses everywhere else. The
materialization reaches a remote, so it still fails closed on an authorization
only a human can give, and the deploy key is what the failure carries.

### Premises

- P1. The registry is shared by every instance and the checkouts are not
  (ADR-0009), so a project attached under one backend is registered and absent
  under the next. That is the ordinary state of a project not yet opened here,
  not a fault.
- P2. `Add` for a registered entry with an absent checkout clones and rewrites
  nothing: the entry already equals what would be persisted, so the shared
  registry is untouched. Pinned by
  `TestAddMaterializesARegisteredProjectWithAbsentCheckout`.
- P3. A checkout's drift is reported as stable markers, so `checkout_absent`
  alone is distinguishable from every other state without reading prose.
- P4. The remote on record is the one address every guest uses (ADR-0023), so a
  materialization needs no input from the operator.

## Walkthrough

Before: press `b` in the hub, pick another backend, press enter on a project.
The session helper refuses because there is no checkout, and the hub reports
drift naming a command. The operator leaves the hub, runs `torio project add
<id>`, returns, and presses enter again.

After: the same keypress reports that there is no checkout on this guest yet,
makes it from the remote on record, and opens the session. A repository whose
key this guest has not been given stops with the key to authorize, which is
where it stopped before.

## Context

ADR-0021 gave the hub a rebind so that trying a repository under another agent
stopped being a setup task. It left the other half open, and said so: the
chooser "is only as useful as the project's presence on the other instance".
Presence was a command the operator had to know about, run outside the hub, and
follow with a repeat of what they originally pressed.

The command surface had the same gap for the same reason. `project agent` on a
project registered but not materialized here failed with a remedy rather than
performing it, though every input that remedy needs was already on record.

## Consequences

Enter can now clone, which is minutes of work behind one keypress. It says so
while it runs, and the hub holds its single-operation lock throughout, so
nothing else starts underneath it.

It reaches the network. A session key is no longer guaranteed to be local work,
and an operator on a metered or absent connection feels that on the first open
of a project rather than at a moment they chose.

The retry is exactly one. A second refusal is reported, never answered again, so
a remote that fails intermittently produces one clone attempt per keypress
rather than a loop.

This does not extend to registration. Attaching a project is still an explicit
act with an explicit remote, and ADR-0021's refusal to hide a mutation inside
navigation is untouched: nothing here happens during a rebind, only on a
keypress naming a project the operator selected.

## Rejected

**Ask first.** A confirmation before cloning would make the mutation visible.
It also puts a prompt in front of the answer to a question the operator already
asked by pressing enter on a project they chose, and the state it guards is one
where nothing can be lost: there is no checkout to overwrite. The progress line
and the single-operation lock give the visibility without the extra key.

**Materialize on rebind, for every project.** It would make the next enter
instant. It also clones repositories the operator never asked to open, on a
keypress that means "show me the other box", which is the mutation-inside-
navigation ADR-0021 rejected.

**Answer more than one drift.** Repairing an origin mismatch or a dirty tree
would cover more failures. Each of those is somebody's working tree, and the
only safe repair is one a human chooses; Torio does not reset, clean or repoint
a tree, and a session key is the worst possible place to start.
