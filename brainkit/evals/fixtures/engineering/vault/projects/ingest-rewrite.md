---
type: project
title: Ingest rewrite
description: Replace the nightly batch importer with a streaming path.
status: active
tags: [platform, ingest]
---

# Ingest rewrite

## Now

Cutting the batch importer over one source at a time. The slug normaliser is the
piece currently in flight.

## Decisions

- Slugs are normalised once, at ingest, not on read. Normalising on read meant
  two callers disagreed about the same record.
- The cutover is per source, not global. A global switch has no way back that
  does not involve a night.

## Log

- 2026-07-30 — [Review with Jan](../meetings/2026-07-30-ingest-review.md).
  Agreed the normaliser lands before any source moves.
