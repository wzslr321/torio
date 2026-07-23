# Executor contract

## Cel

Executor oddziela control plane od mechanizmu wykonawczego. Demo B implementuje adapter natywnego Docker backendu Hermesa; Hardening może dodać control-plane-owned Docker + SSH bez zmiany evidence semantics.

## Pojęcia

- `ExecutionSpec` — immutable effective policy + workspace/input/output.
- `ExecutionHandle` — opaque ID i labels potrzebne do inspect/stop/reconcile.
- `VerifierSpec` — exact candidate + trusted checks.
- `ExecutionResult` — exit/lifecycle metadata, nie trusted test verdict.

## Wymagany interface semantyczny

```go
type Executor interface {
    Prepare(ctx context.Context, spec ExecutionSpec) (ExecutionHandle, error)
    Start(ctx context.Context, handle ExecutionHandle) error
    Inspect(ctx context.Context, handle ExecutionHandle) (ExecutionState, error)
    Stop(ctx context.Context, handle ExecutionHandle) error
    Destroy(ctx context.Context, handle ExecutionHandle) error
    RunVerifier(ctx context.Context, spec VerifierSpec) (VerificationResult, error)
    Reconcile(ctx context.Context, leases []Lease) ([]Observation, error)
}
```

To jest kontrakt, nie gotowy kod. Implementacja może rozdzielić metody, ale musi zachować zachowanie testowalne przez fake executor.

## Invariants

- `Prepare` nie uruchamia candidate code.
- `Start` jest idempotentny dla tego samego handle.
- `Stop` czeka na rzeczywiste zatrzymanie albo failuje.
- `Destroy` nie usuwa host workspace/candidate.
- `RunVerifier` zawsze tworzy fresh boundary.
- Wszystkie zasoby mają labels: project, task, execution, policy hash.
- Brak labela lub mismatch to `RECONCILIATION_REQUIRED`, nie automatyczny delete.
- Repo/task nie może dostarczyć raw Docker args.
