# Zadanie: następny pionowy slice Demo B

Przeczytaj AGENTS.md, docs/04-threat-model.md, docs/plans/03-demo-b.md, docs/contracts/ i ADR-0002/0004/0005/0006/0007/0008/0010.

Prerequisites:
- Demo A musi mieć PASS evidence.
- `docs/spike-results/99-decision.md` musi zawierać `Demo B native Docker: GO`.
- Jeśli nie, zatrzymaj się fail closed.

Wybierz dokładnie pierwszy niezakończony slice B1–B13 ze spełnionymi dependencies. Nie buduj future abstractions i nie przechodź do kolejnego slice.

Dla wybranego slice:
1. Zdefiniuj jedno end-to-end behavior i jego negatywny security test.
2. Napisz failing test i pokaż oczekiwaną porażkę.
3. Zaimplementuj minimum.
4. Uruchom green/regression/race tam, gdzie dotyczy state/concurrency.
5. Zweryfikuj effective behavior, nie tylko serialized config.
6. Nie wykonuj candidate code na VM hoście.
7. Nie zapisuj do Kanban DB bezpośrednio.
8. Nie dawaj workerowi używalnego Git metadata, host web/MCP ani implicit credentials.
9. Każda operacja mutująca musi mieć idempotency/precondition tests.

Po zmianie:
```bash
python3 scripts/validate_artifacts.py
go test ./...
go test -race ./...
go vet ./...
```

Final response: slice, behavior, RED evidence, GREEN evidence, negative security proof, changed files, contract impact, open risk i exact next slice.
