# ADR-0004: Natywny Docker backend Hermesa w Demo B

- Status: Accepted for PoC
- Date: 2026-07-23

## Context

Najmocniejszy wariant daje Docker wyłącznie control plane i łączy worker runtime z task containerem przez SSH. Jest jednak dodatkową warstwą przed potwierdzeniem podstawowego flow.

Aktualny Hermes ma natywny Docker backend routujący terminal/file/execute operations do kontenera bez domyślnego mountu Docker socketa.

## Decision

Demo B używa natywnego Docker backendu, traktując proces Hermes Worker Runtime jako część TCB. Workload container pozostaje niezaufany.

Wymagane:

- świeży boundary per task,
- cross-process persistence disabled,
- pinned image digest,
- `network none`,
- minimalne mounty,
- tool/skill/credential allowlist,
- labels i reconciliation tests.

## Consequences

- Proces Hermesa ma hostowy dostęp potrzebny do Docker adaptera.
- PoC osiąga T1, nie T2.
- Interface `Executor` zachowuje seam dla późniejszego CP-owned Docker + SSH.

## Rejected

- SSH executor od pierwszego dnia: przedwczesna złożoność.
- Docker-in-Docker: zbędny daemon i większa powierzchnia ataku.
- Persistent container per project: przeciek między taskami.
