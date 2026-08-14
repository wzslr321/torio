# ADR-0020: An ADR is decision-first, names its premises, and fits a page

- Status: Accepted
- Date: 2026-08-11
- Applies to: `docs/adr/` (records after this one), `docs/adr/TEMPLATE.md`, `docs/adr/README.md`

## Decision

**A new ADR opens with its decision, lists the premises the decision rests on,
walks through what the operator does when the decision touches an operator
surface, and fits in 120 lines. The decision statement is at most ten lines and
a reader may stop after it.** Context, consequences and rejected alternatives
follow the decision instead of preceding it. What does not fit in 120 lines is
cut, not appended.

### Premises

- P1. A decision's reasons go stale silently: nothing in the current format
  marks an ADR whose premise a later ADR has made false.
- P2. Long records are skimmed, not read; a constraint buried past the fold is
  found by archaeology, not by reading.
- P3. The records reason about invariants and never about the operator's hands,
  which is where the last two surface defects lived.

### Rules

1. **Premises are numbered, one line each, falsifiable.** "The Claude Code
   guest can hold no read credential" is a premise. "Security matters" is not.
2. **An ADR that makes an earlier premise false says so in its header:**
   `Invalidates: ADR-NNNN P2`. The earlier record gains the pointer
   `P2 invalidated by: ADR-NNNN` in its header block. The pointer follows the
   existing `Superseded in part by:` mechanics: a header line, never an edit to
   the body. An earlier ADR that predates numbered premises gets the pointer
   with the invalidated claim quoted in parentheses.
3. **A decision that changes an operator surface carries a walkthrough**: what
   the operator does, in order, at the keyboard, before and after the change.
   A decision that does not survive its own walkthrough is not accepted.
4. **The cap is 120 lines and the decision statement is at most ten.** The cap
   is enforced by review; the validation gate does not check it yet.
5. **Accepted records stay immutable.** Nothing here rewrites ADR-0001 through
   ADR-0019; they are exempt from the format and subject to the pointers.

## Walkthrough

1. A reader opens a record, reads the decision statement, and has the decision
   in under a minute. Premises tell them what it depends on.
2. A reader who suspects the world has moved checks the header. A pointer names
   the record that moved it; no pointer means no accepted record disagrees.
3. An author starts from `TEMPLATE.md`. The premises section forces the load-
   bearing beliefs into the open; the walkthrough forces the decision to meet
   the operator; the cap forces cutting over appending.
4. A reviewer checks the new record's premises against the accepted set and
   asks for `Invalidates:` lines where they collide.

## Context

The record corpus did its core job: it preserved why each decision was made.
What it did not do is age. ADR-0019 fixed the hub to one instance and backend
in a clause its own document states in one sentence, 143 lines in. ADR-0018
gave every guest identity a way to hold a read credential, which falsified the
belief that a Claude Code guest cannot read a private remote; the issues and
records reasoning from that belief were not touched and still describe the old
world. Both facts were rediscovered in conversation, not by reading. At the
same time no record ever had to defend a decision against the operator's
sequence of actions, and the two most recent surface defects (a deploy key the
TUI never shows; an Enter key that opens nothing on the default backend) are
exactly the kind a walkthrough exposes.

## Consequences

- Writing gets harder and reading gets cheaper. Distilling premises is real
  work that the author now pays once so every reader stops paying it.
- 120 lines will sometimes hurt. Nuance that does not fit goes to the commit
  message or the archive tags, which is where nuance that steers no open
  decision already lives under ADR-0005.
- The premise pointers only work if reviewers check collisions; rule 2 has no
  mechanical enforcement today. If that proves unreliable, teaching the
  validation gate to cross-check `Invalidates:` lines is the follow-up.
- The known stale case can now be recorded: a follow-up may add pointers for
  what ADR-0018 changed underneath earlier remote-access reasoning.

## Rejected

**Backfilling premises into ADR-0001 through 0019.** Editing accepted records
breaks the one rule that makes them trustworthy: the record of what was
believed then is the thing being kept.

**A standalone premise registry.** A second document listing every premise
would drift from the records the moment either changed, which is the exact
failure ADR-0019 avoided by refusing a second implementation of operations.

**No cap, style review only.** Style review produced the current corpus.
The cap is the only rule in this record that cannot be argued around.

**Replacing ADRs with issues.** Issues have no acceptance state, live outside
the tree the code cites, and are already where stale premises went unnoticed.
