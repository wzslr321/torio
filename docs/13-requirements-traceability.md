# Requirements and traceability

## Functional

| ID | Requirement | Plan | Primary evidence |
|---|---|---|---|
| FR-001 | VM init/start/stop/status jest idempotentne | D3–D4 | Lima integration tests |
| FR-002 | Desktop łączy się przez loopback/SSH | S1, D5, D8 | live Desktop evidence |
| FR-003 | Gateway używa native lifecycle | S2, D6 | command/service evidence |
| FR-004 | Project registry jest trusted i walidowany | B1 | schema/path tests |
| FR-005 | Submit tworzy immutable effective policy | B2 | canonical hash tests |
| FR-006 | Hermes Kanban dispatchuje task | S3, B2 | task/run evidence |
| FR-007 | Każdy execution ma fresh workload | S4, B5 | canary isolation test |
| FR-008 | Control plane tworzy candidate po stopie | S5, B7 | real Git object tests |
| FR-009 | Fresh verifier sprawdza exact candidate | S7, B8 | image/OID/log evidence |
| FR-010 | Human approval wiąże exact tuple | B9 | mutation invalidation tests |
| FR-011 | Integration jest fast-forward-only | B10 | stale/concurrency tests |
| FR-012 | Push jest osobny i explicit | B11 | local bare remote tests |
| FR-013 | Restart daje deterministic reconciliation | S8, B12 | kill matrix |

## Security

| ID | Requirement | Threat | Test |
|---|---|---|---|
| SR-001 | Brak broad macOS mount | TM-01 | host path access denied |
| SR-002 | Brak Docker authority w workloadzie | TM-02 | socket/CLI/group probes |
| SR-003 | Brak Git/other repo authority | TM-03 | refs/sibling access denied |
| SR-004 | Brak implicit credentials | TM-04 | skill canary denial |
| SR-005 | Brak host-side egress | TM-05 | toolset enumeration/bypass |
| SR-006 | Task branch nie rozszerza policy | TM-06 | malicious config ignored/denied |
| SR-007 | Stop przed freeze | TM-07 | concurrent mutation test |
| SR-008 | Candidate code nie działa na hoście | TM-08 | malicious verifier fixture |
| SR-009 | Approval invaliduje się po zmianie tuple | TM-09 | per-field mutation matrix |
| SR-010 | Base drift blokuje integration | TM-10 | stale target test |
| SR-011 | Brain/worker nie ma admin capability | TM-11 | identity permission test |
| SR-012 | Sekrety nie trafiają do logs | TM-12 | canary redaction test |
| SR-013 | Brak cross-task state | TM-13 | task A/B canary |
| SR-014 | Orphans nie są usuwane na ślepo | TM-14 | mismatched label test |
| SR-015 | Untrusted devcontainer nie steruje runtime | TM-15 | privileged/mount fixture denied |
| SR-016 | Config/version-lock authority tylko z zaufanej ścieżki (no-follow, mode-private, owned-by-EUID) | TM-16 | `internal/config` trust tests: symlink/world-writable/obcy-owner odrzucone (ADR-0013) |

## Release gate

Requirement bez automatycznego lub realnego acceptance evidence ma status `UNVERIFIED`, nawet jeśli kod/config wygląda poprawnie.
