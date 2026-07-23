# State and ledger contract

## Nie jest task engine

Hermes Kanban pozostaje właścicielem queue status, claims, retries, heartbeat i run history. `hb.db` przechowuje security/evidence facts.

## Lokalizacja

```text
~/.local/share/hermes-box/hb.db
~/.local/state/hermes-box/logs/
~/.local/share/hermes-box/artifacts/
```

DB pracuje w WAL mode, ma foreign keys, migrations i transakcje. Każda mutująca operacja ma event zapisany w tej samej transakcji co zmiana rekordu domenowego.

## Minimalne encje

- `projects` — snapshot identity registry entry.
- `task_bindings` — `hb_task_id ↔ kanban_board/task_id`.
- `policy_snapshots` — canonical JSON + SHA-256.
- `executions` — jedna worker attempt i resource lease.
- `candidates` — base/review/tree/diff OIDs/hashes.
- `verifications` — exact candidate + verifier image + outcomes.
- `approvals` — actor, artifact tuple, decision/revocation.
- `integrations` — target before/after.
- `pushes` — remote/ref/exact commit.
- `resource_leases` — container/workspace identifiers i cleanup status.
- `events` — append-only audit facts.

## Derived status

Status nie jest niezależną manualnie edytowaną FSM. Jest wyliczany:

```text
ADMITTED       task binding + policy snapshot
RUNNING        active execution lease
CANDIDATE      frozen candidate exists
VERIFY_FAILED  latest verification failed
REVIEW_READY   verification passed, no valid approval
APPROVED       valid approval matches artifact tuple
STALE          base/policy/candidate/evidence no longer matches
INTEGRATED     target moved from base to review commit
PUSHED         remote points to integrated commit
DISCARDED      explicit tombstone, audit retained
FAILED         terminal infrastructure/policy failure
```

## Concurrency

- Jedno aktywne execution per `hb_task_id`.
- Jedna operacja integration/push per task lock.
- Mutacje używają optimistic preconditions i transaction locks.
- Process lock file nie zastępuje DB constraints.
- Nie przechowuj sekretów w DB.

## Reconciliation

Po restarcie `hb reconcile` porównuje:

- ledger leases,
- Docker labels/state,
- workspace existence,
- Git refs/OIDs,
- Hermes Kanban task/run state,
- service state.

Niejednoznaczność zwraca exit 9 i wymaga repair planu. Reconcile nie usuwa dirty worktree automatycznie.
