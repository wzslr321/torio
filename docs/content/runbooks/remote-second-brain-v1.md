---
output: docs/runbooks/remote-second-brain-v1.md
---

# Runbook — Remote Second Brain V1 (controlled dogfood)

Status: **controlled Remote Second Brain V1 path ready for operator use; formal Demo A remains pending.**

This runbook drives the existing, already-created `torio` Lima VM through a
reproducible **start → bootstrap/verify → connect** path. It is deliberately
narrow: it does not create, recreate, or re-image the VM, install a model or
provider, accept secrets, or create gateway/serve services. It does not claim
S1–S8 are solved.

The operational sections below are shared with the documentation site and are
generated from one source, so the two cannot drift.

## Prerequisites

- macOS host (Apple Silicon) with `limactl` on `PATH`.
- The `torio` VM already created (this runbook never creates it).
- The `torio` binary built from this repository: `go build -o torio ./cmd/torio`.

## 1. Start and bootstrap

<!-- include: vm-bring-up level=3 -->

## 2. Persistent Desktop backend (D5 — loopback-only)

<!-- include: serve-bring-up level=3 -->

## 3. Reach the backend from the Mac (operator-controlled SSH tunnel)

<!-- include: tunnel level=3 -->

## 4. Session token

<!-- include: session-token level=3 -->

## 5. Configure Hermes Desktop (human confirmation step)

<!-- include: desktop-connect level=3 -->

Supplying provider/OAuth credentials, selecting a model, holding an actual chat,
and any second-brain/KB data migration remain **manual human steps — not
performed by this runbook and not proof of Demo A.** Credential entry is
interactive and `torio vm ssh` forwards no stdin/TTY, so it cannot be scripted
through the control plane; see the coding-session entrypoint in
[`code-v0-REDACTED-PROJECT.md`](code-v0-REDACTED-PROJECT.md).

## Stop (part of the lifecycle proof)

```bash
torio vm stop
```

`stop` is graceful and idempotent, never uses `--force`, and never removes the
VM or its data. It re-queries and requires a `Stopped` post-state. For V1
dogfood, leave the VM **Running** at the end.

## Safety notes

- Never copy credentials, `.env`, real KB content, or raw logs into Git.
- `bootstrap` never accepts secrets and never prints raw guest output — only derived, bounded, redacted check details.
- The session token is a secret: it appears in this repository only as `[REDACTED]`.
