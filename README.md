# Hermes Box

> Reprodukowalne i kontrolowane środowisko dla Hermes Agent na Macach Apple Silicon.

**Status:** pakiet projektowy gotowy do implementacji. Kod produkcyjny nie został jeszcze rozpoczęty. Pierwszym etapem jest obowiązkowy spike weryfikujący aktualne zachowanie Hermesa, Lima, Git worktrees i Docker backendu.

## Cel

Hermes Box łączy cztery role bez budowania drugiego frameworka agentowego:

1. **Hermes Brain** — rozmowa, pamięć, planowanie i zgłaszanie pracy.
2. **Hermes Kanban** — trwała kolejka, dispatcher, retries, heartbeats i procesy workerów.
3. **Hermes Box Control Plane** — admission, policy, izolacja wykonania, Git, verification, approval, integration i push.
4. **Człowiek** — jedyna władza zatwierdzająca, integrująca i wypychająca zmiany.

Najważniejszy invariant:

> Brain planuje i zgłasza. Hermes dispatchuje. Hermes Box egzekwuje granice. Worker produkuje kandydat. Człowiek zatwierdza dokładnie ten artefakt, który zostaje zintegrowany.

## Zacznij tutaj

Czytaj w tej kolejności:

1. [`AGENTS.md`](AGENTS.md) — normatywne zasady dla LLM-a i developera.
2. [`docs/01-product-brief.md`](docs/01-product-brief.md) — problem, użytkownik i sukces.
3. [`docs/02-scope.md`](docs/02-scope.md) — zakres oraz zakazy.
4. [`docs/03-architecture.md`](docs/03-architecture.md) — architektura i przepływy.
5. [`docs/04-threat-model.md`](docs/04-threat-model.md) — model zagrożeń.
6. [`docs/05-responsibilities.md`](docs/05-responsibilities.md) — granice odpowiedzialności.
7. [`docs/contracts/`](docs/contracts/) — CLI, konfiguracja, policy, state i evidence.
8. [`docs/adr/`](docs/adr/) — zatwierdzone decyzje architektoniczne.
9. [`docs/plans/00-roadmap.md`](docs/plans/00-roadmap.md) — kolejność etapów.
10. [`prompts/`](prompts/) — samodzielne prompty do pracy z LLM-em.

## Lokalny start z LLM-em

```bash
# Po rozpakowaniu paczki
cd hermes-box
python3 scripts/validate_artifacts.py

# Następnie uruchom wybranego agenta w katalogu repo.
# AGENTS.md zostanie automatycznie wykryty przez Hermes/Codex/Claude Code.
```

Pierwszy prompt:

```text
Przeczytaj AGENTS.md oraz prompts/01-spike.md. Nie implementuj Demo A ani Demo B.
Zrealizuj wyłącznie spike zgodnie z docs/plans/01-spike.md, zapisując rzeczywiste
wyniki w docs/spike-results/. Nie zgaduj zachowania Hermesa.
```

## Etapy

| Etap | Wynik | Automatyczne coding tasks |
|---|---|---:|
| 0. Spike | Zweryfikowane kontrakty runtime | Nie |
| 1. Demo A | Desktop ↔ Lima ↔ Hermes Brain | Nie |
| 2. Demo B | Jeden bezpieczny coding worker | Tak, jeden projekt |
| 3. Hardening | Silniejsza separacja procesów i executorów | Tak |
| 4. Company-ready | Multi-project, backup, audit, policy governance | Tak |

## Zasady bezpieczeństwa PoC

- Lima jest granicą między agentem a macOS.
- Profile Hermesa **nie są sandboxem**.
- Workload container jest świeży per task.
- Worker nie dostaje Docker socketa, Git metadata, push credentials ani pamięci Braina.
- `network none` obejmuje kontener; host-side web/MCP/messaging tools są osobno zabronione.
- Załadowane skills są częścią policy, ponieważ mogą forwardować env i credential files.
- Worker jest zatrzymywany przed snapshotem.
- Exact snapshot jest weryfikowany w świeżym verifier containerze.
- Approval wiąże base commit, review commit/tree, policy hash i verification evidence.
- Integracja PoC jest wyłącznie fast-forward i odmawia po zmianie target base.
- Push jest osobną operacją wymagającą człowieka.

## Stos

- Host: macOS na Apple Silicon.
- VM: Lima 2.x, Linux arm64.
- Runtime: Hermes Agent, wersja przypięta po spike'u.
- Kontenery: Docker Engine w VM; natywny Docker backend Hermesa w Demo B.
- Control plane: Go 1.26.x, pojedynczy binarny `hb`.
- State: SQLite jako policy/evidence ledger; Hermes Kanban pozostaje task engine.
- Git: thin trusted adapter i content-addressed review artifacts.

## Walidacja pakietu

```bash
python3 scripts/validate_artifacts.py
```

Walidator sprawdza schematy JSON, przykłady, linki względne, placeholdery sekretów i obecność obowiązkowych dokumentów.

## Źródła

Stan Hermesa został zweryfikowany 2026-07-23 względem oficjalnego repozytorium przy commitcie `d9165d7a678d4105f42921a7fc1886df3804531b`. Przed implementacją należy powtórzyć weryfikację zgodnie z [`docs/07-source-verification.md`](docs/07-source-verification.md).
