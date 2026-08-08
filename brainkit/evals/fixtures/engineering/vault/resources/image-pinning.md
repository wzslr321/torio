---
type: resource
title: Pinning the guest image
description: Why the base image is pinned by digest rather than by tag.
tags: [platform, decisions]
---

# Pinning the guest image

The guest base image is pinned **by digest**, never by tag.

A tag is a mutable pointer. Upstream re-points `24.04` whenever they cut a new
build, so two machines that both say they run `24.04` can be running different
kernels, different package sets, and different defaults. A digest names the
bytes.

The cost is that an upgrade is a deliberate edit with a new digest in it, and
someone has to verify the new image before that edit lands. That cost is the
feature: it is the difference between an upgrade and a surprise.

## Why this matters here

We spent a week on a failure that reproduced on one machine and not another,
and the difference turned out to be two builds of the same tag.
