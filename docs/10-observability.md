# Observability and redaction

## Stable identifiers

Każdy log/event ma, jeśli dotyczy:

```text
project_id
hb_task_id
kanban_board
kanban_task_id
kanban_run_id
execution_id
policy_sha256
candidate_oid
resource_handle
```

## Logs

- Structured JSON opcjonalnie, human stderr domyślnie.
- Credentials i token-like values są redacted przed zapisem.
- Raw env nigdy nie jest logowany.
- Command argv jest logowane po redakcji.
- Candidate stdout/stderr ma size limit i artifact pointer.
- Hash liczony jest z redacted stored artifact; jeśli potrzebny raw hash, przechowuj go bez raw content i opisz semantykę.

## Audit events

Events są append-only. Nie usuwaj approval/revocation/integration history przy cleanup taska.

## Doctor vs reconcile

- `doctor` obserwuje i raportuje.
- `reconcile --dry-run` klasyfikuje różnice.
- repair/mutation wymaga osobnej jawnej operacji.

## Metrics PoC

Nie buduj dashboardu. Wystarczą:

- operation durations,
- verification outcomes,
- orphan/resource mismatch count,
- active execution leases,
- stale candidate count.
