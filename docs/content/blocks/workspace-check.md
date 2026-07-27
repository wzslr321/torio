## Run one documented, non-destructive check {#workspace-check}

Select exactly **one** command from the repository's own contributor
documentation. For this repository the local-checks doc requires only Python, and
the CI "validate" job runs a generated-artifact **sync check** in dry-run
(`--check`) mode: it reads sources, recomputes the generated outputs in memory,
and compares. It performs **no** file writes, **no** network access, and has
**no** deploy, release, push, or data-destruction effect.

Run it as `hermes`, then confirm the tree stayed clean:

```bash
torio vm ssh -- sudo -u hermes -- python3 <repo>/scripts/<documented-check> --check
torio vm ssh -- sudo -u hermes -- git -C /home/hermes/projects/REDACTED-PROJECT status --porcelain
```

The second command must print nothing. Do not invent an install or test command;
Go-based CI steps are not run here, matching the repository's own note that
contributors only need Python locally.
