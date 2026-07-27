<!--
AI-Provenance:
  model: Claude Opus 5
  harness: Claude Code
-->

# ADR-0016: Dokumenty normatywne są poprawiane, a nie archiwizowane

- Status: Accepted
- Date: 2026-07-27
- Supersedes: klauzula archiwalna [ADR-0014](0014-rename-to-torio.md) **wyłącznie w części
  dotyczącej `docs/contracts/`**. Reszta tej klauzuli — `docs/plans/`, ADR-y 0001–0013,
  `docs/0*–1*.md`, `prompts/`, `spikes/`, `docs/spike-results/` — pozostaje w mocy. Treść
  ADR-0014 nie jest przepisywana (AGENTS.md §9).

## Context

ADR-0014 zaliczył `docs/contracts/` do materiału archiwalnego, który zachowuje historyczne
`hb` / `hermes-box`. Uzasadnieniem była wierność evidence: „reviewer musi odtworzyć zapisany
przebieg”. Dla spike-resultów i promptów to jest słuszne — zapis przebiegu, który został
zmieniony po fakcie, przestaje być dowodem.

Kontrakty nie są jednak evidence. `AGENTS.md` §3 stawia `docs/contracts/` na **trzecim**
miejscu w kolejności autorytetu, powyżej planu etapu. Dokument z tego poziomu nie opisuje
przeszłego przebiegu, tylko mówi implementerowi, co ma zbudować.

Rozjazd przestał być teoretyczny. `docs/contracts/cli.md` opisywał `bootstrap` jako
komendę, która „może zapewnić membership `hermes` w grupie `docker`” i weryfikuje
osiągalność Dockera dla tej tożsamości. Kod robi dokładnie odwrotnie:
`internal/lima/bootstrap.go` ma `verifyHermesNotInDocker`, który zawodzi closed, a
`internal/lima/templates/torio.yaml` aktywnie usuwa `hermes` z grupy `docker`
(`gpasswd -d hermes docker`). Członkostwo w grupie `docker` jest root-equivalent, a
[ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md) go zakazuje.

Implementer czytający kolejność autorytetu dosłownie mógł więc „przywrócić zgodność
z kontraktem” i nadać tożsamości agenta uprawnienia root-equivalent. Klauzula precedensu
z `AGENTS.md` ratuje sytuację tylko wtedy, gdy czytający ją zauważy. Bezpieczeństwo nie
może zależeć od tego, czy ktoś doczytał przypis.

## Decision

- Dokumenty **normatywne** — `docs/contracts/` i `schemas/` — są poprawiane, gdy rozjeżdżają
  się z dostarczonym zachowaniem albo z przyjętym ADR-em. Nie są materiałem archiwalnym.
- Materiał **archiwalny** pozostaje dosłowny: `docs/spike-results/`, `spikes/`, pliki
  evidence, ADR-y 0001–0015, `prompts/`, `docs/plans/`. Evidence musi zostać odtwarzalne.
- Gdy kontrakt nadal opisuje superseded design, dostaje **jawną notę statusu** nazywającą,
  która część jest legacy. Nie usuwamy historycznego uzasadnienia po to, żeby dokument
  wyglądał spójnie, i nie zostawiamy go po cichu błędnym.
- Nazwy własne wewnątrz superseded designu (np. plik `hb.db` w zarchiwizowanym ledgerze)
  nie są nazwą binarki i nie podlegają tej korekcie.
- ADR-y nadal nie są przepisywane po fakcie. Zmiana decyzji wymaga kolejnego ADR-u.

## Consequences

- `docs/contracts/cli.md`, `service-lifecycle.md` i `state-ledger.md` używają nazwy `torio`.
- Zapis o grupie `docker` w `cli.md` jest zastąpiony tym, co kod egzekwuje: `hermes` **NIE
  MOŻE** należeć do grupy `docker`, a bootstrap weryfikuje nieobecność tego członkostwa.
- `cli.md` dostaje notę statusu wskazującą komendy, których nie ma w dostarczonym CLI
  (`doctor`, `status`, `reconcile`, `gateway`, `task`, `admin`).
- Rozjazd między kontraktem a kodem staje się defektem do naprawy, a nie zaakceptowanym
  stanem — dotyczy to także przyszłych kontraktów dla `brain` i `project`.

## Rejected

- **Zostawić kontrakt błędnym i polegać na klauzuli precedensu ADR-0015.** Działa tylko dla
  czytelnika, który ją zauważy; wymóg bezpieczeństwa nie może zależeć od uważności.
- **Usunąć superseded treść kontraktów.** Traci historię projektową, którą repozytorium
  świadomie zachowuje.
- **Przepisać ADR-0014.** Zakazane przez `AGENTS.md` §9 — nowa decyzja wymaga nowego ADR-u.
- **Rozszerzyć korektę na `docs/spike-results/`.** Zniszczyłoby wierność evidence, czyli
  dokładnie to, co ADR-0014 słusznie chronił.
