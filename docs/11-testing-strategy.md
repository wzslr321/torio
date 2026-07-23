# Testing strategy

## Pyramid

### Unit

Pure logic i adapters z fake process runner/filesystem/clock:

- config/schema/defaults,
- canonical policy hashing,
- CLI envelopes/exit codes,
- path/symlink validation,
- approval tuple equality,
- derived status,
- redaction.

### Contract

Real binary/library behavior na kontrolowanych fixtures:

- Hermes CLI surface i JSON/text parsing,
- Git temporary repo/worktree/object OIDs,
- SQLite migrations/transactions,
- Docker execution spec/inspect mapping,
- systemd/Lima rendered artifacts.

Parser fixtures zawierają success, old/new version, malformed i secret-bearing error.

### Integration

Linux VM:

- real Git,
- real SQLite,
- real Docker containers,
- test Hermes profile/board,
- process kill/restart,
- filesystem permissions.

### End-to-end

Real macOS + Lima + Desktop dla Demo A; full Kanban task→candidate→verify→approve→integrate→push-to-local-bare-remote dla Demo B.

### Adversarial

TM-01–TM-15. Każdy control ma próbę bypassu. Konfiguracja lub `inspect` bez próby dostępu nie jest wystarczającym dowodem.

## TDD

Dla produkcyjnego behavior:

```text
RED: jeden test pokazuje brak zachowania
GREEN: minimalna implementacja
REFACTOR: bez zmiany behavior
REGRESSION: pakiet + validator + race tam, gdzie state/concurrency
```

Nie wolno pisać testu dopiero po implementacji i deklarować TDD.

## Test isolation

- Każdy test ma własny temp dir/DB/repo.
- Nie używaj realnego `~/.hermes` ani globalnego Docker state bez explicit integration tag.
- Integration resources mają unique labels i cleanup w `t.Cleanup`/equivalent.
- Test cleanup nie usuwa zasobu, którego labels nie pasują.
- Używaj canary credentials, nigdy realnych env.

## Go commands

Docelowo:

```bash
go test ./...
go test -race ./...
go vet ./...
python3 scripts/validate_artifacts.py
```

Integration/E2E muszą mieć build tags lub osobne harness commands i jawne prerequisites. Unit suite nie może przypadkowo uruchomić Dockera/Lima/Hermesa.

## Fault matrix

Testuj kill/restart po każdym durable boundary:

| Boundary | Expected recovery |
|---|---|
| policy committed, task not created | safe retry/idempotent bind |
| task created, execution not started | dispatcher/native state authoritative |
| workload running | inspect/reclaim/retry; no false candidate |
| stopped, candidate absent | capture may safely resume |
| candidate persisted, verify absent | rerun fresh verifier |
| verified, approval absent | review-ready |
| approved, integration absent | revalidate tuple/base |
| integrated, push absent | no implicit push; explicit resume |

## Security regression gate

Zmiana executor/policy/workspace/Git/approval/admin wymaga relewantnego negatywnego testu. Brak możliwości uruchomienia testu jest blockerem release, nie powodem do pominięcia.
