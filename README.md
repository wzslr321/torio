# Torio

Project website: [torio.dev](https://torio.dev)

> Torio is a thin, trusted control plane for running an AI second brain and a
> single controlled coding workspace on a Linux VM on Apple Silicon Macs.

**Status: Torio V0.** The delivered product is deliberately narrow and fully
operator-controlled. Everything below describes what exists today; anything not
listed here is not part of V0.

## What Torio V0 is today

Torio V0 delivers two things on the **existing** `hermes-box` Lima VM:

1. **Remote Second Brain V1 (controlled dogfood).** A persistent Hermes backend
   runs as a user systemd service inside the VM, bound to guest loopback only. It
   is reached from the Mac exclusively through an **operator-established SSH
   tunnel** — Torio itself opens no tunnel and starts no chat.

2. **Code V0 — exactly one hardcoded workspace.** Torio prepares a single,
   credential-neutral guest-side clone of one fixed repository at
   `/home/hermes/projects/REDACTED-PROJECT` and registers it as a Hermes
   project. The operator then inspects/edits it, runs one documented
   non-destructive check, reviews `git diff` / `git status`, and **decides
   manually** whether to commit or push.

The operator-controlled code loop is: **edit/inspect → run one documented safe
check → inspect diff/status → manual commit/push decision.** Commit and push are
human-only and out of scope for Torio.

## Scope and limitations (what V0 is *not*)

Torio V0 is intentionally not:

- **not** a worker/agent platform (no dispatcher, queue, or autonomous workers);
- **not** multi-project — exactly one hardcoded workspace;
- **not** an isolated per-task sandbox;
- **not** Git automation — no automated commit, push, merge, or release;
- **not** a demonstrated Hermes Desktop coding chat — driving an actual Desktop
  session is a manual human step, not something V0 performs or proves.

## Prerequisites (high level)

- A macOS host on Apple Silicon with `limactl` on `PATH`.
- The `hermes-box` Lima VM **already created** — Torio never creates,
  re-images, or destroys it.
- The `hb` binary built from this repository: `go build -o hb ./cmd/hb`.
- For Code V0 only: repo-scoped **read** access to the private remote must
  already exist on the guest. Provisioning that access is a **human-only
  prerequisite outside Torio** (see the note below).

## Getting started

Torio's canonical operational documentation is the two runbooks. They contain
the exact, ordered commands; this README does not duplicate them.

1. Build the CLI: `go build -o hb ./cmd/hb`.
2. Bring up the Remote Second Brain and connect over the operator tunnel:
   [`docs/runbooks/remote-second-brain-v1.md`](docs/runbooks/remote-second-brain-v1.md).
3. Prepare and drive the single Code V0 workspace:
   [`docs/runbooks/code-v0-REDACTED-PROJECT.md`](docs/runbooks/code-v0-REDACTED-PROJECT.md).

Optionally, validate the documentation pack for internal consistency:

```bash
python3 scripts/validate_artifacts.py
```

## Private-repository read access is a human-only prerequisite

Torio never sets up, configures, stores, or reads credentials, and never causes a
credential prompt. Read access to the private Code V0 remote must be established
by a **human, directly on the guest, outside Torio**. The Code V0 runbook's
**noninteractive credential preflight is the gate**: if it passes, read access
exists and the workspace step proceeds; if it fails, stop at the human
prerequisite. The secret and the method by which it is provisioned never enter
this repository, its evidence, or any PR/comment.

## Canonical documentation surface

The active operational surface of Torio V0 is only:

- this `README.md`;
- [`docs/runbooks/remote-second-brain-v1.md`](docs/runbooks/remote-second-brain-v1.md);
- [`docs/runbooks/code-v0-REDACTED-PROJECT.md`](docs/runbooks/code-v0-REDACTED-PROJECT.md).

Normative engineering rules for contributors and agents live in
[`AGENTS.md`](AGENTS.md).

## Legacy architecture

An earlier, broader exploration (Demo A / Demo B, worker/control-plane,
registry, verifier, and evidence-pipeline plans) predates V0 and is **superseded**.
It is retained for historical context only and is **not** the current
implementation plan. See [`docs/legacy-architecture.md`](docs/legacy-architecture.md)
for what that material is and where it lives. Do not use it as an onboarding or
task path.
