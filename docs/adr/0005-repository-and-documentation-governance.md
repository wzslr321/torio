# ADR-0005: Repository and documentation governance

- Status: Accepted
- Date: 2026-08-05
- Consolidates and amends: the rules that normative documents are corrected
  rather than archived, that exploration leaves the working tree, and that
  product documentation describes the delivered binary. The superseded originals
  are recoverable at `git show archive/pre-oss:docs/adr/…` (`0014`, `0016`,
  `0017`, `0020`).
- Applies to: `docs/`, `README.md`, `AGENTS.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `site/`, `spikes/`

## Context

Three rules governed this repository, and they were right for a private tree.

**Normative documents are corrected, not archived.** A contract that describes
something the code does not do is a defect, not a record. This rule was written
after `docs/contracts/cli.md` described `bootstrap` as a command that *may* place
`hermes` in the `docker` group — while the code does the opposite and actively
removes it. Docker group membership is root-equivalent. An implementer reading the
authority order literally could have "restored compliance with the contract" and
handed the agent identity root-equivalent privileges. Security cannot depend on
whether someone read the footnote.

**Exploration leaves the working tree.** Before the current product, the
repository carried a much broader design: staged roadmap, per-task worker
isolation, admission control, fresh sandboxed verification. None of it was
delivered, and 680 tracked files carried it. It was cut to a tag, because a
"superseded" banner does not scale to hundreds of files — the banner had already
failed to stop a contract being read as an instruction.

**Product documentation describes the delivered binary.** The README claimed
Torio never creates a VM while the binary created one; the site contradicted
itself inside a single build; the tutorial taught a manual clone-and-register
procedure that one command had replaced with stronger guarantees.

Two of those rules now block the repository being published.

The tree is Polish in the places a contributor must read — `AGENTS.md`,
`CONTRIBUTING.md`, `SECURITY.md`, `docs/contracts/`, `docs/03-architecture.md`,
and seventeen of twenty ADRs — and English in the places a user reads. Half a
migration reads worse than either end of it.

The decision record is also the most valuable thing here and the least
accessible: twenty ADRs, heavy overlap, several describing decisions that were
later narrowed or withdrawn, and one — the destination allowlist — that exists
only to say it is blocked. A reader deciding whether to trust a tool that
provisions a VM and a system identity has to read all of it in a language they
may not have.

Both problems collide with the rule that an accepted ADR is never rewritten and
that a new decision requires a new ADR superseding the old. Translating an ADR is
rewriting it. Merging six into one is rewriting all six. That rule exists to stop
a decision being changed silently, and it should not be worked around quietly —
it should be amended in the open, which is what this record does.

## Decision

**Everything a contributor or user reads is in English. The decision record is
consolidated. What is neither is carried by a tag.**

1. **English is the language of the repository.** Code, comments, documentation,
   commit messages, ADRs, contracts and CLI strings.

2. **The decision record is consolidated into five ADRs**, covering the control
   plane and trusted host inputs, the VM trust boundary, the ownership split and
   operator-carried write, MCP custody and egress, and this record. Consolidation
   is permitted to translate, merge and bring a decision up to date with what was
   actually delivered. It is **not** permitted to change a decision: where a
   decision was narrowed, withdrawn or blocked, the consolidated ADR says so and
   says why.

3. **The prior state is carried by the annotated tag `archive/pre-oss`**, at the
   `v0.2.0` tree. Recovering any file is `git show archive/pre-oss:<path>`;
   recovering the whole tree is `git checkout archive/pre-oss`. This is the same
   mechanism the earlier cut used with `archive/pre-v1`, and both tags remain
   valid.

4. **Delivery evidence leaves the working tree.** Roughly 330 files and 1.4 MB of
   run transcripts, spike results and internal plans are removed. They recorded
   gates that are closed and steered no open decision; six of them also carried
   the author's host username. Where a source comment cited one to justify an
   implementation choice, the citation is rewritten to tag form
   (`archive/pre-oss:docs/…`) — the address changes, the text does not.

5. **The rules that survive, unchanged in substance:**
   - A normative document that disagrees with delivered behaviour is a defect to
     fix, in the document or in the code, and never a state to accept.
   - Product surfaces — `README.md`, `docs/content/` and everything generated
     from it, and every operator-visible CLI string — describe the delivered
     binary. Internal milestone labels do not appear there.
   - A new decision still requires a new ADR. This ADR authorizes one
     consolidation of the existing record, not a habit of editing decisions.

6. **AI-Provenance headers are removed.** Fifty-eight files carried a header
   naming the model and harness that produced them, and thirty still named a
   harness that stopped being used in July 2026. Authorship is what version
   control is for, and a stale header in a security-adjacent tool invites a
   conversation about the wrong thing.

7. **The validation gate keeps the surface honest**, because prose rules did not:
   relative links resolve, documents cited from Go exist, no operator-facing
   surface carries a version label, no document hands the reader a pasteable
   credential, and no obvious secret material is committed.

## Consequences

- Anyone reading the repository for the first time reads five decisions instead
  of twenty, in one language.
- The Polish originals are one command away and are still the authoritative
  record of what was decided when. Where the consolidated text and the archived
  original disagree about a *decision*, the original is right and the
  consolidation is a defect to fix.
- The working tree drops from 598 tracked files to roughly 250.
- Anyone wanting the history pays one extra command. That cost is real and
  accepted.
- `CONTRIBUTING.md` can no longer route a contributor through an internal plan
  directory, because there is no longer one in the tree.

## Rejected

- **Publish with the documentation as it stands.** The contributor-facing surface
  would be in a language most readers of a public repository do not have, and
  `SECURITY.md` would describe an architecture — workers, task containers, a
  Docker socket — that this product does not have.
- **Translate but do not consolidate.** Cheaper and preserves granularity, but
  leaves twenty overlapping records including several that were narrowed or
  withdrawn, and one that exists only to announce a blockage. The value of the
  record is that it can be read.
- **Consolidate but keep the ADRs in Polish.** The decision record is the
  strongest argument this project has for being trusted; leaving it unreadable
  keeps that argument private.
- **Drop the ADRs entirely and write one summary.** Loses the rejected
  alternatives, which are the part that shows a decision was actually made rather
  than defaulted into.
- **Rewrite the archived originals in place.** Exactly what the no-silent-edit
  rule forbids, and the reason this ADR exists rather than a quiet pass over
  `docs/adr/`.
- **Keep the evidence directories and redact the username in place.** Rewriting
  evidence destroys the fidelity that made it evidence. Moving the whole set to a
  tag preserves it byte-for-byte and takes it off the published surface.
