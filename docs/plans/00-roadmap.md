# Roadmap

## Gate model

Etapy są sekwencyjne. Implementer NIE MOŻE rozpocząć następnego etapu, dopóki wszystkie gate'y poprzedniego nie mają evidence i decyzji PASS/FAIL.

```text
0 Spike ──PASS──> 1 Demo A ──PASS──> 2 Demo B ──PASS──> 3 Hardening
   │                  │                  │
  FAIL               FAIL               FAIL
   └── revise ADR/contracts, do not continue
```

## Etap 0 — Runtime contract spike

Cel: usunąć niepewności na styku Hermes/Lima/Docker/Git przed produkcyjnym kodem.

Deliverables:

- version matrix,
- Desktop backend evidence,
- gateway lifecycle evidence,
- Kanban/worker environment evidence,
- Docker per-task isolation evidence,
- worktree/Git mount evidence,
- skills/env/credential evidence,
- crash/reconcile observations,
- decyzja GO/NO-GO i aktualizacja ADR-ów.

Plan: [`01-spike.md`](01-spike.md).

## Etap 1 — Demo A: Remote Hermes Box

Cel: trwały Hermes Brain w Lima, dostępny lokalnie z macOS.

Deliverables:

- Go CLI skeleton i command contracts,
- Lima lifecycle,
- deterministic non-secret bootstrap,
- serve service + loopback access,
- native gateway lifecycle wrapper,
- doctor/readiness,
- restart acceptance test,
- narrow transfer folder.

Plan: [`02-demo-a.md`](02-demo-a.md).

## Etap 2 — Demo B: Safe Coding Worker

Cel: jeden projekt i jeden end-to-end candidate flow z T1 isolation.

Deliverables:

- project registry,
- task admission,
- policy snapshot,
- fresh task executor,
- workspace without Git authority,
- candidate freeze,
- fresh verifier,
- review evidence,
- admin approval,
- fast-forward integration,
- explicit push,
- negative security tests,
- restart reconciliation.

Plan: [`03-demo-b.md`](03-demo-b.md).

## Etap 3 — Hardening

- separate OS identities,
- admin/submit sockets,
- CP-owned/rootless Docker,
- SSH executor,
- signed/pinned policy bundles,
- project-internal networks,
- credential broker,
- multi-project concurrency,
- backup/restore drill.

## Etap 4 — Company-ready

- remote/disposable runners,
- central audit export,
- policy governance,
- managed image build and provenance,
- organisation identity,
- incident response and retention.

## Explicitly deferred

Patrz [`04-future.md`](04-future.md). Element deferred nie może być dodany przy okazji wcześniejszego taska.
