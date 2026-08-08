---
type: project
title: Vault standard
description: Write down the vault format and ship it as an installable kit.
status: active
tags: [standard, writing]
---

# Vault standard

## Now

Draft the type table and get it in front of
[Jane](../people/jane-doe.md) before Friday.

## Decisions

- **No wikilinks.** Relative Markdown links only. They can be verified by a
  plain link checker and followed by a plain reader; wikilinks can be neither.
  Decided at the [kickoff](../meetings/2026-08-06-vault-standard-kickoff.md).
- **`type` is the only required field.** Every other required field is a thing
  an agent gets wrong and a human never fixes.
- **Notes without frontmatter stay readable.** Otherwise nobody with existing
  notes can adopt this.

## Log

- 2026-08-06 — kickoff; eight types named. Whiteboard sketch kept at
  [`attachments/vault-standard-sketch.txt`](../attachments/vault-standard-sketch.txt).
- Open question: what licence the base format's spec carries — captured
  [here](../inbox/2026-08-06-1412-okf-licence-question.md).
