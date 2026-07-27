---
output: site/tutorials.html
nav: Tutorials
order: 2
title: Tutorials — get started with Torio
description: Get started with Torio on one page — build the CLI, start the VM, bring up the loopback backend, open your tunnel, pin a session token, prepare the Code V0 workspace, connect Hermes Desktop, configure a provider, and run the operator loop.
kicker: Tutorials
scope_notice: "Get started is complete on this page — every command you need is here, in order. Links only ever take you to optional detail."
---

# Tutorials

Guided, start-to-finish walkthroughs. Right now there is one; more will be added
as Torio grows.

## Get started {#get-started}

By the end of this page you will have built the CLI, started the existing VM,
brought up the loopback-only Hermes backend, reached it from your Mac through a
tunnel you control, prepared the single Code V0 workspace, and held a real
Hermes Desktop session against it — with no automated commit, push, or
credential handling anywhere in the loop.

Work straight down. Nothing here sends you to another page to finish a step; the
links are for going deeper afterwards.

<nav class="toc" aria-label="Steps on this page">

<!-- toc -->

</nav>


<!-- include: prerequisites level=3 -->

<!-- include: build-cli level=3 heading="Step 1 — Install the CLI" -->

<!-- include: vm-bring-up level=3 heading="Step 2 — Start and verify the VM" -->

<!-- include: serve-bring-up level=3 heading="Step 3 — Bring up the loopback backend" -->

<!-- include: tunnel level=3 heading="Step 4 — Reach the backend from your Mac" -->

<!-- include: session-token level=3 heading="Step 5 — Pin a session token" -->

<!-- include: workspace-preflight level=3 heading="Step 6 — Check read access to the private remote" -->

<!-- include: workspace-clone level=3 heading="Step 7 — Prepare the Code V0 workspace" -->

<!-- include: workspace-project level=3 heading="Step 8 — Register the Hermes project" -->

<!-- include: desktop-connect level=3 heading="Step 9 — Connect Hermes Desktop" -->

<!-- include: desktop-workspace level=3 heading="Step 10 — Point Desktop at the workspace" -->

<!-- include: provider-auth level=3 heading="Step 11 — Configure a model provider" -->

<!-- include: workspace-check level=3 heading="Step 12 — Run one documented check" -->

<!-- include: everyday-loop level=3 heading="Step 13 — Learn the everyday loop" -->

### Step 14 — Leave a clean end state {#step-end}

Leave the VM and the backend **running**. Nothing you did here committed,
pushed, merged, or deleted anything — that is the intended shape of the loop.

If a step failed, every known first-run failure and its fix is in
[why something isn't running](how-to.html#troubleshooting).

## Where to go next {#next}

- Prefer your own editor over Desktop? [Edit the workspace with your own editor](how-to.html#editor).
- Want the exact command surface and exit codes? [Reference](reference.html).
- Want to know why the boundaries are drawn this way? [Explanation](explanation.html).
