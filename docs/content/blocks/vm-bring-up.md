## Start and verify the VM {#vm-bring-up}

If the VM does not exist yet, create it from the trusted template:

```bash
torio vm init
```

If `torio vm status` did not report `Running`, start the VM, then reconcile and
verify it:

```bash
torio vm start
torio vm bootstrap --timeout 15m
```

`init` creates the Gate-0-pinned Lima instance (or succeeds idempotently when a
compatible one already exists) and verifies the post-create list output before
reporting success. `start` is idempotent and confirms a `Running` post-state.
`bootstrap` operates only on the existing target after a verified `Running`
precondition, through the typed Lima boundary. Hermes Agent install can be slow —
use an ample timeout (for example `--timeout 15m`). On a fully-reconciled target
it mutates nothing; when the pinned launcher is missing it installs Hermes Agent
at the Gate-0 commit (verifiable postcondition: git HEAD pin + launcher path),
then reconciles the PATH shim. It:

- installs the pinned Hermes Agent when `/home/hermes/hermes-agent/venv/bin/hermes` is missing (never curl|bash pipe — download to a hermes-writable path, run with fixed flags, verify git HEAD);
- ensures `/usr/local/bin/hermes` is a symlink to the pinned launcher (only after confirming the launcher exists);
- **verifies** (not merely trusts an exit code): the `hermes` user exists; group `torio-projects` exists; `hermes` and the Lima login operator are members; `hermes` is **not** in the `docker` group (rootful Docker for hermes is forbidden); `uname -m == aarch64`; `hermes --version` through the documented stable command path; `git --version`; the persistent profile, Second Brain, and workspace paths are directories with the expected owner, group, and mode on native Linux (ext4), not a host share; and no broad macOS host mount is present.

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

V1 can create the VM via `torio vm init`; bootstrap then verifies the guest layout
above on an already-running instance. It never recreates or re-images the VM.
