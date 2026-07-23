# Project layout

## Aktualny pakiet

```text
AGENTS.md                    nadrzędne zasady dla LLM/developera
README.md                    entry point
schemas/                     normatywne JSON Schemas
examples/                    walidowane przykłady
prompts/                     phase/task/review prompts
scripts/                     artifact validation
spikes/                      throwaway spike code only
templates/                   trusted templates, nie runtime evidence
docs/
  adr/                       decyzje architektoniczne
  contracts/                 normatywne interfaces/state/config
  plans/                     phase gates i vertical slices
  spike-results/             real runtime evidence
.hermes/plans/               plan wykrywany przez Hermes
```

## Docelowy kod — tworzony stopniowo

```text
cmd/hb/                      composition root only
internal/cli/                command parsing, JSON envelope, exit mapping
internal/app/                use-case orchestration
internal/config/             XDG config and version locks
internal/execx/              safe external command runner
internal/redact/             central redaction
internal/lima/               Lima adapter
internal/hermes/             verified Hermes CLI/API adapter
internal/project/            trusted registry
internal/policy/             resolution, validation, canonical hash
internal/ledger/             SQLite migrations/repositories/events
internal/workspace/          task checkout/worktree boundary
internal/gitx/               typed security-sensitive Git adapter
internal/executor/           workload/verifier interface and adapters
internal/evidence/           candidate/verification/review bundles
internal/admin/              capability and approval/integrate/push
internal/reconcile/          observation/classification/repair planning
migrations/                  embedded ordered SQLite migrations
```

## Dependency direction

```text
cmd → cli → app → domain interfaces
                 ↘ adapters (git/hermes/lima/executor/ledger)
```

- Domain/use cases nie importują CLI.
- Adapters nie wywołują się przez global state.
- `os/exec`, SQL i filesystem są na brzegach.
- Nie twórz wszystkich katalogów jako empty scaffolding; dodawaj je z pierwszym vertical behavior.

## Naming

Używaj różnych nazw:

- `Runtime` — proces Hermesa,
- `Workload` — niezaufany task container,
- `Verifier` — niezależny sandbox,
- `Candidate` — frozen Git artifact,
- `Approval` — human decision,
- `Integration` — target ref mutation.

Unikaj ogólnego `Worker`, gdy nie wiadomo, czy chodzi o runtime, model, proces czy kontener.
