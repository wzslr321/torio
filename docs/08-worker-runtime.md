# Worker runtime specification

## Dwie warstwy

- **Hermes Worker Runtime** jest zaufanym procesem modelowym/dispatchowym w T1.
- **Workload Container** jest niezaufanym miejscem file/terminal/candidate execution.

Nie nazywaj obu „workerem” w kodzie; używaj `runtime` i `workload`.

## Demo B workload profile

```text
fresh per task
image pinned by digest
network none
workspace rw
input ro
output rw
no usable .git
no Docker socket/CLI/group
no host credentials
no broad home mount
no cross-process persistence
resource/time limits
```

## Tool surface

Model może używać tylko narzędzi potrzebnych do edycji/testów i lifecycle taska. Host-side web, browser, messaging, SaaS, memory/session search, admin i unrelated MCP są wyłączone.

## Skills

Skill jest capability-bearing code/config. Resolver sprawdza deklarowane env i credential files przed execution. Unknown metadata jest deny. Pusta `allowed_skills` jest prawidłowym defaultem.

## Workspace

Control plane przygotowuje tree. Workload nie dostaje Git authority. Jeśli narzędzie próbuje `git`, oczekiwanym zachowaniem jest kontrolowana porażka, nie mount głównego `.git`.

## Completion

Worker report jest untrusted. Kanban completion kończy attempt, ale nie nadaje statusu verified/approved/integrated. Control plane potwierdza stop i dopiero potem tworzy candidate.
