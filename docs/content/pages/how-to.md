---
output: site/how-to.html
nav: How-to guides
order: 3
title: How-to guides · Torio
description: Task-oriented Torio guides — open the operator SSH tunnel, pin a session token, point Hermes Desktop at the backend and the workspace, configure a provider, inspect the workspace safely, edit it with your own editor, and diagnose why a command is not running.
kicker: How-to guides
scope_notice: "One task per section, for when you already have a running setup. Doing this for the first time? Follow [Get started](tutorials.html#get-started) instead — it covers all of this in order."
---

# How-to guides

Short, task-oriented guides for the things an operator does by hand. Each
section stands alone; they share their text with
[Get started](tutorials.html#get-started), so the two can never disagree.

<!-- include: tunnel -->

<!-- include: session-token -->

<!-- include: desktop-connect -->

<!-- include: desktop-workspace -->

<!-- include: provider-auth -->

## Safely check the workspace state {#workspace-state}

To see the current state of the single Code V0 workspace without changing
anything, run the read-only classification commands from
[prepare the workspace](#workspace-clone) and interpret them with the same gate:
absent means it has not been prepared yet, a clean checkout of the exact remote
on `main` is healthy and retained as-is, and anything else is a hard stop.

Checking the Hermes project registration is also read-only:

```bash
torio vm ssh -- sudo -u hermes -- hermes project list
```

The active project is marked with `*`.

<!-- include: workspace-clone -->

<!-- include: workspace-check -->

<!-- include: editor-choices -->

<!-- include: everyday-loop -->

<!-- include: troubleshooting -->

The full exit-code table is in [Reference](reference.html#readiness).
