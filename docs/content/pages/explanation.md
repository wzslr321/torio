---
output: site/explanation.html
nav: Explanation
order: 5
title: Explanation — why Torio is narrow · Torio
description: Why Torio is narrow: the VM and loopback boundary, explicit credential custody, derived project paths, operator-controlled Git writes, no host mount, and one-way Brain import.
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

## Credential custody is explicit {#credentials}

Torio stores no credential on the host. Provider credentials are configured by
the operator inside the guest. MCP OAuth is created by an explicit login and
stored under the separate `torio-mcp` guest identity.

Read access to a private SSH remote is the one thing it helps with, and it helps
without holding anything. The guest generates its own deploy key, keeps the
private half in a file the backend identity owns, and reports the public half.
Authorizing that key on the forge is a human act with a human's account behind
it; until it happens, the attach keeps failing closed. The private half never
reaches the host, this repository, its evidence, or any pull request.

How narrow that key stays is decided by the human, not by Torio. Added to the
repository as a deploy key with write access off, it reads one repository and
writes nothing. Added to an account, it does everything that account can do, and
the attach succeeds either way, because the only difference is on the forge.
Torio says which to do at the moment the key is printed and stops there:
verifying the answer would take a push, and running one to check a boundary is
not a way to keep it. The key is also readable by the identity the model runs
as, so the guest is no longer a place where a prompt-injected agent finds
nothing worth taking.

That division is the point. The control plane stays free of secret material by
construction rather than by discipline, and the capability it does help
provision is the weakest one that makes the tool usable.

`torio vm ssh` forwards no stdin or TTY, so an interactive provider picker must
run in the operator's own guest shell.

## Projects are a list you keep {#projects}

Torio manages only repositories in the registry. It does not discover or scan
checkouts on disk. The registry is not an access-control list: removing an entry
leaves its checkout on the guest until the operator removes it separately.

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

Torio never pushes, merges, or releases. An agent may edit and commit in its
checkout, but remote write capability is not installed as a durable operator
credential.

When you want to write to a remote, `torio project shell` forwards your own SSH
agent into an interactive session, and that capability ends when you exit. A
credential that lives only inside a session you opened cannot be used by
something running while you sleep.

A whole agent is still more capability than a push needs: it answers for every
identity it holds, silently. Pinning `operator_key` in the config narrows the
session to one key and puts a person in front of every signature, with each
decision recorded before it takes effect. The record says what a session was
allowed to sign, which is deliberately a smaller claim than what was pushed:
Torio cannot see what a signature was used for, and a log that pretended
otherwise would be trusted exactly where it is wrong. The same reasoning gives
an agent session `--push-grant`: not a standing permission, but the right to
ask, one signature at a time, for one invocation. With no pin there is nothing
to mediate, so the grant is refused rather than degraded into handing the
socket over bare — and an absent pin changes nothing about `shell`, because a
document with no pin was written by an operator who has not chosen a key, and
choosing one for them is choosing which key a guest may use.

## No host mount {#no-editor-integration}

Torio mounts no host directory into the VM. A broad mount would carry host Git
configuration, hooks, and keys across the boundary. Checkouts stay on the VM's
native filesystem, and editors reach them through a terminal or SSH. The
repository includes an optional Neovim panel; it does not move the checkout to
the host.

The boundary stays in one place regardless of tooling. The
[how-to guide](how-to.html#editor) covers Neovim, Remote-SSH, and terminal
agents.

## Data comes in and does not go out {#data-direction}

`torio brain import` brings a Markdown vault onto the guest through verified
staging. There is no matching export, and that asymmetry is intentional: an
export command would be a supported, verified-looking path for Brain content to
leave the boundary, and every guarantee it implied would have to hold.

Copying the Brain back to your host is a `limactl copy` you run yourself. Torio
does not verify it and does not call it a backup.

## How this documentation avoids drifting {#single-source}

The site and runbook are generated from `docs/content/`. Shared sections are
included from one file, and validation fails when committed output differs from
its source.
