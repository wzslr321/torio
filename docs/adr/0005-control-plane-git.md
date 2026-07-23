# ADR-0005: Worker nie otrzymuje Git authority

- Status: Superseded
- Superseded by: [ADR-0011](0011-materialized-git-free-workspaces.md)
- Date: 2026-07-23

> **Superseded by [ADR-0011](0011-materialized-git-free-workspaces.md) (2026-07-23).** Spike S5
> obalił „masking-only” jako security control. Intencja (worker bez Git authority) pozostaje;
> mechanizm zmienia się na materialized Git-free workspace. Poniższa treść zachowana jako historia.

## Context

Linked worktree ma `.git` wskazujący główny repository metadata, często absolutną host path. Zamontowanie tylko worktree łamie Git w kontenerze; zamontowanie głównego `.git` rozszerza władzę workera na refs i inne worktrees.

## Decision

Control plane przygotowuje exact host worktree/checkout. Workload otrzymuje editable tree bez używalnego Git metadata. Worker edytuje i testuje, ale nie commit/merge/push. Trusted Git adapter tworzy candidate/review commit po zatrzymaniu workera.

## Consequences

- `hb` ma mały security-sensitive workspace/Git lifecycle adapter.
- Nie buduje generic worktree schedulera ani queue.
- Submodules/LFS wymagają przygotowania przez control plane przed startem.
- Worker potrzebujący historii otrzymuje narrow read-only API w późniejszym etapie, nie `.git`.

## Rejected

- Write access do głównego `.git`.
- Push credential bez network jako „wystarczająca” kontrola.
- Ufanie commitowi utworzonemu przez workera jako review artifact.
