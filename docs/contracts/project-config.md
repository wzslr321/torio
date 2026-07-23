# Project registry contract

## Trust

Project config jest przechowywany poza repo taska, domyślnie:

```text
~/.config/hermes-box/projects.d/<project-id>.yaml
```

Nie zawiera sekretów. Plik jest kontrolowany przez admina i walidowany przez [`project.schema.json`](../../schemas/project.schema.json).

Task branch może zawierać sugestie developerskie, ale nie może rozszerzyć aktywnej policy, verification command, image, mount, tool, skill, network ani integration mode.

## Kluczowe pola

- `project_id` — stabilny slug.
- `repo.local_path` — absolute path na Linux filesystemie VM.
- `repo.default_branch` — target branch.
- `hermes.board` — board przeznaczony dla projektu.
- `hermes.worker_profile` — minimalny profil worker runtime.
- `worker.image` — image reference z digestem `@sha256:`.
- `worker.network` — `none` w Demo B.
- `worker.allowed_toolsets` i `allowed_skills` — jawne allowlisty.
- `verification.commands` — argv arrays z trusted config; nigdy shell strings.
- `integration.mode` — `fast-forward-only` w PoC.

## Normalizacja

Przed hashingiem control plane:

1. parsuje YAML,
2. waliduje schema,
3. rozwiązuje absolute/canonical paths bez wychodzenia poza trusted roots,
4. sortuje set-like arrays,
5. materializuje defaulty,
6. serializuje canonical JSON,
7. liczy SHA-256.

Hash raw YAML nie jest effective policy hash.
