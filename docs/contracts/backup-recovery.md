# Backup and recovery contract

## Zakres

Pełny backup Hermes Box musi objąć osobno:

1. Hermes state — przez zweryfikowane `hermes backup`/profile export mechanizmy.
2. Hermes Box config i `hb.db` — spójny SQLite snapshot.
3. Git repositories — remote-backed lub osobny filesystem backup.
4. Review refs/artifacts — tak, aby zatwierdzone OID-y pozostały osiągalne.
5. Lima template i version lock — bez sekretów.

`hermes profile export` nie jest kompletnym backupem całego Hermes Box i nie obejmuje automatycznie repozytoriów ani `hb.db`.

## PoC

Backup nie jest warunkiem Demo A/B, ale state layout nie może go uniemożliwiać. Wszystkie state paths są jawne i znajdują się poza task worktrees.

## Recovery invariant

Po restore:

- żaden approval nie staje się valid, jeśli brakuje exact Git objects/evidence,
- running executions wracają jako `RECONCILIATION_REQUIRED`, nie `RUNNING`,
- push credentials są provisionowane osobno i nie znajdują się w archive,
- restore nie uruchamia automatycznie workerów ani push.
