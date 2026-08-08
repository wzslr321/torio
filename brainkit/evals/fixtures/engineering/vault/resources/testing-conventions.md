---
type: resource
title: How we write tests
description: Table-driven, no mocks, and the two cases every table has to carry.
tags: [conventions, testing]
---

# How we write tests

Tests here are **table-driven**. One test function per behaviour, one table of
cases inside it, one assertion loop. A file with fifteen near-identical test
functions is a table someone declined to write.

Every table MUST carry two cases, whatever else it carries:

- a case named exactly `empty input`
- a case named exactly `unicode`

Those two are not decoration. Every parsing bug we have shipped was one of them:
something that assumed a non-empty string, or something that counted bytes and
called them characters.

We do not use mocking libraries. Where a test needs a collaborator, it gets a
small hand-written fake in the test file. A mock asserts that the code called
what we expected; a fake lets the test assert what the code produced, which is
the thing we actually care about.

## Why this matters here

We are pragmatic about coverage and strict about corner cases. Nobody here is
asked to hit a percentage. Everybody is asked to have thought about the empty
string and the non-ASCII string before a reviewer has to.
