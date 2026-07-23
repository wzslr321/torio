# ADR-0002: Hermes pozostaje agent runtime i task engine

- Status: Accepted
- Date: 2026-07-23

## Context

Hermes ma już model execution, profiles, sessions, memory, messaging gateway, Kanban SQLite, claims, dispatcher, retries, heartbeats, worker processes i events.

## Decision

Hermes Box nie implementuje drugiej queue, dispatchera, retry engine ani agent loop. `hb` jest admission/policy/artifact control plane. Przechowuje security facts i korelację z Hermes task/run IDs.

## Consequences

- Kanban `done` oznacza „worker zakończył próbę / dostarczył candidate”, nie „integrated”.
- `hb.db` nie może stać się konkurencyjnym task status store.
- Adapter Hermes jest kontraktowo testowany względem przypiętej wersji.

## Rejected

- Własny scheduler z pełną FSM: duplikacja i ryzyko rozjazdu.
- Bezpośredni zapis do Kanban SQLite: coupling do prywatnego schema i naruszenie ownershipu.
