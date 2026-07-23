# Deferred future work

Elementy poniżej nie należą do Demo A/B.

## Executor hardening

- `hb-control` osobny OS user,
- Brain/runtime osobny OS user,
- admin i submit Unix sockets,
- rootless/dedicated Docker daemon,
- worker runtime → SSH task container,
- ephemeral Lima/remote runner dla hostile repos.

## Networking

- project-internal Docker network,
- service fixtures,
- egress proxy/allowlist,
- DNS and connection audit.

## Environment formats

- validated Dev Container subset,
- trusted-base lifecycle commands,
- pinned Features integrity,
- managed image build/provenance/SBOM.

## Secrets

- short-lived per-task credentials,
- read-only narrow mounts,
- broker/audit/revocation,
- no general host cloud profile.

## Product

- Desktop-integrated review UI,
- policy diff visualization,
- multi-project concurrency,
- central audit export,
- organisation roles,
- PR provider adapters,
- scheduled backup/restore drills.

## Explicit rule

Dodanie elementu wymaga nowego ADR-u, threat-model update i acceptance test. Nie implementować „przy okazji”.
