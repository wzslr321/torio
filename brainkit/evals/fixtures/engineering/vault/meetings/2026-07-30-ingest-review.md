---
type: meeting
title: Ingest review
timestamp: 2026-07-30T14:00:00+02:00
attendees:
  - people/jan-kowalski.md
project: projects/ingest-rewrite.md
tags: [ingest, review]
---

# Ingest review

With [Jan Kowalski](../people/jan-kowalski.md), on
[Ingest rewrite](../projects/ingest-rewrite.md).

## Notes

Walked through the normaliser. Jan's concern is that two sources already carry
slugs normalised the old way, so the cutover has to tolerate both for a while.

## Decisions

- The slug normaliser lands before any source is cut over.
- Old-style slugs are tolerated on read until the last source has moved, and
  then the tolerance is deleted in the same change that moves it.

## Actions

- Jan: list the sources still carrying old-style slugs.
