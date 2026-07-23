# ADR-0009: Osobne lifecycle dla serve i gateway

- Status: Accepted
- Date: 2026-07-23

## Context

Desktop backend i messaging gateway są różnymi procesami. Gateway ma natywne install/start/status; `serve` może wymagać custom user service zależnie od aktualnej wersji CLI.

## Decision

- Gateway lifecycle deleguje do `hermes gateway ...`.
- Backend używa `hermes serve` po feature detection i custom service Hermes Box.
- Backend binduje loopback; dostęp przez SSH transport/tunnel.
- `hb doctor` weryfikuje endpoint, port i live path, nie tylko PID.

## Consequences

- Nie zgadujemy nazwy gateway unitu.
- Compatibility z `dashboard --no-open` wymaga wyników spike'a.
- Signal/Slack adapter nie jest warunkiem Demo A.

## Rejected

- Jeden własny daemon zastępujący oba procesy.
- Publiczny bind bez potrzeby i auth.
