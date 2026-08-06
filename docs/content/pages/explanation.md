---
output: site/explanation.html
nav: Explanation
order: 5
title: Explanation — why Torio is narrow · Torio
description: Why Torio is intentionally narrow — the VM, loopback, and tunnel boundary, human-only credentials, projects as a list you keep, write capability that lives only in a session you opened, no editor integration, data that comes in and does not go out, and documentation generated from one source.
kicker: Explanation
scope_notice: "This page explains the reasoning behind the boundaries. It is background, not a procedure."
---

# Why Torio is intentionally narrow

Torio is small on purpose. Each boundary below exists so the delivered product is
something an operator can fully control and reason about, rather than a broad
platform that only looks finished.

## The VM, loopback, and tunnel boundary {#vm-boundary}

Torio creates the Lima VM from a pinned template and reconciles it later, but it
never re-images, resets, or deletes one. An instance that does not match the
trusted pins fails closed rather than being recreated — there is no `--force`,
because the recovery a `--force` would offer is indistinguishable from
destroying the thing you were trying to keep.

The persistent Hermes backend runs as a user systemd service bound to **guest
loopback only** — it is not exposed on any public or LAN address. Reaching it
from the host is therefore a deliberate act: you open an SSH port forward
yourself. Torio opens no tunnel and starts no chat. Keeping the backend
loopback-bound and putting you in charge of the forward means network exposure
is never an accident of running a command.

The same reasoning explains the session token. The backend authenticates
non-public API calls, and because `serve` is headless it never publishes a token
of its own — so you pin one deliberately rather than the control plane minting
and distributing credentials on your behalf.

## Credentials are a human-only prerequisite {#credentials}

Torio never sets up, configures, stores, or reads credentials, and never causes
a credential prompt. Read access to a repository must already work from the
guest before you attach it; a remote the guest cannot read without prompting
fails closed with a specific exit code.

That failure is the boundary doing its job. The remedy is a human granting
access on the guest, outside Torio — not a retry, and not a workaround. The
secret and the method by which it is provisioned never enter this repository,
its evidence, or any pull request. The control plane stays free of secret
material by construction rather than by discipline.

Model and provider credentials work the same way. `torio vm ssh` deliberately
forwards no stdin or TTY, so the interactive provider picker cannot be driven
through the control plane at all — configuring a model is something you do in
your own shell on the guest.

## Projects are a list you keep {#projects}

The model can see the repositories you registered, and nothing else. Nothing is
discovered, scanned, or picked up because it happened to be on disk.

The workspace path is never an input. It is derived from the project id, so
there is no way to point Torio at an arbitrary directory and no path to store in
config that could later disagree with reality. Attaching is non-destructive by
construction: it clones only into an absent path, verifies and adopts a checkout
already there, and hard-stops on anything else without resetting, cleaning, or
recloning.

A workspace is also never seeded by copying a checkout from your host, because a
recursive copy would drag host Git configuration, hooks, and keys across the VM
boundary. If read access cannot be provisioned, the correct outcome is to stop
— not to weaken the boundary.

## Write capability lives in a session, not in the system {#writes}

There is no automated commit, push, merge, or release, and the persistent
backend cannot perform one: it holds read access only.

When you want to write to a remote, `torio project shell` forwards your own SSH
agent into an interactive session, and that capability ends when you exit. A
credential that lives only inside a session you opened cannot be used by
something running while you sleep.

## No editor integration, and no host mount {#no-editor-integration}

Torio integrates with no editor and mounts no host directory into the VM. Both
are deliberate. A broad host mount would carry host Git configuration, hooks,
and keys across the VM boundary — the same reason a workspace is never seeded
from a host checkout. So checkouts exist only on the VM's native filesystem,
owned by the `hermes` identity, and an editor reaches them as your own tool over
SSH rather than through a shared folder.

The upside is that the boundary sits in one place regardless of tooling. Whether
you edit in Neovim, VS Code, Cursor, or a CLI agent, the tool is yours to
install and drive; read access remains a human prerequisite and writes to
history remain something you do inside a session you opened. The
[how-to guide](how-to.html#editor) covers each tool and its caveats.

## Data comes in and does not go out {#data-direction}

`torio brain import` brings a Markdown vault onto the guest through verified
staging. There is no matching export, and that asymmetry is intentional: an
export command would be a supported, verified-looking path for Brain content to
leave the boundary, and every guarantee it implied would have to hold.

Copying the Brain back to your host is a `limactl copy` you run yourself. Torio
does not verify it and does not call it a backup.

## How this documentation avoids drifting {#single-source}

Every page on this site and the runbook in the repository are generated from one
set of Markdown sources under `docs/content/`. A section that appears in more
than one place — pinning the session token, say, which is both a step in Get
started and a task in the how-to guides — is a single file included in each, not
a copy. A validation gate re-renders everything and fails if any committed
output has drifted from its source, so the two cannot disagree even briefly.
