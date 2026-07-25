# CLI contract

## Binary i output

Binary nazywa się `hb`. Domyślnie wypisuje czytelny output na stdout i diagnostykę na stderr. `--json` zwraca dokładnie jeden JSON document na stdout i nie miesza z nim logów.

### JSON envelope

```json
{
  "schema_version": "1",
  "ok": true,
  "command": "task.status",
  "data": {},
  "warnings": [],
  "error": null
}
```

Błąd:

```json
{
  "schema_version": "1",
  "ok": false,
  "command": "task.integrate",
  "data": null,
  "warnings": [],
  "error": {
    "code": "STALE_BASE",
    "message": "target ref no longer matches the reviewed base",
    "details": {
      "expected_base": "<oid>",
      "actual_base": "<oid>"
    }
  }
}
```

`message` nie może zawierać credentials, raw env ani pełnych command lines zawierających sekrety.

## Exit codes

| Exit | Klasa | Przykład |
|---:|---|---|
| 0 | success/idempotent success | już zintegrowany exact commit |
| 2 | usage/schema validation | brak argumentu, invalid config |
| 3 | unmet precondition | VM stopped, task not frozen |
| 4 | policy denied | forbidden mount/tool/skill |
| 5 | stale/conflict | base/candidate/policy changed |
| 6 | verification failed | trusted check exit != 0 |
| 7 | permission/capability denied | brain attempts admin action |
| 8 | external dependency failed | Hermes/Docker/Git unavailable |
| 9 | reconciliation required | state/resource disagreement |

## Global flags

```text
--json                 machine output
--config PATH          explicit non-secret config
--state-dir PATH       test/diagnostic override
--timeout DURATION     bounded operation; cannot exceed policy maximum
--verbose              more redacted diagnostics on stderr
```

Nie ma globalnego `--force` omijającego policy. Komendy mogą mieć wąskie, udokumentowane recovery flags, ale nie mogą omijać approval, base check ani credential boundaries.

### Dostępność per slice (implementacja)

Powyższa lista to docelowy kontrakt. Zaimplementowane globalne flagi zależą od slice'a i nieznane
flagi są odrzucane (usage, exit 2) — nie są po cichu akceptowane:

- **D1:** `--json`, `--verbose`, `--timeout`. `--config` i `--state-dir` są **D2-pending** i w D1
  zwracają usage error.
- **D2:** dochodzą `--config PATH` i `--state-dir PATH` jako realne globalne (persistent) flagi,
  działające przed i po subkomendzie. Resolują się do typowanej konfiguracji D2 (patrz
  [`config.md`](config.md)) używanej przez wykonanie komendy — nie są tylko parsowane. Błąd
  resolucji/walidacji konfiguracji jest usage/schema error (exit 2). Nieznana flaga nadal jest
  odrzucana (exit 2).

### `--help` a `--json`

`--help`/`-h` jest jedynym, wąskim wyjątkiem od reguły „jeden JSON envelope na stdout w trybie
`--json`". Help to afordancja dla człowieka: wypisuje tekst usage na stdout i kończy exit 0, także gdy
podano `--json` (nie emituje envelope). Każde inne wyjście w trybie `--json` MUSI być dokładnie jednym
envelope.

## Command surface

### Informacyjne

```text
hb version [--json]
hb doctor [--json]
hb status [--json]
hb reconcile [--dry-run] [--json]
```

`doctor` wykonuje rzeczywiste proby: binary/version, service state, socket/port bind, Git, Docker, Lima, Hermes CLI surface, state migrations i permissions.

### VM

```text
hb vm init [--template PATH]
hb vm start
hb vm stop
hb vm status
hb vm ssh [-- COMMAND...]
hb vm logs
```

- `init` jest idempotentne dla zgodnego template digestu.
- Zmiana niebezpiecznego template field wymaga jawnego recreate planu.
- `stop` nie usuwa VM ani danych.

### Backend i gateway

```text
hb serve install
hb serve start|stop|restart|status|logs
hb gateway install
hb gateway start|stop|restart|status|logs
```

- `serve install` zarządza własną user service wyłącznie po feature detection.
- `gateway install` deleguje do natywnego `hermes gateway install` i nie zgaduje nazwy unitu.
- `serve` binduje loopback w VM w Demo A.

### Projects

```text
hb project add --file PROJECT.yaml
hb project list
hb project show PROJECT_ID
hb project validate PROJECT_ID
hb project remove PROJECT_ID
```

`remove` usuwa wpis registry, nie repo. Odmawia, jeśli istnieje aktywne execution lub niezamknięty candidate.

### Task submit i observation

```text
hb task submit --project PROJECT_ID --file REQUEST.json [--idempotency-key KEY]
hb task status TASK_ID
hb task events TASK_ID
hb task logs TASK_ID [--execution EXECUTION_ID]
hb task review TASK_ID
hb task discard TASK_ID
```

`submit` jest jedyną capability dostępną narrow API Braina. Brain nie dostaje ogólnego lokalnego `hb` terminala.

`review` nie zatwierdza. Pokazuje exact candidate, policy i verification evidence.

### Human-only admin

```text
hb admin approve TASK_ID --candidate REVIEW_COMMIT
hb admin revoke TASK_ID --candidate REVIEW_COMMIT --reason TEXT
hb admin integrate TASK_ID --candidate REVIEW_COMMIT
hb admin push TASK_ID --candidate REVIEW_COMMIT --remote REMOTE
```

Wymagania:

- połączenie z protected admin endpointem niedostępnym dla brain/worker OS identity,
- explicit candidate OID,
- brak domyślnego „latest” dla mutujących admin commands,
- interactive confirmation w human mode,
- `--json` nie wyłącza capability check,
- nie istnieje `--yes` pozwalające Brainowi obejść admin identity.

## Hermes adapter — mutation postconditions

Adapter `hb → hermes kanban` NIE MOŻE traktować exit code procesu Hermesa jako wystarczającego
postcondition security-relevant mutacji stanu. Ustalenie ze spike'u: `hermes kanban claim` przy
konflikcie **wypisuje odmowę, ale kończy się exit `0`** (patrz [spike-results/03-kanban-worker.md](../spike-results/03-kanban-worker.md)).

Reguła (dotyczy `claim` i każdej innej security-relevant mutacji, np. `reclaim`, `complete`):

- exit `0` sam w sobie **nie** dowodzi udanej zmiany stanu,
- adapter używa structured output (`--json`), jeśli jest dostępny,
- po mutacji adapter wykonuje **re-query** stanu i potwierdza:
  - task status,
  - run ID,
  - lock owner,
  - expected execution identity,
- brak jednoznacznego potwierdzenia oczekiwanego stanu (niejednoznaczny output lub mismatch)
  oznacza **fail closed** — traktuj mutację jako nieudaną, nie kontynuuj.

## Idempotency

- `task submit`: dedup po `(project_id, idempotency_key)`.
- `approve`: drugi identyczny approval tego samego aktora/artifactu zwraca success; inny artifact wymaga nowego approval.
- `integrate`: success, jeśli target już wskazuje exact approved commit; stale, jeśli wskazuje coś innego.
- `push`: success, jeśli remote ref już wskazuje exact integrated commit.
- `discard`: nie usuwa audit records.
