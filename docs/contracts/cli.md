# CLI contract

> **STATUS (częściowo superseded — czytaj razem z ADR-0015).** Ten kontrakt powstał dla
> platformy **pre-V0** i opisuje command surface, którego dostarczona binarka nie ma:
> `doctor`, `status`, `reconcile`, `vm logs`, `gateway`, `project`, `task` i `admin` **nie
> istnieją** w CLI. Zaimplementowane są `version`, `vm` (`init`, `status`, `start`, `stop`,
> `bootstrap`, `ssh`), `serve` i `brain`. Normatywne i aktualne pozostają: nazwa binarki, JSON envelope,
> tabela exit codes, reguła „jeden envelope na stdout" oraz opisane niżej postconditions
> `vm bootstrap` i `serve`. Tam, gdzie ten dokument jest sprzeczny z
> [ADR-0015](../adr/0015-torio-v1-onboarding-projects-and-operator-push.md), **wygrywa ADR** —
> mimo kolejności z `AGENTS.md` §3, bo ADR-0015 jawnie supersedes zakres tej platformy
> (patrz [`legacy-architecture.md`](../legacy-architecture.md)). Superseded treść jest
> zachowana celowo jako kontekst historyczny; nie implementuj jej.

## Binary i output

Binary nazywa się `torio` (zmiana nazwy: [ADR-0014](../adr/0014-rename-to-torio.md)). Domyślnie wypisuje czytelny output na stdout i diagnostykę na stderr. `--json` zwraca dokładnie jeden JSON document na stdout i nie miesza z nim logów.

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
torio version [--json]
torio doctor [--json]
torio status [--json]
torio reconcile [--dry-run] [--json]
```

`doctor` wykonuje rzeczywiste proby: binary/version, service state, socket/port bind, Git, Docker, Lima, Hermes CLI surface, state migrations i permissions.

### VM

```text
torio vm init [--template PATH]
torio vm start
torio vm stop
torio vm bootstrap
torio vm status
torio vm ssh [-- COMMAND...]
torio vm logs
```

- `init` jest idempotentne dla zgodnego template digestu.
- Zmiana niebezpiecznego template field wymaga jawnego recreate planu.
- `stop` nie usuwa VM ani danych. Jest idempotentne (już `Stopped` → exit 0) i nie ufa czystemu exit code: po `limactl stop` re-query wymaga stanu `Stopped`, inaczej fail-closed (exit 3). Nigdy nie używa `--force`.
- `bootstrap` reconciliuje i weryfikuje target dla Remote Second Brain V1: stabilną, nieinteraktywną komendę `hermes` oraz układ trwałych katalogów gościa na natywnym Linux filesystem. Działa wyłącznie na istniejącym targetcie po zweryfikowanym warunku `Running`, przez typed Lima/execx boundary (fixed argv, bez `sh -c`, bez sklejanych stringów, bounded/redacted output). Jest idempotentne i wąskie: może doinstalować pinowany launcher Hermes Agent oraz symlink PATH `/usr/local/bin/hermes`, ale **nie** recreatuje/re-image'uje VM, nie instaluje modelu/providera, nie przyjmuje sekretów i nie tworzy usług gateway/serve.
- **Docker: `hermes` NIE MOŻE należeć do grupy `docker`.** Członkostwo w `docker` jest równoważne rootowi na gościu, więc rootful Docker dla tożsamości serwisowej `hermes` jest zakazany przez [ADR-0015](../adr/0015-torio-v1-onboarding-projects-and-operator-push.md). `bootstrap` **weryfikuje brak** tego członkostwa (`verifyHermesNotInDocker`) i fail-closed, gdy je znajdzie; template provisioningu usuwa `hermes` z `docker`, jeśli grupa istnieje. W V1 Docker Engine nie jest w ogóle instalowany, a `bootstrap` **nie** sprawdza osiągalności serwera Docker. Przyszły container runtime wymaga rootless, hermes-owned rozwiązania za osobnym ADR-em.
- `bootstrap` **weryfikuje** (nie ufa samemu exit code): istnienie użytkownika `hermes`; istnienie grupy `torio-projects` oraz członkostwo w niej `hermes` i operatora (login identity Limy); **brak** członkostwa `hermes` w grupie `docker`; `uname -m == aarch64`; `hermes --version` przez tę samą, dokumentowaną stabilną ścieżkę; `git --version`; że każda wymagana ścieżka jest katalogiem o oczekiwanym owner/group/mode na natywnym Linux filesystem (nie host share); oraz brak szerokiego host mountu macOS. Każdy nieznany/nieweryfikowalny stan lub drift (architektura/wersja/ownership/mount) jest raportowany i fail-closed (exit 6), nie papering over. Rerun jest sukcesem tylko gdy wszystkie postconditions są udowodnione.
- Wymagane ścieżki i ich role są rozłączne (ADR-0015; stałe w `internal/lima/bootstrap.go`) — `/home/hermes/.hermes` **nie jest** Knowledge Base:

  | Stała | Ścieżka | Rola |
  |---|---|---|
  | `HermesHome` | `/home/hermes` | home tożsamości serwisowej |
  | `HermesProfilePath` | `/home/hermes/.hermes` | profil i stan aplikacyjny Hermesa (`$HERMES_HOME`) |
  | `HermesBrainPath` | `/home/hermes/brain` | vault Second Brain |
  | `HermesWorkspacePath` | `/home/hermes/projects` | współdzielony workspace projektów |

  `bootstrap` weryfikuje profil i brain **niezależnie**; żadna z tych ścieżek nie jest aliasem drugiej.
- `bootstrap` wykonuje kilka bounded guest probes; uruchamiaj z odpowiednio dużym `--timeout` (np. `--timeout 5m`). Akcja dotarcia do zdalnego Hermesa po bootstrapie pozostaje operator-controlled (np. `torio vm ssh -- sudo -u hermes -- hermes --version`).

### Backend i gateway

```text
torio serve install
torio serve start|stop|restart|status|logs
torio gateway install
torio gateway start|stop|restart|status|logs
```

- `serve install` zarządza własną **user** service (custom systemd unit dla użytkownika `hermes`)
  wyłącznie po feature detection (`hermes serve --help`). W D5 (V1) generuje deterministyczny unit
  `hermes-serve.service` z pinowanym loopback bindem (`--host 127.0.0.1 --port 9119`), `HERMES_HOME=/home/hermes/.hermes`
  i `Restart=always`, waliduje go `systemd-analyze --user verify` **przed aktywacją**, po czym
  `daemon-reload` + `enable`. Zapewnia `linger` dla `hermes`, by usługa `Restart=always` działała bez
  interaktywnej sesji i po reboot. Jest idempotentne (re-run bez zmiany = `changed:false`), nie przyjmuje
  sekretów i **nie startuje** backendu. Zapis unitu jest atomowy (staging → verify → rename); niepoprawny
  unit nigdy nie jest aktywowany. Kilka bounded guest probes — używaj większego `--timeout` (np. `--timeout 2m`).
- `serve start`/`restart` startują backend i **weryfikują** readiness: re-query stanu systemd (`is-active == active`)
  **oraz** rzeczywistą odpowiedź `GET /api/status == 200` przez loopback. Aktywny proces z martwym endpointem
  to porażka (exit 6). Idempotentne. `serve stop` jest graceful i idempotentne (re-query wymaga stanu
  nie-active), nie usuwa unitu/profilu/state.
- `serve status` udowadnia **oba**: stan user-systemd i faktyczną gotowość endpointu przez loopback.
  Exit 0 tylko gdy `active` i `/api/status == 200`; brak instalacji lub inactive → exit 3; aktywny z martwym
  endpointem → exit 6. Nie modyfikuje systemu.
- `serve logs [--lines N]` zwraca bounded, redagowane wpisy journala **tylko** dla unitu
  (`journalctl --user -u hermes-serve.service -n N --no-pager`) — scope'owane do unitu i
  redagowane przez execx, więc nie ujawnia własnej konfiguracji `torio` (profil/brain/provider). Nie jest
  to jednak absolutna gwarancja: własny stdout/stderr backendu Hermes może teoretycznie zawierać
  tekst pochodny od danych użytkownika. Traktuj to jako runtime-only ograniczenie ekspozycji, nie
  formalną gwarancję prywatności.
- `gateway install` deleguje do natywnego `hermes gateway install` i nie zgaduje nazwy unitu.
- `serve` binduje loopback w VM w Demo A. Dotarcie z Maca to operator-controlled SSH tunnel do gościa
  `127.0.0.1:9119` (patrz [runbook](../runbooks/remote-second-brain-v1.md)); `torio` nie dodaje własnej
  funkcji tunelu. `serve` to **backend Desktopu**, a nie `torio gateway` (messaging).

### Projects

```text
torio project add --file PROJECT.yaml
torio project list
torio project show PROJECT_ID
torio project validate PROJECT_ID
torio project remove PROJECT_ID
```

`remove` usuwa wpis registry, nie repo. Odmawia, jeśli istnieje aktywne execution lub niezamknięty candidate.

### Task submit i observation

```text
torio task submit --project PROJECT_ID --file REQUEST.json [--idempotency-key KEY]
torio task status TASK_ID
torio task events TASK_ID
torio task logs TASK_ID [--execution EXECUTION_ID]
torio task review TASK_ID
torio task discard TASK_ID
```

`submit` jest jedyną capability dostępną narrow API Braina. Brain nie dostaje ogólnego lokalnego `torio` terminala.

`review` nie zatwierdza. Pokazuje exact candidate, policy i verification evidence.

### Human-only admin

```text
torio admin approve TASK_ID --candidate REVIEW_COMMIT
torio admin revoke TASK_ID --candidate REVIEW_COMMIT --reason TEXT
torio admin integrate TASK_ID --candidate REVIEW_COMMIT
torio admin push TASK_ID --candidate REVIEW_COMMIT --remote REMOTE
```

Wymagania:

- połączenie z protected admin endpointem niedostępnym dla brain/worker OS identity,
- explicit candidate OID,
- brak domyślnego „latest” dla mutujących admin commands,
- interactive confirmation w human mode,
- `--json` nie wyłącza capability check,
- nie istnieje `--yes` pozwalające Brainowi obejść admin identity.

## Hermes adapter — mutation postconditions

Adapter `torio → hermes kanban` NIE MOŻE traktować exit code procesu Hermesa jako wystarczającego
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
