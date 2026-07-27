# AGENTS.md — instrukcje normatywne dla implementerów i LLM-ów

Ten plik jest nadrzędnym kontraktem pracy w repozytorium Torio. Jeśli inny dokument lub prompt jest sprzeczny z tym plikiem, zatrzymaj pracę i zgłoś konflikt. Nie rozwiązuj konfliktu przez zgadywanie.

## Status produktu (V0 delivered — V1 w implementacji)

**Aktualnie dostarczonym (released) produktem jest Torio V0 — wąski i w pełni
operatorski.** Opis V0 dla operatorów nadal leży w [`README.md`](README.md)
oraz dwóch runbookach:
[`docs/runbooks/remote-second-brain-v1.md`](docs/runbooks/remote-second-brain-v1.md)
i [`docs/runbooks/code-v0-REDACTED-PROJECT.md`](docs/runbooks/code-v0-REDACTED-PROJECT.md).
`README.md` **NIE** jest przepisywany na V1 przed finalnym release taskiem.

**Aktywny kierunek implementacji to Torio V1** (presentation-ready onboarding,
obowiązkowy Second Brain, multi-project, operator-only push). Zakres i decyzje
V1 definiują:
[`docs/adr/0015-torio-v1-onboarding-projects-and-operator-push.md`](docs/adr/0015-torio-v1-onboarding-projects-and-operator-push.md)
oraz plan
[`.hermes/plans/2026-07-27_131723-torio-v1-presentation-ready.md`](.hermes/plans/2026-07-27_131723-torio-v1-presentation-ready.md).

> Runbooki oraz strony w `site/` są **generowane** przez
> `scripts/build_docs.py` ze źródeł w `docs/content/` i współdzielą sekcje, więc
> nie mogą się rozjechać. Nie edytuj plików wygenerowanych — zmień źródło i
> uruchom `make docs`. `make validate` zawodzi, gdy output odbiega od źródła.

- Opisane niżej w tym pliku **platformowe** obowiązki i inwarianty Torio —
  w szczególności sekcje **4–5** (legacy project registry / admission control,
  per-task isolation, fresh verifier, approval/integration/push oraz
  worker/container/worktree invariants) — a także starsze `docs/plans/`,
  `docs/contracts/`, ADR-y 0001–0014 jako historia platformy, `prompts/` i
  starsze `docs/spike-results/`, opisują **superseded / pre-V0** eksplorację.
  Są zachowane **wyłącznie jako kontekst historyczny** (patrz
  [`docs/legacy-architecture.md`](docs/legacy-architecture.md)) i **NIE MOGĄ być
  implementowane ani traktowane jako następny task** w Torio V0 ani V1.
  ADR-0015 i plan V1 **nie** reaktywują tej platformy.
- Dyscyplina inżynierska i bezpieczeństwa z sekcji **6–10** (TDD, jeden wąski
  behavior slice na raz, `scripts/validate_artifacts.py`, typed adaptery i
  timeouty, redakcja sekretów jako `[REDACTED]`, wymóg evidence, brak sekretów w
  output/logach) **pozostaje w mocy** — o ile nie wymaga zarchiwizowanej platformy
  workerów/registry/verifiera.
- **Precedens dla pracy implementacyjnej Torio V1:** gdy sekcje platformowe tego
  pliku, legacy plany/kontrakty/ADR-y 0001–0014, prompty lub spike material są
  sprzeczne z ADR-0015 albo planem V1, autorytetem są **ADR-0015 i plan V1**.
  Gdy ADR-0015 / plan V1 są sprzeczne z `README.md` lub runbookami V0, rozbieżność
  jest **oczekiwana** w trakcie implementacji: README/runbooki opisują dostarczone
  V0 do release; nie traktuj ich jako stop-the-work konfliktu blokującego taski V1.
  Zakaz cichej zmiany ADR-ów (sekcja 9) dotyczy nowych decyzji — ADR-0015 jest
  jawnym, superseding zapisem granic V1.

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

### Torio jest ownerem

- Lima lifecycle i provisioning,
- project registry i admission control,
- effective policy,
- execution specification,
- per-task isolation guarantees,
- security-sensitive Git operations,
- fresh sandboxed verification,
- review evidence,
- approval, integration i push.

### Torio NIE MOŻE implementować

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
- SQLite Torio jest policy/evidence ledgerem, nie queue.
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
