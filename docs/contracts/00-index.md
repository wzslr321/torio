# Contract index

Kontrakty są normatywne. Zmiana pola, exit code, invariantu lub semantyki wymaga testu kontraktowego i ADR-u, jeśli wpływa na granice odpowiedzialności.

| Contract | Dokument | Schema |
|---|---|---|
| CLI | [`cli.md`](cli.md) | JSON envelope opisany inline |
| Project registry | [`project-config.md`](project-config.md) | [`project.schema.json`](../../schemas/project.schema.json) |
| Task admission | [`task-request.md`](task-request.md) | [`task-request.schema.json`](../../schemas/task-request.schema.json) |
| Effective policy | [`effective-policy.md`](effective-policy.md) | [`effective-policy.schema.json`](../../schemas/effective-policy.schema.json) |
| Ledger/event | [`state-ledger.md`](state-ledger.md) | [`state-event.schema.json`](../../schemas/state-event.schema.json) |
| Review evidence | [`review-evidence.md`](review-evidence.md) | [`review-evidence.schema.json`](../../schemas/review-evidence.schema.json) |
| Executor | [`executor.md`](executor.md) | Go interface contract |
| Services | [`service-lifecycle.md`](service-lifecycle.md) | CLI/process probes |
| Backup | [`backup-recovery.md`](backup-recovery.md) | manifest contract |

## Versioning

- Wszystkie dokumenty i schematy zaczynają od `schema_version: "1"`.
- Dodanie opcjonalnego pola jest kompatybilne w ramach v1.
- Usunięcie pola, zmiana typu, defaultu bezpieczeństwa lub semantyki wymaga v2.
- Unknown fields są odrzucane (`additionalProperties: false`) w trusted configuration.
- Czytelnik nowszego minor contractu musi fail closed, jeśli nie rozumie capability field.
