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
