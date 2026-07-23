# Product brief

## Problem

Hermes Agent daje wygodny agent runtime, pamięć, komunikatory, Kanban i wykonanie narzędzi, ale nie definiuje kompletnej polityki dla bezpiecznego delegowania coding tasks z prywatnego Braina do ograniczonych workerów. Sam Dev Container zapewnia reprodukowalność, nie pełną izolację. Profil Hermesa oddziela konfigurację, ale nie jest sandboxem.

## Produkt

Hermes Box jest opinionated control plane dla Apple Silicon/macOS:

```text
macOS → Lima VM → Hermes Brain/Kanban → per-task workload container
                                  ↘ Hermes Box policy/evidence pipeline
```

Zapewnia:

- izolację agent runtime od macOS,
- reprodukowalny bootstrap VM,
- persistent Brain dostępny przez Desktop i messaging gateway,
- controlled task admission,
- fresh execution boundary per coding task,
- brak credentials i integration authority w workerze,
- content-addressed review evidence,
- human approval przed integration i push.

## Primary user

Pojedynczy techniczny użytkownik na Macu Apple Silicon, który:

- chce prywatnego/local-first second braina,
- pracuje nad kilkoma repozytoriami,
- używa LLM-ów do coding tasks,
- potrzebuje lepszej granicy niż zwykły lokalny agent z pełnym terminalem,
- akceptuje, że PoC nie jest hostile multi-tenant sandboxem.

## Jobs to be done

1. Uruchomić Hermes Brain w izolowanej VM i korzystać z niego jak z lokalnej aplikacji.
2. Zgłosić coding task bez przekazania workerowi Git/host credentials.
3. Otrzymać reviewable, zweryfikowany candidate commit.
4. Zatwierdzić exact artifact, a nie zmienny katalog roboczy.
5. Zintegrować i opcjonalnie wypchnąć wyłącznie po jawnej decyzji człowieka.
6. Po restarcie odtworzyć stan bez zgadywania, które taski/kontenery/worktrees są aktywne.

## Miary sukcesu

### Demo A

- Desktop łączy się z backendem w VM przez loopback + SSH transport/tunnel.
- Backend nie nasłuchuje publicznie.
- Sesje, memory i gateway survive restart.
- Repozytoria znajdują się na Linux filesystemie VM.
- `hb doctor` potrafi wykazać realny stan, nie tylko obecność plików.

### Demo B

- Jeden task tworzy jeden świeży workload container.
- Worker widzi tylko task workspace/input/output.
- Próby odczytu innych repo, Git metadata, Docker socket i host credentials kończą się odmową.
- Candidate code jest weryfikowany w drugim, świeżym sandboxie.
- Zmiana candidate, policy, verification evidence albo target base unieważnia approval.
- Bez approval nie można integrate; bez osobnej komendy nie można push.

## Zasada produktowa

Każda funkcja musi odpowiadać na jedno z pytań:

- Czy poprawia reprodukowalność?
- Czy technicznie egzekwuje granicę bezpieczeństwa?
- Czy zwiększa audytowalność exact artifact?
- Czy poprawia restart/recovery?

Jeśli nie, nie należy do pierwszych etapów.
