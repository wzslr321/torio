<!--
AI-Provenance:
  model: unknown
  harness: Cursor
-->

# ADR-0014: Produkt, CLI i moduł nazywają się `torio`

- Status: Accepted
- Date: 2026-07-27
- Supersedes: [ADR-0001](0001-go-control-plane.md) w zakresie nazwy modułu Go (`hermes-box.local/hb`); reszta ADR-0001 obowiązuje bez zmian.

## Context

Nazwa „Hermes Box" była nazwą roboczą i myli się z samym Hermes Agentem, którego
ten control plane tylko obsługuje. Dokumentacja produktu (`README.md`, strona)
mówiła już „Torio", podczas gdy kod, binarka, moduł, katalog configu i instancja
Limy nadal nazywały się `hb` / `hermes-box`. Rozjazd był widoczny dla operatora
w każdej komendzie.

ADR-0001 ustalił nazwę modułu jako lokalną (`hermes-box.local/hb`) „do czasu
wyboru publicznego hostingu". Hosting został wybrany, więc warunek z ADR-0001
przestał obowiązywać.

## Decision

Jedna nazwa w całym produkcie: **`torio`**.

| Element | Było | Jest |
| --- | --- | --- |
| Binarka i komenda | `hb` | `torio` |
| Ścieżka pakietu main | `cmd/hb` | `cmd/torio` |
| Moduł Go | `hermes-box.local/hb` | `github.com/wzslr321/torio` |
| Katalog config/state (XDG) | `hermes-box/` | `torio/` |
| Instancja Limy (`lima.InstanceName`) | `hermes-box` | `torio` |
| `$id` schematów JSON | `https://hermes-box.local/schemas/…` | `https://torio.dev/schemas/…` |
| Szablony | `templates/{config,lima}/hermes-box.*` | `templates/{config,lima}/torio.*` |

Nazwa unitu systemd gościa pozostaje `hermes-serve.service`: uruchamia
`hermes serve` i opisuje proces Hermesa, nie control plane.

Materiał archiwalny **nie jest** przepisywany: `docs/plans/`, `docs/adr/`
0001–0013, `docs/contracts/`, `docs/0*–1*.md`, `prompts/`, `spikes/` oraz
`docs/spike-results/` zachowują historyczne `hb` / `hermes-box`. Evidence musi
zostać dosłowne, żeby reviewer mógł odtworzyć zapisany przebieg, a ADR-u nie
zmienia się po fakcie (AGENTS.md §9).

## Consequences

- Instancja Limy o starej nazwie przestaje być widoczna dla CLI. `torio vm
  status` szuka instancji `torio`; VM `hermes-box` trzeba odtworzyć pod nową
  nazwą. Lima nie umie przemianować instancji w miejscu, a `vm init` nie jest
  jeszcze zaimplementowane, więc utworzenie instancji pozostaje krokiem
  operatora.
- Katalog configu zmienia lokalizację. Istniejący `~/.config/hermes-box/`
  trzeba przenieść do `~/.config/torio/` albo utworzyć od nowa; CLI go nie
  migruje i nie zgaduje starej ścieżki.
- Opis generowanego unitu zmienia się na `Torio loopback backend (torio
  serve)`. Jest to pole opisowe, nie identyfikator, więc `serve install`
  nadpisuje je idempotentnie.
- Import path zmienia się dla całego drzewa `internal/`. Zmiana jest
  mechaniczna i pokryta istniejącymi testami pakietów.
- Zmiana `$id` schematów jest zmianą identyfikatorów kontraktu. Żaden `$ref`
  ani walidator ich nie rozwiązuje po URL-u (`scripts/validate_artifacts.py`
  czyta pliki lokalnie), więc nie ma konsumenta, któremu by się to zepsuło.
