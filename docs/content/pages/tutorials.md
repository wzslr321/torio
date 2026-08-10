---
output: site/tutorials.html
nav: Tutorials
order: 2
title: Tutorials — get started with Torio
description: Get started with Torio on one page — build the CLI, create the VM, bring up the loopback backend, open your tunnel, pin a session token, create the Second Brain, attach your first repository, connect Hermes Desktop, and learn the everyday loop.
kicker: Tutorials
scope_notice: "Get started is complete on this page — every command you need is here, in order. Links only ever take you to optional detail."
---

# Tutorials

The complete first-run walkthrough.

## Get started {#get-started}

By the end of this page you will have built the CLI, created the Linux VM,
brought up the loopback-only Hermes backend, reached it from your host through a
tunnel you control, created your Second Brain, attached your first repository,
and held a real Hermes Desktop session against it. Git remote writes remain an
operator action.

Work straight down. Nothing here sends you to another page to finish a step; the
links are for going deeper afterwards.

<nav class="toc" aria-label="Steps on this page">

<!-- toc -->

</nav>


<!-- include: prerequisites level=3 -->

<!-- include: build-cli level=3 heading="Step 1 — Install the CLI" -->

<!-- include: vm-bring-up level=3 heading="Step 2 — Create and verify the VM" -->

<!-- include: serve-bring-up level=3 heading="Step 3 — Bring up the loopback backend" -->

<!-- include: tunnel level=3 heading="Step 4 — Reach the backend from your host" -->

<!-- include: session-token level=3 heading="Step 5 — Pin a session token" -->

<!-- include: brain-bring-up level=3 heading="Step 6 — Create the Second Brain" -->

<!-- include: project-attach level=3 heading="Step 7 — Attach your first repository" -->

<!-- include: desktop-connect level=3 heading="Step 8 — Connect Hermes Desktop" -->

<!-- include: desktop-workspace level=3 heading="Step 9 — Point Desktop at the project" -->

<!-- include: provider-auth level=3 heading="Step 10 — Configure a model provider" -->

<!-- include: everyday-loop level=3 heading="Step 11 — Learn the everyday loop" -->

### Step 12 — Leave a clean end state {#step-end}

Leave the VM and the backend **running**. No project remote was modified.

If a step failed, known first-run failures and fixes are in
[why something isn't running](how-to.html#troubleshooting).

## Where to go next {#next}

- Ready to push something? [Push, when you decide to](how-to.html#project-push).
- Prefer your own editor over Desktop? [Edit a project with your own editor](how-to.html#editor).
- Want the exact command surface and exit codes? [Reference](reference.html).
- Want to know why the boundaries are drawn this way? [Explanation](explanation.html).
