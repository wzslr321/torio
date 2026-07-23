# Etap 2 — Demo B: Safe Coding Worker

## Prerequisites

- Demo A PASS.
- Spike daje `Demo B native Docker: GO`.
- Jeden trusted test project.
- Jeden prebuilt Linux/arm64 image pinned by digest.
- Brak realnych push credentials do czasu ostatniego acceptance slice.

## Vertical slice order

Każdy slice kończy się działającym testem. Nie twórz wszystkich interfaces/DB tables z góry.

## B1 — Project registry

- parse/validate schema,
- trusted XDG storage,
- canonical repo path,
- image digest requirement,
- no unknown fields/secrets,
- immutable registry snapshot hash.

Negative tests: task-branch config, tag-only image, path outside roots, forbidden network.

## B2 — Admission and task binding

- parse task request,
- reject capability fields/oversize input,
- resolve effective policy,
- canonical JSON + SHA-256,
- create binding to Hermes board/task przez zweryfikowany adapter,
- idempotency key.

`submit` nie uruchamia execution przed zakończeniem policy/workspace preparation.

## B3 — Evidence ledger minimum

Dodaj wyłącznie tables/events potrzebne B1–B2. SQLite migration, WAL, FK, transaction + event. Nie implementuj pełnej FSM.

Tests: crash/rollback, duplicate idempotency, concurrent admission.

## B4 — Workspace/Git adapter

- verify clean trusted base,
- create exact task workspace,
- deny/mask usable Git metadata,
- capture file modes/tracked/untracked/deleted,
- reject escaping symlink/special/nested repo,
- cleanup refuses dirty/unknown workspace.

Tests muszą używać realnego temporary Git repo.

## B5 — Fresh task executor

- generate execution spec from effective policy,
- unique labels/handle per execution,
- native Docker backend integration zgodna ze spike'em,
- persistence off,
- image digest, network none, limits,
- exact mount,
- stop/destroy/inspect/reconcile.

Negative tests: socket, sibling repo, host home, previous task canary, background process, network.

## B6 — Minimal worker profile/tool policy

- separate worker profile data,
- allow sandbox-routed terminal/file + Kanban worker operations,
- deny web/browser/messaging/cloud/MCP/memory/admin,
- inspect skill required env/credential files,
- fail closed on implicit passthrough.

Testuj effective behavior, nie tylko generated config.

## B7 — Candidate freeze

- wait for confirmed stop,
- revoke write,
- trusted Git capture,
- create commit/tree + retention ref,
- canonical diff hash,
- persist candidate/event atomically,
- changing workspace after stop is impossible or detected.

## B8 — Fresh verifier

- exact candidate snapshot,
- pinned image,
- trusted argv commands,
- no shell interpolation,
- separate fresh sandbox,
- log limits/redaction/hashes,
- failed command blocks review-ready.

Negative test: candidate test attempts host read/network/Docker access.

## B9 — Review and approval

- render machine/human review bundle,
- exact artifact tuple,
- protected admin capability,
- approve/revoke idempotency,
- candidate/policy/evidence mutation invalidates approval.

Brain/worker identity must get exit 7 for admin operations.

## B10 — Fast-forward integration

- acquire task/project lock,
- revalidate all tuple fields,
- check current target equals base,
- atomic fast-forward target → review commit,
- record before/after,
- idempotent exact-repeat.

Negative tests: changed base, changed ref, revoked approval, failed verification, concurrent integration.

## B11 — Explicit push

- separate admin command,
- exact integrated commit/ref,
- fast-forward remote semantics,
- credentials outside worker/brain,
- no implicit push from integrate,
- redacted Git output.

Start with local bare remote in tests; real remote acceptance only after security review.

## B12 — Reconciliation

Test kill/restart matrix z planu spike'a. Orphans są klasyfikowane; ambiguous resources nie są automatycznie usuwane.

## B13 — End-to-end adversarial acceptance

Jedno harmless task request przechodzi cały flow. Następnie negatywne scenariusze TM-02–TM-13 z threat modelu.

Demo B PASS wymaga:

- real task/run/container/evidence IDs,
- fresh worker i verifier,
- approval invalidation proof,
- stale-base proof,
- no secret/log leak,
- integration exact OID,
- push tylko po osobnej human operation.
