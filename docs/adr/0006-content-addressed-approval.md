# ADR-0006: Approval wiąże exact content-addressed artifact

- Status: Accepted
- Date: 2026-07-23

## Context

Approval flag lub nazwa refa nie chroni przed TOCTOU. Ref jest mutowalny, a target branch może przesunąć się po review.

## Decision

Sekwencja jest normatywna:

```text
stop → revoke writes → candidate commit/tree → fresh verification
→ human approval bound to OIDs/hashes → target base check
→ fast-forward integration → explicit push
```

Approval tuple zawiera base/review/tree OIDs, diff hash, policy hash, verification hash, image digests i target ref. Integracja PoC wymaga `target HEAD == base_commit`.

## Consequences

- Zmiana dowolnego elementu unieważnia approval.
- Base drift wymaga nowego candidate, verification i approval.
- Review ref jest retention pointerem, nie identity.

## Rejected

- Boolean `approved=true`.
- Automatyczny cherry-pick/rebase na zmieniony target.
- Testy wykonane przed zatrzymaniem workera.
