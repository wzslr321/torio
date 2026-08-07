---
applyTo: "docs/adr/**"
---

# Architecture decision records

An accepted ADR is an immutable record. The strongest finding available here is
that a diff edits one.

## Flag without exception

- A change to the Context, Decision, Consequences or Rejected text of an ADR
  whose status is Accepted. A changed decision needs a new ADR that supersedes
  the old one, not a rewrite of the old rationale.
- A `Status:` flipped in place from Accepted to anything else.

Two edits to an accepted record are legitimate, and both are pointers rather
than rewrites: a `Supersedes:` line on the new record, and a `Superseded in part
by:` line on the old one naming the later ADR and the exact part it replaces.
Anything beyond a header line is a rewrite.

## A new ADR

- Header block: `Status`, `Date`, `Applies to`, and where relevant `Supersedes`.
- Sections: `## Context`, `## Decision`, `## Consequences`, `## Rejected`. All
  four are required. The rejected alternatives are how a reader tells a decision
  from a default, so an empty or one-line Rejected section is a finding.
- The index table in `docs/adr/README.md` gains a row in the same pull request,
  and the prose count above the table stays true.
- A change to a security boundary also updates `docs/03-architecture.md` and the
  invariants in `AGENTS.md`. A boundary ADR that lands alone is incomplete.
