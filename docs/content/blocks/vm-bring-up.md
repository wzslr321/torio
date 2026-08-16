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
precondition, through the typed Lima boundary. A backend install can be slow,
so give it room — but `10m` is the policy maximum for any single operation, and
asking for more is refused before any work starts:

```text
torio: timeout 15m0s exceeds policy maximum 10m0s
```

On a fully-reconciled target
it mutates nothing; when the pinned binary is missing it installs the declared
backend at its pin. It:

- installs the backend at its pinned version when it is missing, verifying the download against a checksum this repository commits — never a `curl | bash` pipe;
- **verifies** (not merely trusts an exit code): the agent's guest user exists; group `torio-projects` exists; the agent and the Lima login operator are members; the agent is **not** in the `docker` group (rootful Docker for the agent is forbidden); `uname -m` matches the host profile's guest architecture; the backend's own version command through the documented stable command path; `git --version`; the persistent profile, Second Brain, and workspace paths are directories with the expected owner, group, and mode on native Linux (ext4), not a host share; and no broad host mount is present.

Any drift or unverifiable state fails closed (exit 6) with remediation. A rerun
is success only when every postcondition is proven. Use `--json` for the
machine-readable envelope (one document on stdout).

### Persistent guest locations {#persistent-locations}

The table below is the Claude Code layout; every backend has the same shape
under its own identity.

| What | Path (on the VM's native Linux filesystem) |
| --- | --- |
| Guest user | `claude` |
| Home | `/home/claude` |
| Profile / application state | `/home/claude/.claude` |
| Second Brain vault | `/home/claude/brain` |
| Workspace root | `/home/claude/projects` |

These are also emitted in the `torio vm bootstrap` output (human and `--json`).

`init` creates the VM; `bootstrap` then verifies the guest layout above on an
already-running instance. Neither recreates or re-images it.
