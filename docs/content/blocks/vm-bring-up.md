## Start and verify the VM {#vm-bring-up}

If `torio vm status` did not report `Running`, start the VM, then reconcile and
verify it:

```bash
torio vm start
torio vm bootstrap --timeout 5m
```

`start` is idempotent and confirms a `Running` post-state before reporting
success. `bootstrap` operates only on the existing target after a verified
`Running` precondition, through the typed Lima boundary. It is idempotent and,
on a fully-reconciled target, mutates nothing. It:

- ensures the intended non-root guest user `hermes` is in the `docker` group;
- ensures `/usr/local/bin/hermes` is a symlink to the pinned launcher (only after confirming the launcher exists — a missing launcher is reported as drift, never a dangling shim);
- **verifies** (not merely trusts an exit code): `uname -m == aarch64`; `hermes --version` through the documented stable command path; the Docker server is reachable by the `hermes` identity; `git --version`; the persistent knowledge-base and workspace paths are directories on native Linux (ext4), not a host share; and no broad macOS host mount is present.

Any drift or unverifiable state fails closed (exit 6) with remediation. A rerun
is success only when every postcondition is proven. Use `--json` for the
machine-readable envelope (one document on stdout).

### Persistent Hermes locations {#persistent-locations}

| What | Path (on the VM's native Linux filesystem) |
| --- | --- |
| Guest user | `hermes` |
| Hermes home | `/home/hermes` |
| Knowledge base / profile | `/home/hermes/.hermes` |
| Workspace root | `/home/hermes/projects` |

These are also emitted in the `torio vm bootstrap` output (human and `--json`).
