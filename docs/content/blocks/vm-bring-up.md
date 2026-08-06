## Start and verify the VM {#vm-bring-up}

Create the VM from the trusted template, start it, then reconcile and verify it.
Run all three in order: each is idempotent, so this is also the sequence you
re-run later.

```bash
torio vm init
torio vm start
torio vm bootstrap --timeout 10m
```

`init` prints `next: torio vm start` whether it created the instance or found a
compatible one, so there is no state in which you skip the second command.

`init` creates the pinned Lima instance (or succeeds idempotently when a
compatible one already exists) and verifies the post-create list output before
reporting success. `start` is idempotent and confirms a `Running` post-state.
`bootstrap` operates only on the existing target after a verified `Running`
precondition, through the typed Lima boundary. Hermes Agent install can be slow,
so give it room — but `10m` is the policy maximum for any single operation, and
asking for more is refused before any work starts:

```text
torio: timeout 15m0s exceeds policy maximum 10m0s
```

On a fully-reconciled target
it mutates nothing; when the pinned launcher is missing it installs Hermes Agent
at the pinned commit, then reconciles the PATH shim. It:

- installs the pinned Hermes Agent when `/home/hermes/hermes-agent/venv/bin/hermes` is missing (never curl|bash pipe — download to a hermes-writable path, run with fixed flags, verify git HEAD);
- ensures `/usr/local/bin/hermes` is a symlink to the pinned launcher (only after confirming the launcher exists);
- **verifies** (not merely trusts an exit code): the `hermes` user exists; group `torio-projects` exists; `hermes` and the Lima login operator are members; `hermes` is **not** in the `docker` group (rootful Docker for hermes is forbidden); `uname -m` matches the host profile's guest architecture; `hermes --version` through the documented stable command path; `git --version`; the persistent profile, Second Brain, and workspace paths are directories with the expected owner, group, and mode on native Linux (ext4), not a host share; and no broad host mount is present.

Any drift or unverifiable state fails closed (exit 6) with remediation. A rerun
is success only when every postcondition is proven. Use `--json` for the
machine-readable envelope (one document on stdout).

### Persistent Hermes locations {#persistent-locations}

| What | Path (on the VM's native Linux filesystem) |
| --- | --- |
| Guest user | `hermes` |
| Hermes home | `/home/hermes` |
| Profile / application state | `/home/hermes/.hermes` |
| Second Brain vault | `/home/hermes/brain` |
| Workspace root | `/home/hermes/projects` |

These are also emitted in the `torio vm bootstrap` output (human and `--json`).

`init` creates the VM; `bootstrap` then verifies the guest layout above on an
already-running instance. Neither recreates or re-images it.
