# ADR-0007: Policy obejmuje host tools, skills i implicit credentials

- Status: Accepted
- Date: 2026-07-23

## Context

Container `network none` nie blokuje host-side web/MCP/messaging tools. Pusta jawna lista env nie blokuje credential/env passthrough deklarowanego przez skills.

## Decision

Effective policy jawnie obejmuje:

- host toolsets i MCP servers,
- skills allowlist,
- explicit i implicit env passthrough,
- credential file mounts,
- container network i mounts.

Default jest deny. Demo B worker ma terminal/file routowane do sandboxa, Kanban worker tools i nic więcej, chyba że spike potwierdzi minimalny dodatkowy element.

## Consequences

- Profile nie są wystarczającym enforcementem.
- Policy resolver musi inspekcjonować effective skill metadata.
- `network none` jest prawdziwe tylko razem z host tool deny.

## Rejected

- Kontrola wyłącznie `docker_forward_env`.
- Poleganie na instrukcji promptowej „nie używaj web”.
