# ADR-0008: SQLite jako evidence ledger, nie druga FSM

- Status: Accepted
- Date: 2026-07-23

## Context

Policy snapshots, approvals i integration evidence muszą przetrwać restart poza task worktree. Jednocześnie Hermes posiada trwały Kanban i retry state.

## Decision

`hb.db` zapisuje append-only events oraz tabele policy/evidence/resources. Wyświetlany status jest wyliczany z faktów. Queue lifecycle i worker retries pozostają w Hermes Kanban.

## Consequences

- DB ma migrations, WAL, FK i transactional event writes.
- Reconcile koreluje ledger z Hermes, Git i Docker.
- Brak automatycznego cleanup przy ambiguity.

## Rejected

- JSON files bez transactional semantics.
- Pełna własna task FSM.
- Approval w task worktree.
