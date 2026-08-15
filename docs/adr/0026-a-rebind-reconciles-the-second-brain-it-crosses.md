# ADR-0026: A rebind reconciles the Second Brain on both sides of the move

- Status: Accepted
- Date: 2026-08-15
- Applies to: `internal/tui`, `docs/contracts`

## Decision

**Rebinding the hub syncs the Brain of the box being left, then the Brain of
the box arrived at.** The box being left is synced before the binding moves,
because it is the side that holds work nothing else has yet. Each sync is the
ordinary reconciliation of ADR-0025, bounded like any long operation, and the
hub's note says what each side did, in counts.

**Neither sync may fail the rebind.** The operator asked to move, and the
Brain not moving with them is something to say, not a reason to hold them on
the box they were leaving. A sync that cannot run — a stopped box, an
uninitialized vault — becomes the note's content; a merge conflict is reported
with the host vault path where it is resolved. The rebind itself fails only
for the reasons it always did.

### Premises

- P1. Two boxes agree only when both have synced (ADR-0025), and a rebind is
  the one moment the hub knows the operator's attention is crossing from one
  box to another. Notes written on the old box are exactly what the operator
  is about to ask the new box about.
- P2. Sync is idempotent and content-blind: a vault with nothing to carry
  reports zero commits moved, and no vault content reaches the screen, so
  running it on every rebind can repeat but not corrupt or leak.
- P3. Sync mutates only state Torio owns on the operator's behalf — the two
  vaults and the transported bundle — and requires no authorization a human
  must give. Nothing operator-owned is touched, which is what separates it
  from the registration ADR-0021 refused to run here.
- P4. A failed direction leaves that direction exactly as it was (ADR-0025),
  so a sync attempted against a box that cannot answer changes nothing.

## Walkthrough

Before: write notes in a hermes session, press `b`, pick codex, and ask the
codex agent about them. It finds nothing, because syncing was two keypresses
the operator had to remember on two different tabs, one of them before
leaving a box they had already left.

After: the same `b` reports "rebound to codex · brain on hermes: 2 to the
host, 0 back · brain on codex: 2 back". The notes are there. If the hermes
box was already stopped, the note says its Brain was not reconciled and why,
and the rebind lands on codex regardless.

## Context

ADR-0025 made the three vaults one Brain, reconciled through the host, and
left sync as an explicit key. That was deliberate at the time: ADR-0021 had
rejected hiding a mutation inside navigation, and the branch that introduced
sync held every new mutation to that rule.

Operator feedback drew the line differently: what ADR-0021 rejected was a
*hidden* mutation — an auto-registration that would attach repositories and
mint deploy keys behind a keypress that means "show me the other box". A sync
carries the operator's own notes between the operator's own replicas, asks
for nothing, and is reported on screen when it happens. Hiding was the
objection, and this is not hidden.

## Consequences

A rebind now takes as long as two syncs on top of what it took, and reaches
both guests' filesystems on a keypress that used to be pure navigation. The
busy line says the Brain is being reconciled on the way, so the added wait is
named while it is felt.

The note is the feedback and the whole feedback. There is no confirmation
before and no acknowledgement after, so an operator who rebinds and looks
away can miss a "not reconciled". The Brain tab still reports how far a
replica stands from the host vault, which is where a missed note is caught.

`y` on the Brain tab keeps its meaning. A rebind syncs two boxes at one
moment; an operator who wrote notes and wants them carried *now*, without
moving, still has the explicit key.

ADR-0024's refusal to materialize checkouts on rebind is untouched. Cloning
repositories the operator never asked to open fails P3 — a clone reaches a
forge and can require an authorization — and stays where it was: behind a
keypress naming a project.

## Rejected

**Ask before syncing.** A confirmation would interrupt every rebind to guard
an operation that is idempotent, content-blind, and touches nothing the
operator did not already entrust to Torio. The note gives the visibility
without the toll.

**Sync only the side being left.** Half the carries, and the new box stays
stale until its own next sync — which makes the rebind note say "carried out"
while the box on screen still cannot answer for the notes, the exact gap this
record closes.

**A background daemon.** Continuous agreement, and no daemon lifecycle
exists in Torio to hang it on: every action is an operator's invocation that
ends (ADR-0025 kept sync an operator action, and this record does too — a
rebind is one).
