# Scope i non-goals

## In scope

### Etap 0 — spike

- feature detection aktualnego Hermesa,
- Desktop/serve przez SSH,
- gateway lifecycle,
- Kanban workspaces i worker environment,
- Docker persistence/isolation,
- worktree mount bez Git authority,
- skill/env/credential passthrough,
- verifier flow,
- ARM64 image compatibility.

### Demo A

- deklaratywny Lima template,
- deterministyczny bootstrap bez sekretów,
- `hb vm ...`, `hb serve ...`, `hb gateway ...`, `hb doctor`,
- loopback backend i SSH access,
- narrow host↔VM transfer directory,
- persistent Hermes Brain.

### Demo B

- trusted project registry,
- policy resolution i immutable snapshot,
- powiązanie z Hermes Kanban task/run,
- fresh native-Docker-backed workload per task,
- exact workspace mount bez Git authority,
- `network none` i minimalny tool/skill allowlist,
- candidate freeze,
- fresh verifier,
- review/approval/evidence,
- fast-forward integration,
- separate push,
- crash reconciliation.

## PoC non-goals

- własny agent framework lub model router,
- własna queue/dispatcher/retry engine,
- pełny web dashboard Hermes Box,
- multi-user tenancy,
- arbitrary untrusted repositories,
- kernel-grade malware sandbox,
- Kubernetes,
- Vault lub rozbudowany secret broker,
- domenowy egress allowlist,
- automatyczny PR/merge/push,
- pełna implementacja Dev Container Specification,
- wykonywanie `initializeCommand`, `privileged`, arbitrary mounts lub lifecycle hooks z task branch,
- Windows/Linux host jako primary platform,
- laptop jako gwarantowane 24/7 service,
- synchronizacja całego macOS home do VM.

## Hardening, nie PoC

- control-plane-owned Docker daemon i SSH executor,
- rootless Docker,
- osobne OS identities dla brain/runtime/control plane,
- ephemeral VM per untrusted task,
- project-internal service networks,
- short-lived secret broker,
- signed policy bundles,
- central audit export,
- multi-project concurrency.

## Zasada odrzucania scope creep

Nowa funkcja może wejść do aktualnego etapu tylko jeśli:

1. jest wymagana przez istniejące acceptance criterion, albo
2. zamyka wykazane ryzyko z threat modelu, oraz
3. nie duplikuje natywnej funkcji Hermesa.

W przeciwnym razie trafia do `docs/plans/04-future.md`.
