# Zadanie: niezależny code review

Przeczytaj AGENTS.md, relewantny plan, ADR-y i contracts. Przejrzyj wyłącznie diff aktualnego branchu względem jego trusted base.

Priorytety:
1. Naruszenie security invariant lub fail-open.
2. Duplikacja odpowiedzialności Hermesa.
3. TOCTOU, stale-base i idempotency.
4. Command injection/path traversal/symlink escape.
5. Secret leakage w env/logach/JSON/errors.
6. Candidate execution na hoście.
7. SQLite transactions/concurrency/recovery.
8. Test, który nie dowodzi deklarowanego enforcementu.
9. Niepotrzebny scope/abstraction.

Dla każdego findingu podaj:
- severity: BLOCKER/HIGH/MEDIUM/LOW,
- plik i linie,
- naruszony contract/invariant,
- konkretny scenariusz awarii,
- minimalną poprawkę,
- wymagany test.

Nie komentuj stylu, jeśli nie wpływa na correctness/maintainability. Jeśli nie ma findingów, napisz to jawnie oraz podaj residual risks i testy, których nie dało się uruchomić.
