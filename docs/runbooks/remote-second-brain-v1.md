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

## 4. Persistent Desktop backend (D5 — loopback-only)

Bring up a persistent Hermes backend as a custom user systemd service on the VM,
bound to guest loopback only, using the existing `/home/hermes/.hermes` profile.

```text
hb serve install --timeout 2m
hb serve start   --timeout 2m
hb serve status
```

- `install` ensures user linger, renders the unit (loopback bind, `HERMES_HOME`,
  `Restart=always`), validates it with `systemd-analyze` **before** activation,
  then reloads and enables it for boot. Idempotent; accepts no secrets; does not
  start the backend.
- `start` starts it and fails closed unless the systemd state is active **and**
  `GET /api/status` answers 200 through loopback. `status` proves the same and
  exits non-zero when not ready (3 = not installed / inactive, 6 = active but the
  endpoint is dead). `stop`/`restart` mirror the lifecycle. `logs [--lines N]`
  shows bounded, redacted, unit-scoped journal entries only.

The backend binds `127.0.0.1:9119` inside the VM (never a public address).

## 5. Reach the backend from the Mac (operator-controlled SSH tunnel)

`hb` deliberately adds no tunnel feature. Derive the tunnel from the supported
live Lima SSH config and forward a host loopback port to the guest backend:

```text
# supported form: ssh -F ~/.lima/hermes-box/ssh.config lima-hermes-box
ssh -F ~/.lima/hermes-box/ssh.config -L 19119:127.0.0.1:9119 -N -f \
    -o ExitOnForwardFailure=yes lima-hermes-box

# verify from the Mac:
curl -s -m 5 -o /dev/null -w '%{http_code}\n' http://127.0.0.1:19119/api/status   # -> 200
```

Tear the tunnel down when done (`kill` the `ssh` process holding the forward).
`overall:degraded` in `/api/status` is expected when the messaging gateway is
stopped — the serve backend/dashboard component is still `ok`.

### Configure Hermes Desktop against the local tunnel (human confirmation step)

In Hermes Desktop, point the Remote Gateway / backend URL at the **local tunnel
endpoint** (`http://127.0.0.1:19119`, i.e. the host side of the SSH tunnel to the
guest `127.0.0.1:9119`). An actual Desktop chat, provider/OAuth credentials, model
selection, and any second-brain/KB data migration are **manual human confirmation
steps — not accomplished by this runbook and not proof of Demo A.**

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
