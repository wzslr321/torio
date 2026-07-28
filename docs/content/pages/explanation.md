---
output: site/explanation.html
nav: Explanation
order: 5
title: Explanation — why Torio is narrow · Torio
description: Why Torio is intentionally narrow — the VM, loopback, and tunnel boundary, human-only credentials, one hardcoded workspace, no Git automation, no editor integration, and legacy architecture kept only as history.
kicker: Explanation
scope_notice: "This page explains the reasoning behind the boundaries. It is background, not a procedure."
---

# Why Torio is intentionally narrow

Torio is small on purpose. Each boundary below exists so the delivered product is
something an operator can fully control and reason about, rather than a broad
platform that only looks finished.

## The VM, loopback, and tunnel boundary {#vm-boundary}

Torio operates on an **existing** Lima VM and never creates, re-images, or
destroys it. The persistent Hermes backend runs as a user systemd service bound
to **guest loopback only** — it is not exposed on any public or LAN address.

Reaching it from the Mac is therefore a deliberate, operator-controlled act: you
open an SSH port forward yourself. Torio opens no tunnel and starts no chat.
Keeping the backend loopback-bound and putting the operator in charge of the
forward means network exposure is never an accident of running a command.

The same reasoning explains the session token. The backend authenticates
non-public API calls, and because `serve` is headless it never publishes a token
of its own — so the operator pins one deliberately rather than the control plane
minting and distributing credentials on their behalf.

## Credentials are a human-only prerequisite {#credentials}

Torio never sets up, configures, stores, or reads credentials, and never causes
a credential prompt. For the Code V0 workspace, repo-scoped **read** access to
the private remote must be established by a human, directly on the guest,
outside Torio.

A credential-neutral preflight is the **gate**: if it passes, read access
already exists and the workspace step proceeds; if it fails, work stops at the
human prerequisite. The secret and the method by which it is provisioned never
enter the repository, its evidence, or any pull request or comment. This keeps
the control plane free of any secret material by construction.

Model and provider credentials work the same way. `torio vm ssh` deliberately
forwards no stdin or TTY, so the interactive provider picker cannot be driven
through the control plane at all — configuring a model is something a human does
in their own shell on the guest.

## Exactly one hardcoded workspace {#one-workspace}

Code V0 targets a single fixed repository and adds no generic project, registry,
or worker machinery. It reuses the existing VM and serve paths plus Hermes' own
native `project` command. One hardcoded workspace keeps the surface area — and
the ways it can go wrong — small enough to hold in your head.

The workspace is also a **credential-neutral guest-side clone**: it is never
seeded by copying a host checkout, because a recursive copy would drag host
`.git` configuration, hooks, and keys across the VM boundary. If read access
cannot be provisioned, the correct outcome is to stop — not to weaken the
boundary.

## No Git automation {#no-git-automation}

There is no automated commit, push, merge, or release. The workspace gate is
strictly non-destructive: a healthy checkout is retained as-is, and a non-repo,
origin-mismatched, dirty, or wrong-branch state is a hard stop with no
destructive action. Writes to history are human-only decisions, made after
inspecting `git diff` and `git status`.

## No editor integration, and no host mount {#no-editor-integration}

Torio integrates with no editor and mounts no macOS directory into the VM. Both
are deliberate. A broad host mount would carry host Git configuration, hooks,
and keys across the VM boundary — the same reason the workspace is never seeded
from a host checkout. So the workspace exists only on the VM's native
filesystem, owned by the `hermes` identity, and an editor reaches it as your own
tool over SSH rather than through a shared folder.

The upside is that the boundary sits in one place regardless of tooling. Whether
you edit in Neovim, VS Code, Cursor, or a CLI agent, the tool is yours to
install and drive; credentials remain a human prerequisite and writes to history
remain human-only. The [how-to guide](how-to.html#editor) covers each tool and
its caveats.

## What Torio does and does not claim {#not-demonstrated}

An operator has driven a Hermes Desktop coding session against the Code V0
workspace by hand, and the prerequisites that turned out to be necessary —
pinning a session token, setting Desktop's working directory, and configuring a
provider — are written down in [Get started](tutorials.html#get-started).

That is a record of what a human did, not a feature. Torio still starts no chat,
supplies no credentials, and selects no model: each of those steps is manual,
and Torio neither performs nor automates them. Writing the steps down makes them
reproducible without moving them inside the control plane.

## How this documentation avoids drifting {#single-source}

Every page on this site and both runbooks in the repository are generated from
one set of Markdown sources under `docs/content/`. A section that appears in
more than one place — pinning the session token, say, which is both a step in
Get started and a task in the how-to guides — is a single file included in each,
not a copy. A validation gate re-renders everything and fails if any committed
output has drifted from its source, so the two cannot disagree even briefly.

## Legacy architecture is history, not a roadmap {#legacy}

An earlier, much broader exploration predates V0: a staged roadmap, a control
plane with a project registry and admission control, per-task worker isolation,
fresh sandboxed verification, and an evidence and review pipeline. None of that
is what Torio V0 delivers.

That material is not part of the product and must not be treated as an
onboarding or next-task path. It no longer ships in the repository's working
tree; it is kept under the annotated tag `archive/pre-v1` and read with
`git show archive/pre-v1:<path>`.
