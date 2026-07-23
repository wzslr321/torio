# ADR-0011: Materialized Git-free workspaces for sandboxed workers

- Status: Accepted
- Date: 2026-07-23
- Supersedes: ADR-0005

## Context

ADR-0005 zakładało, że control plane przygotuje worktree/checkout i odbierze workerowi
używalne Git metadata. Spike S5 (host-side characterization, PASS) obalił kluczową część tej
strategii jako security control: natywny Kanban worktree powstaje **wewnątrz** repozytorium
(`<repo>/.worktrees/<id>`), tworzy branch i ma ref-authoritative `.git`; a **samo zamaskowanie
`.git` NIE odbiera władzy** — git discovery escapuje w górę do repo-przodka (odtworzono
utworzenie refa w realnym repo). „masking-only” jest więc niewystarczające (FAIL).

## Decision

Dla izolowanych coding workerów workspace **nie jest** natywnym Git worktree.

- Worker nie dostaje Git worktree.
- Control plane materializuje **exact base tree** do plain directory z dokładnego `base_commit`.
- Directory leży **poza** project repository — nie ma osiągalnego repo-przodka.
- Directory **nie zawiera** `.git`.
- Kanban otrzymuje workspace jako `dir:<prepared-workspace>`, nie `worktree`.
- Kanoniczny layout: `/var/lib/hermes-box/workspaces/<task-id>/<run-id>/workspace`; jest to
  jedyny writable mount kodu w workload containerze.
- Candidate tree rekonstruuje **zaufana strona** przez osobny, isolated Git index
  (semantycznie: read base tree → stage materialized workspace `add -A` → `write-tree` →
  `commit-tree` z base parent). Workload nie wykonuje żadnych Git operations.
- `GIT_CEILING_DIRECTORIES=/workspace` jest **defense-in-depth**, nie podstawową granicą.
- Verifier MUSI negatywnie sprawdzić Git discovery i ref mutation (np. `git -C /workspace
  rev-parse` / próba `update-ref` zawodzą).

## Consequences

- Nie musimy bezpiecznie maskować linked-worktree `.git`, bo workspace w ogóle nie jest worktree.
- `hb` ma mały security-sensitive materialization/Git-reconstruction adapter po zaufanej stronie.
- Dalszy spike musi doprecyzować komendy dla: deletion, executable bit, symlink, rename,
  submodule, Git LFS (poprawność rekonstrukcji tree).
- Effective policy zyskuje pola opisujące workspace jako materialized-tree (patrz
  `docs/contracts/effective-policy.md`, `schemas/effective-policy.schema.json`).

## Rejected

- Natywny Kanban worktree jako workspace workera (daje ref-authoritative `.git`).
- „masking `.git` wystarcza” jako security control (obalone w S5).
- Poleganie wyłącznie na `GIT_CEILING_DIRECTORIES` bez usunięcia osiągalnego repo-przodka.
