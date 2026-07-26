# Legacy architecture (superseded — retained for history)

**Status: superseded / pre-V0 exploration. Retained for historical context only.
Not the current implementation plan and not an onboarding path.**

Before Torio V0, this repository held a much broader design exploration: a staged
roadmap (Spike → Demo A → Demo B → Hardening → Company-ready), a trusted
control plane with a project registry and admission control, per-task worker
isolation, fresh sandboxed verification, a review/evidence pipeline, and
autonomous-worker machinery. **None of that is what Torio V0 delivers.**

Torio V0 is deliberately narrow: a controlled Remote Second Brain V1 plus exactly
one hardcoded Code V0 workspace, both fully operator-controlled. See the current
canonical surface below.

## The current (active) surface

The only active operational documentation for Torio V0 is:

- [`../README.md`](../README.md) — the Torio V0 entrypoint;
- [`runbooks/remote-second-brain-v1.md`](runbooks/remote-second-brain-v1.md);
- [`runbooks/code-v0-REDACTED-PROJECT.md`](runbooks/code-v0-REDACTED-PROJECT.md).

Normative engineering rules remain in [`../AGENTS.md`](../AGENTS.md). Its
top-of-file **Torio V0 status block** is authoritative on precedence: the
platform-oriented obligations and invariants in the older `AGENTS.md` sections —
project registry / admission control, per-task isolation, fresh verifier,
approval / integration / push, and the worker / container / worktree invariants —
belong to the **same superseded exploration** described here and **must not be
implemented or treated as a next task**. Only the general engineering and safety
discipline (TDD, validation, redaction, narrow slices, evidence) remains in force
for Torio V0. Where those legacy sections conflict with `../README.md` or the
runbooks, the README and runbooks win — that divergence is expected and is not a
stop-the-work conflict.

## Retained legacy material (historical only)

The following roots describe the superseded exploration. They are kept for
history and are **not** current tasks. Do not treat their roadmap, plans, prompts,
or spike results as work to start.

- [`plans/`](plans/) — the old staged roadmap and Demo A / Demo B / future plans.
- The numbered design docs — historical product brief, scope, architecture,
  threat model, responsibilities, and supporting chapters:
  [`01-product-brief.md`](01-product-brief.md),
  [`02-scope.md`](02-scope.md),
  [`03-architecture.md`](03-architecture.md),
  [`04-threat-model.md`](04-threat-model.md),
  [`05-responsibilities.md`](05-responsibilities.md),
  [`06-glossary.md`](06-glossary.md),
  [`07-source-verification.md`](07-source-verification.md),
  [`08-worker-runtime.md`](08-worker-runtime.md),
  [`09-review-pipeline.md`](09-review-pipeline.md),
  [`10-observability.md`](10-observability.md),
  [`11-testing-strategy.md`](11-testing-strategy.md),
  [`12-project-layout.md`](12-project-layout.md),
  [`13-requirements-traceability.md`](13-requirements-traceability.md),
  [`14-local-development.md`](14-local-development.md).
- [`../prompts/`](../prompts/) — the LLM prompt set for the old staged workflow
  (spike, Demo A, Demo B, review, handoff templates).
- [`spike-results/`](spike-results/) — recorded spike evidence from the pre-V0
  runtime-contract investigation.
- [`../HANDOFF.md`](../HANDOFF.md) — a historical session handoff (Demo A / D1). It
  now carries an archival banner and is not a live next-task instruction.
- [`adr/`](adr/) and [`contracts/`](contracts/) — architecture decision records
  and interface contracts from the same exploration.

These files are intentionally left in place: git history is not a substitute for a
readable temporary archive. They may be reorganized or removed in a later,
dedicated change — not here.
