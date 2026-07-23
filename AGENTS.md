# AGENTS.md — instrukcje normatywne dla implementerów i LLM-ów

Ten plik jest nadrzędnym kontraktem pracy w repozytorium Hermes Box. Jeśli inny dokument lub prompt jest sprzeczny z tym plikiem, zatrzymaj pracę i zgłoś konflikt. Nie rozwiązuj konfliktu przez zgadywanie.

## 1. Misja

Budujemy cienki trusted control plane nad Hermes Agent, Lima, Dockerem i Git. Nie budujemy nowego frameworka agentowego, task queue ani ogólnego worktree managera.

## 2. Normatywne słowa

- **MUST / MUSI** — bezwarunkowy wymóg.
- **MUST NOT / NIE MOŻE** — bezwarunkowy zakaz.
- **SHOULD / POWINIEN** — domyślna decyzja; odstępstwo wymaga ADR-u.
- **MAY / MOŻE** — opcja.

## 3. Źródła prawdy

Kolejność ważności:

1. `AGENTS.md`.
2. Przyjęte ADR-y w `docs/adr/`.
3. Kontrakty w `docs/contracts/` i schematy w `schemas/`.
4. Threat model i architektura.
5. Plan aktualnego etapu.
6. Prompty pomocnicze.

Aktualna dokumentacja i kod Hermes Agent są źródłem prawdy o Hermesie. Nie używaj pamięci modelu do odgadywania komend, portów, opcji, paths ani lifecycle.

## 4. Niezmienne granice

### Hermes Agent jest ownerem

- model execution,
- profili, sesji i pamięci,
- messaging gateway,
- Kanban queue,
- dispatch, claims, retries i heartbeats,
- procesów workerów i task events.

### Hermes Box jest ownerem

- Lima lifecycle i provisioning,
- project registry i admission control,
- effective policy,
- execution specification,
- per-task isolation guarantees,
- security-sensitive Git operations,
- fresh sandboxed verification,
- review evidence,
- approval, integration i push.

### Hermes Box NIE MOŻE implementować

- alternatywnego agent loop,
- drugiego Kanbana,
- własnego dispatchera lub retry engine,
- autonomicznego merge/push,
- dowolnego wykonywania `devcontainer.json` z task branch,
- sekret managera klasy Vault w PoC,
- domenowego network allowlistu w PoC.

## 5. Security invariants

Każda implementacja MUSI zachować:

1. Repozytoria i state znajdują się na Linux filesystemie VM, nie na szerokim mountcie macOS.
2. Profile Hermesa nie są traktowane jako sandbox.
3. Jeden task ma świeży workload container albo równoważną osobną granicę wykonania.
4. Worker nie ma `/var/run/docker.sock`, członkostwa w grupie Docker wewnątrz workloadu ani hostowego Docker CLI.
5. Worker nie ma używalnego `.git`, push credentials ani admin capability.
6. Worker nie ma pamięci, sesji, credentials ani skills Braina.
7. Worker policy obejmuje host-side tools, MCP-y, skills i implicit credential/env passthrough.
8. Config/policy są czytane z trusted registry/base revision, nigdy z task branch jako authority.
9. Worker zostaje zatrzymany i traci write access przed snapshotem.
10. Weryfikacja candidate code odbywa się w świeżym verifier sandboxie, nigdy na hoście VM.
11. Approval jest związany z exact object IDs i hashami evidence.
12. Integracja PoC jest fast-forward-only i wymaga `target HEAD == approved base_commit`.
13. Push jest osobną, human-only operacją.
14. Brain nie może uzyskać capability `approve`, `integrate` ani `push` przez własny terminal.

## 6. Zasady implementacji

- Język control plane: Go 1.26.x; dokładny toolchain jest pinowany w repo.
- Używaj `log/slog`, `context.Context`, jawnych timeoutów i `os/exec.CommandContext`.
- Nie wykonuj komend przez `sh -c`, jeśli argumenty można przekazać bezpośrednio.
- Wszystkie zewnętrzne komendy mają typed adapter, timeout, capture exit code i redacted logs.
- Nie importuj prywatnych modułów Pythona Hermesa. Używaj zweryfikowanego CLI/API/plugin contract.
- Nie zapisuj do `~/.hermes/kanban.db` bezpośrednio.
- SQLite Hermes Box jest policy/evidence ledgerem, nie queue.
- Mutujące operacje muszą być idempotentne albo wymagać idempotency key.
- Każdy zapis state i artefaktu musi być crash-safe: temp file → fsync → atomic rename albo transakcja SQLite.
- Maszynowy output CLI jest stabilnym JSON envelope; ludzkie logi trafiają na stderr.
- Sekrety i przykłady credentials zapisuj wyłącznie jako `[REDACTED]`.

## 7. TDD i workflow

Dla każdej zmiany zachowania:

1. Napisz jeden failing test.
2. Uruchom go i potwierdź oczekiwaną porażkę.
3. Zaimplementuj minimum.
4. Uruchom test i cały relewantny pakiet.
5. Refaktoruj przy zielonych testach.
6. Uruchom `python3 scripts/validate_artifacts.py` oraz później `go test ./...`.
7. Zrób mały commit.

Nie pisz produkcyjnego kodu przed failing testem. Spike może tworzyć throwaway code tylko w `spikes/`; jego wyniki muszą trafić do `docs/spike-results/`, a kod spike'a nie przechodzi automatycznie do `internal/`.

## 8. Wymóg evidence

Nie deklaruj „działa” na podstawie dokumentacji. Zapisz:

- rzeczywistą komendę,
- wersje runtime,
- exit code,
- istotny output z redakcją,
- datę,
- wniosek,
- wpływ na ADR/kontrakt.

## 9. Zasady dla LLM-a

- Najpierw przeczytaj plan etapu i relewantne kontrakty.
- W jednym tasku zmieniaj jeden pionowy behavior slice.
- Nie rozszerzaj zakresu „dla przyszłości”.
- Nie twórz kompatybilności z niezweryfikowanymi wersjami Hermesa.
- Nie dodawaj mechanizmu tylko dlatego, że biblioteka go oferuje.
- Jeśli wymóg bezpieczeństwa jest technicznie niewykonalny, fail closed i zapisz problem.
- Nie zamieniaj enforcementu na promptową instrukcję.
- Nie modyfikuj ADR-u po cichu. Nowa decyzja wymaga kolejnego ADR-u superseding poprzedni.

## 10. Definition of done dla taska

Task jest gotowy wyłącznie gdy:

- acceptance criteria są spełnione,
- test najpierw zawiódł, potem przeszedł,
- regresje są zielone,
- output/logi nie zawierają sekretów,
- kontrakty i docs są zaktualizowane,
- `scripts/validate_artifacts.py` przechodzi,
- reviewer może odtworzyć wynik z zapisanych komend.
