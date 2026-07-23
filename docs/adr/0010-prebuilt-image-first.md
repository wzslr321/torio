# ADR-0010: Prebuilt pinned image zamiast arbitrary devcontainer w PoC

- Status: Accepted
- Date: 2026-07-23

## Context

Dev Container metadata może zawierać build, initialize/lifecycle commands, arbitrary mounts, capabilities, privileged mode i Features. Jest formatem środowiska, nie automatycznie bezpieczną policy.

## Decision

Demo B korzysta z prebuilt Linux/arm64 image pinned przez digest. Project registry, nie task branch, wybiera image. Verification używa pinned verifier image.

Obsługa Dev Container Specification jest późniejszym adapterem z allowlistowanym subsetem i trusted source.

## Consequences

- Dependencies muszą być obecne w image przy `network none`.
- Image build pipeline jest poza pierwszym worker flow.
- Digest jest częścią policy i approval evidence.

## Rejected

- `devcontainer up` na task branch.
- Tag bez digestu.
- Dockerfile/task build z host credentials lub unrestricted secret mounts.
