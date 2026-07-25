# Runbook — Remote Second Brain V1 (controlled dogfood)

Status: **controlled Remote Second Brain V1 path ready for operator use; formal Demo A remains pending.**

This runbook drives the existing, already-created `hermes-box` Lima VM through a
reproducible **start → bootstrap/verify → connect** path. It is deliberately
narrow: it does not create, recreate, or re-image the VM, install a model or
provider, accept secrets, or create gateway/serve services. It does not claim
S1–S8 are solved.

## Prerequisites

- macOS host (Apple Silicon) with `limactl` on `PATH`.
- The `hermes-box` VM already created (this runbook never creates it).
- The `hb` binary built from this repository: `go build -o hb ./cmd/hb`.

## 1. Start

```text
hb vm start
hb vm status
```

`start` is idempotent and confirms a `Running` post-state before reporting
success.

## 2. Bootstrap / verify

```text
hb vm bootstrap --timeout 5m
```

`bootstrap` operates only on the existing target after a verified `Running`
precondition, through the typed Lima boundary. It is idempotent and, on a
fully-reconciled target, mutates nothing. It:

- ensures the intended non-root guest user `hermes` is in the `docker` group;
- ensures `/usr/local/bin/hermes` is a symlink to the pinned launcher (only after
  confirming the launcher exists — a missing launcher is reported as drift, never
  a dangling shim);
- **verifies** (not merely trusts an exit code): `uname -m == aarch64`;
  `hermes --version` through the documented stable command path; the Docker server
  is reachable by the `hermes` identity; `git --version`; the persistent
  KB/workspace paths are directories on native Linux (ext4), not a host share; and
  no broad macOS host mount is present.

Any drift or unverifiable state fails closed (exit 6) with remediation. A rerun
is success only when every postcondition is proven. Use `--json` for the
machine-readable envelope (one document on stdout).

### Persistent Hermes locations (V1)

| What | Path (on the VM's native Linux filesystem) |
| --- | --- |
| Guest user | `hermes` |
| Hermes home | `/home/hermes` |
| Knowledge base / profile | `/home/hermes/.hermes` |
| Workspace | `/home/hermes/projects` |

These are also emitted in the `hb vm bootstrap` output (human and `--json`).

## 3. Connect (operator-controlled)

Reaching the remote Hermes instance stays operator-controlled — `hb` does not
open an interactive shell, migrate second-brain data, start a model
conversation, or copy credentials. `limactl shell` logs in as the Lima user, so
reach the `hermes` service identity explicitly:

```text
hb vm ssh -- sudo -u hermes -- hermes --version
```

From here, use the persistent KB under `/home/hermes/.hermes` and the workspace
under `/home/hermes/projects`. Desktop/SSH access to the running Hermes instance
remains a manual operator step.

## Stop (part of the lifecycle proof)

```text
hb vm stop
```

`stop` is graceful and idempotent, never uses `--force`, and never removes the
VM or its data. It re-queries and requires a `Stopped` post-state. For V1
dogfood, leave the VM **Running** at the end.

## Safety notes

- Never copy credentials, `.env`, real KB content, or raw logs into Git.
- `bootstrap` never accepts secrets and never prints raw guest output — only
  derived, bounded, redacted check details.
