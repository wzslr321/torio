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

- Pre-V1 eksploracja (platforma workerów, admission control, per-task isolation,
  fresh verifier, approval/integration/push, staged roadmap Demo A / Demo B)
  **nie jest już w drzewie roboczym**. Leży pod tagiem `archive/pre-v1` —
  [ADR-0017](docs/adr/0017-pre-v1-exploration-leaves-the-working-tree.md).
  Nie reaktywuj jej i nie traktuj jako następnego taska.
- **Precedens dla pracy implementacyjnej Torio V1:** gdy cokolwiek jest sprzeczne
  z ADR-0015 albo planem V1, autorytetem są **ADR-0015 i plan V1**. Gdy ADR-0015 /
  plan V1 są sprzeczne z `README.md` lub runbookami V0, rozbieżność jest
  **oczekiwana** w trakcie implementacji: README/runbooki opisują dostarczone V0
  do release; nie traktuj ich jako stop-the-work konfliktu blokującego taski V1.
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
3. Kontrakty w `docs/contracts/`.
4. [`docs/03-architecture.md`](docs/03-architecture.md).
5. Plan V1 w `.hermes/plans/`.

Aktualna dokumentacja i kod Hermes Agent są źródłem prawdy o Hermesie. Nie używaj pamięci modelu do odgadywania komend, portów, opcji, paths ani lifecycle.

## 4. Niezmienne granice

Zakres i uzasadnienie: [ADR-0015](docs/adr/0015-torio-v1-onboarding-projects-and-operator-push.md)
oraz [`docs/03-architecture.md`](docs/03-architecture.md).

### Hermes Agent jest ownerem

- model execution,
- profili, sesji i pamięci,
- rejestru projektów po stronie agenta,
- Kanbana, dispatchu i retry.

### Torio jest ownerem

- Lima lifecycle, provisioning i weryfikacji gościa,
- niesekretnej deklaracji podpiętych projektów (`config.json` V2),
- wyprowadzania ścieżek workspace'ów i vaulta,
- krótkotrwałej sesji operatora, która jest jedynym nośnikiem write capability.

### Torio NIE MOŻE implementować

- alternatywnego agent loop ani drugiego Kanbana,
- własnego dispatchera, queue lub retry engine,
- autonomicznego merge/push/release,
- per-task workerów ani verifier platformy,
- secret managera klasy Vault ani domenowego network allowlistu.

## 5. Security invariants

Każda implementacja MUSI zachować:

1. Repozytoria, Brain i state leżą na natywnym filesystemie VM, nigdy na szerokim mountcie macOS.
2. Profil Hermesa nie jest sandboxem; granicą jest brzeg VM.
3. Tożsamość serwisowa `hermes` NIE MOŻE należeć do grupy `docker` ani mieć dostępu do `docker.sock`.
4. `/home/hermes/.hermes` (profil) i `/home/hermes/brain` (vault) są rozróżniane w kodzie i docs.
5. Workspace path jest wyprowadzany z id projektu, nigdy podawany przez użytkownika.
6. Git remote NIE MOŻE zawierać hasła, tokenu, query ani fragmentu.
7. Persistentny `hermes` ma do origin wyłącznie read; `ssh.forwardAgent` jest globalnie wyłączone.
8. Write capability pochodzi wyłącznie z sesji `torio project shell` i kończy się razem z nią.
9. Guest helper sesji operatora jest `root:root 0755`; drift jest raportowany, nie naprawiany.
10. Push, merge i release są osobnymi, human-only operacjami poza CLI.
11. Transport Braina jest jednorazowy i ograniczony; treść payloadu nie trafia do stdout, logów ani evidence.
12. Brain nie jest wstrzykiwany do promptu — dostęp cross-project idzie przez retrieval skill.

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
