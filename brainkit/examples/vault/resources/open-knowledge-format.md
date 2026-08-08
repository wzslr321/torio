---
type: resource
title: Open Knowledge Format
description: A minimal Markdown-plus-frontmatter format for knowledge meant to be shared between tools.
resource: https://cloud.google.com/blog/products/data-analytics/how-the-open-knowledge-format-can-improve-data-sharing
tags: [format, reference]
---

# Open Knowledge Format

A directory of Markdown files. Each carries YAML frontmatter; `type` is the only
required field. `title`, `description`, `resource`, `tags` and `timestamp` are
reserved with fixed meanings. Relative Markdown links between documents are the
graph — there is no separate link store. A directory may carry an `index.md`
that curates it.

The deliberate restraint is the interesting part: almost every decision a
knowledge format usually makes is left to whoever profiles it.

## Why this matters here

It is the base of the [vault standard](../projects/vault-standard.md), which is
a profile of it. Using a published base means the frontmatter is not ours to
design, only ours to constrain — and the parts that *are* ours stay visibly
ours.

Open question on the licence of the spec text:
[capture](../inbox/2026-08-06-1412-okf-licence-question.md).
