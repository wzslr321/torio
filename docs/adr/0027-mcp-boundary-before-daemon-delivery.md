# ADR-0027: MCP custody boundary ships before the broker daemon

- Status: Accepted
- Date: 2026-07-30
- Supersedes: delivery timing in [ADR-0022](0022-mcp-credential-broker.md) and the proposed write-window semantics in [ADR-0025](0025-mcp-write-window.md)
- Applies to: `internal/lima`, `internal/cli`, `internal/mcpbroker`, `cmd/torio-mcp-broker`, release packaging

## Context

ADR-0022 accepted the destination architecture: MCP credentials belong to a dedicated `torio-mcp` identity and Hermes reaches upstream services through a policy-enforcing broker. Implementing the local daemon boundary exposed a separate unresolved decision: the installed Hermes client uses stateful Streamable HTTP MCP `2025-11-25`, while ADR-0022 does not specify the operator login callback, OAuth refresh ownership, session lifecycle, SSE handling, or credential-store contract.

Shipping a daemon with a placeholder upstream would claim custody without carrying traffic. Shipping a bearer-token file would choose an authentication contract by accident. Packaging and activating either is not an acceptable implementation of ADR-0022.

ADR-0025 is `Proposed`. Its write-window mechanism was implemented before acceptance and therefore made delivered behavior depend on a non-binding decision.

## Decision

1. `torio mcp install` provisions and verifies only the custody boundary: the `torio-mcp` identity, its private home, the `torio-mcp-clients` group, membership required to publish and reach future sockets, and the root-owned policy directory.
2. The broker and relay binaries are not release payloads. The public install command does not install a unit or activate a daemon until a separate Accepted ADR defines upstream transport and OAuth lifecycle end to end.
3. `torio mcp status` treats an absent broker runtime as a valid provisioned state. Runtime presence, not an installed unit file, triggers daemon verification. If runtime sockets exist, status requires the exact trusted unit to be active and verifies ownership, modes, listener equality with policy, and the running policy digest.
4. The unfinished daemon code remains testable in the repository, including readiness, runtime policy generation, binary installation, unit validation, and drift checks. It is not a delivered product surface.
5. ADR-0025's write window and `torio mcp allow-write` are not delivered. Policy is the single authorization condition: a tool listed in root-owned policy is granted, including a tool marked as writing. A future time-bounded write capability requires another Accepted ADR.

## Consequences

- Torio can establish the kernel-enforced credential owner before deciding how credentials are minted and refreshed.
- The release does not advertise a broker that cannot authenticate or implement the MCP session protocol used by Hermes.
- Provisioning may complete before any daemon exists. This is not service readiness and must not be reported as such.
- The root-owned policy remains legible and verifiable, but no MCP traffic is brokered until the follow-up transport decision is implemented.
- The hardened daemon and systemd code must not be wired back into packaging or activation as part of incidental maintenance.

## Rejected

- **Activate the placeholder daemon.** It always fails upstream and creates false readiness.
- **Implement a shared HTTP POST client.** Request-scoped SSE and MCP session state make that transport incomplete for Hermes' client.
- **Use a manually provisioned bearer token as the final contract.** This defers OAuth refresh and callback custody while pretending to solve them.
- **Keep the write window because code already exists.** Implementation does not promote a Proposed ADR into an accepted product requirement.
