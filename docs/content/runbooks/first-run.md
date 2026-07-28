---
output: docs/runbooks/first-run.md
---

# Runbook — first run

Brings a Mac from nothing to a working setup: a Linux VM, a Hermes backend on
the VM's own loopback, a tunnel you control, a Second Brain, and your first
attached repository.

Every step is idempotent. Rerunning the sequence on a finished setup changes
nothing and exits `0`.

This runbook does not install a model or a provider, accept secrets, or open a
tunnel for you. Those are yours, and where one is needed the step says so.

The operational sections below are shared with the documentation site and
generated from one source, so the two cannot drift.

<!-- include: prerequisites level=2 heading="Prerequisites" -->

<!-- include: build-cli level=2 heading="1. Build the CLI" -->

<!-- include: vm-bring-up level=2 heading="2. Create and verify the VM" -->

<!-- include: serve-bring-up level=2 heading="3. Bring up the loopback backend" -->

<!-- include: tunnel level=2 heading="4. Reach the backend from the Mac" -->

<!-- include: session-token level=2 heading="5. Pin a session token" -->

<!-- include: brain-bring-up level=2 heading="6. Create the Second Brain" -->

<!-- include: project-attach level=2 heading="7. Attach a repository" -->

<!-- include: invariants-projects level=2 heading="What holds, always" -->

<!-- include: desktop-connect level=2 heading="8. Connect Hermes Desktop" -->

<!-- include: desktop-workspace level=3 -->

Supplying provider credentials, selecting a model, and holding an actual chat
are **manual human steps**. Credential entry is interactive and `torio vm ssh`
forwards no stdin or TTY, so it cannot be scripted through the control plane —
run the provider picker as `hermes` in a shell you opened yourself.

<!-- include: project-push level=2 heading="9. Push, when you decide to" -->

## Stopping

```bash
torio vm stop
```

`stop` is graceful and idempotent, never uses `--force`, and never removes the
VM or its data. It re-queries and requires a `Stopped` post-state. Day to day,
leave the VM **running**.

## Safety notes

- Never copy credentials, `.env`, real Brain content, or raw logs into Git.
- `bootstrap` never accepts secrets and never prints raw guest output — only derived, bounded, redacted check details.
- The session token is a secret: it appears in this repository only as `[REDACTED]`.
