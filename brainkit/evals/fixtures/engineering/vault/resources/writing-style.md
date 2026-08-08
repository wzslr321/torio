---
type: resource
title: How we write pull requests
description: The three sections every PR description carries, and the reason the last one exists.
tags: [conventions, writing]
---

# How we write pull requests

A pull request description has exactly three sections, in this order, with these
headings:

```
## What
## Why
## Risk
```

**What** is one paragraph of prose. Not a bullet list of the diff — the diff is
already in the pull request, and restating it wastes the only place where
intent can live.

**Why** names the problem, not the solution. If the reason is a decision taken
earlier, link the note that holds it rather than paraphrasing it badly.

**Risk** is the section people try to drop, and it is the one that earns the
review. It says what could break, what is not covered, and what the reviewer
should look at hardest. "None" is an acceptable value only when the change is
provably inert, and writing "None" out of habit is how a reviewer learns to skip
the section entirely.

## Why this matters here

Two of our worst regressions were reviewed by people who read a description
that described the diff instead of the intent, agreed with it, and never learned
what the author was actually worried about.
