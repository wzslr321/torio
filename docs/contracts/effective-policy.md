# Effective policy contract

## Zasada

Effective policy jest pełnym, immutable snapshotem uprawnień jednego execution. Nie jest wskaźnikiem do zmiennego configu.

Policy musi objąć:

- project/base identity,
- worker i verifier image digests,
- workspace mounty i Git metadata mode,
- container network,
- host-side toolsets,
- skills,
- explicit oraz implicit env/credential passthrough,
- resource limits,
- verification commands,
- integration/push rules.

## Default deny

Unknown tool, skill, mount, env, credential file, network mode lub extra container arg jest zabroniony.

W Demo B:

```text
network = none
credentials = []
env_allowlist = []
skills_allowlist = [] (lub minimalne zweryfikowane)
git_metadata = denied
container_persistent = false
persist_across_processes = false
integration = fast-forward-only
push = explicit-human-only
```

## Workspace (materialized Git-free tree)

Zgodnie z [ADR-0011](../adr/0011-materialized-git-free-workspaces.md) workspace workera nie jest
Git worktree. Effective policy MUSI zapisać:

```text
workspace.kind                          = materialized-tree
workspace.mount                         = /workspace
workspace.git_metadata                  = denied
workspace.repository_ancestor_reachable = false
workspace.git_ceiling_directories       = ["/workspace"]
workspace.extra_mounts                  = []          (default deny)
```

`git_metadata = denied` to ta sama wartość kontraktowa co dotychczas (handoff nazywa ją „deny”;
zachowano `denied`, by nie zmieniać przyjętej wartości). `git_ceiling_directories` jest
defense-in-depth — podstawową granicą jest brak osiągalnego repo-przodka
(`repository_ancestor_reachable = false`), nie ceiling.

## Executor freshness

Per-execution knoby wykonawcy (obiekt `worker` w schemacie — handoff nazywa je `executor.*`):

```text
worker.fresh_per_task            = true
worker.persist_across_processes  = false
```

Świeży workload per task jest wymagany (security invariant #3); cross-process container reuse jest
zabroniony (domyślnie w źródle Hermesa reuse jest ON — musi zostać wymuszony na OFF).

## Dev Container metadata

Task branch `devcontainer.json` nie jest policy source. PoC używa prebuilt image pinned by digest. Późniejszy adapter może zaakceptować tylko zatwierdzony subset z trusted base/registry i musi odrzucać co najmniej:

- `initializeCommand`,
- arbitrary lifecycle commands,
- `privileged`,
- Docker socket,
- arbitrary mounts,
- host network,
- extra capabilities/security options,
- Features bez pinned integrity.
