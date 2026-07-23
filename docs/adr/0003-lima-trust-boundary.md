# ADR-0003: Lima jest granicą od macOS

- Status: Accepted
- Date: 2026-07-23

## Context

Lokalny agent z pełnym terminalem na macOS ma zbyt szeroki blast radius. Dev Containers nie oddzielają same z siebie daemon/runtime od hosta.

## Decision

Hermes runtime, repos, Docker Engine i `hb` działają w Linux arm64 VM Lima. macOS zawiera Desktop/IDE/admin UI. Jedyny stały host mount to jawny narrow transfer folder. Repozytoria i state nie leżą na VirtFS/9p.

## Consequences

- Kompromitacja zwykłego workloadu pozostaje wewnątrz VM, z zastrzeżeniem container escape.
- Remote access używa SSH; backend binduje loopback.
- Laptop sleep nie jest gwarancją 24/7.

## Rejected

- Hermes bezpośrednio na macOS.
- Mount całego home/workspace z macOS.
- Dev Container jako jedyna granica izolacji.
