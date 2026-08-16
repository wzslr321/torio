---
output: docs/runbooks/first-run.md
---

# Runbook — first run

Brings a workstation from nothing to a working setup: a Linux VM, an agent on
the VM's own loopback, a tunnel you control, a Second Brain, and your first
attached repository.

Every step is idempotent. Rerunning the sequence on a finished setup changes
nothing and exits `0`.

This runbook does not install a model or a provider, accept secrets, or open a
tunnel for you. Those are yours, and where one is needed the step says so.

Running `torio` with no command on a terminal walks the same sequence
interactively, deriving each step from the box rather than from your place in
this document. The steps below are what it runs, in the order it runs them, and
they remain the surface for a script or a CI job.

The operational sections below and the documentation site are generated from
the same sources.

<!-- include: prerequisites level=2 heading="Prerequisites" -->

<!-- include: build-cli level=2 heading="1. Build the CLI" -->

<!-- include: vm-bring-up level=2 heading="2. Create and verify the VM" -->

<!-- include: brain-bring-up level=2 heading="3. Create the Second Brain" -->

<!-- include: project-attach level=2 heading="4. Attach a repository" -->

<!-- include: invariants-projects level=2 heading="What holds, always" -->

<!-- include: provider-auth level=2 heading="5. Configure a model provider" -->

Selecting a model and holding an actual chat are **manual human steps** beyond
this runbook, as is the credential entry the step above describes.

<!-- include: project-push level=2 heading="6. Push, when you decide to" -->

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
