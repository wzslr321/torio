# Zadanie: następny pionowy slice Demo A

Przeczytaj AGENTS.md, docs/plans/02-demo-a.md, `.hermes/plans/2026-07-23_172055-hermes-box.md` i wszystkie ADR-y/contracts relewantne dla pierwszego niezakończonego slice D1–D8.

Prerequisite:
- `docs/spike-results/99-decision.md` musi istnieć i zawierać `Demo A: GO`.
- Jeśli nie istnieje albo ma NO-GO/BLOCKED, zatrzymaj się i zgłoś precondition; nie implementuj.

Wybierz dokładnie pierwszy niezakończony slice D1–D8, którego dependencies są spełnione. Nie realizuj kolejnego slice w tej samej sesji.

Przed kodem wypisz:
- slice ID i expected behavior,
- in/out of scope,
- acceptance tests,
- relewantne security invariants.

Pracuj TDD: failing test → minimal code → green → refactor. Nie twórz pustych stubów dla przyszłych commandów. Wszystkie external commands idą przez testowalny typed runner z argument arrays i timeoutem.

Po implementacji uruchom:
```bash
python3 scripts/validate_artifacts.py
go test ./...
go vet ./...
```
Oraz realny acceptance test, jeśli slice dotyczy Lima/Hermes/service.

Zaktualizuj plan checkbox tylko po realnym PASS. Final response ma zawierać commands, exit codes, changed files, test evidence, security impact, blockers i exact next slice.
