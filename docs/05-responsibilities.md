# Responsibility matrix

| Capability | Hermes | Hermes Box | Git/Docker | Human |
|---|---:|---:|---:|---:|
| Model conversation | owner | — | — | user |
| Memory/sessions/profiles | owner | policy constraints | storage only | configures |
| Messaging gateway | owner | lifecycle wrapper/doctor | — | configures |
| Kanban queue/dispatch/retry | owner | admission binding only | — | observes |
| Project registry | — | owner | repo identity | approves |
| Effective policy | input constraints | owner | — | approves registry |
| Worker process | owner | execution spec | Docker executes workload | — |
| Task container | backend adapter | policy/lifecycle owner | Docker resource | — |
| Worktree/checkout | workspace metadata | security-sensitive adapter | Git owner danych | — |
| Candidate report | worker output | untrusted evidence input | — | reads |
| Review commit/tree | — | creates and records | Git content-addressed objects | reviews |
| Verification | worker may report | owner orchestration | fresh verifier executes | reviews evidence |
| Approval | — | records/validates | — | sole authority |
| Integration | — | enforces exact fast-forward | Git applies | authorizes |
| Push | — | enforces separate capability | Git remote | authorizes |
| VM lifecycle | — | owner | Lima executes | starts/stops |
| Backup/recovery | native Hermes backup input | coordinated owner | filesystem/archive | operates |

## Zakaz podwójnego ownershipu

Jeśli Hermes już realizuje queue, dispatch, retry lub heartbeat, `hb` może je obserwować i korelować, ale nie zapisuje drugiej konkurencyjnej wersji tego stanu.

Jeśli operacja zmienia security artifact — policy, executor spec, Git candidate, verification, approval, integration lub push — ownerem jest `hb`, nawet jeśli wykorzystuje natywną komendę Git/Docker/Hermes.

## Semantic mapping

Hermes Kanban `done` oznacza:

> Worker zakończył daną próbę i wyprodukował wynik/candidate.

Nie oznacza:

- verified,
- approved,
- integrated,
- pushed.

Te fazy są wyliczane z immutable facts w `hb.db`.
