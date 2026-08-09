# ADR-0013: Managed MCP client configuration and login-gated activation

- Status: Accepted
- Date: 2026-08-09
- Supersedes: the backend-configuration and install-time restart parts of
  [ADR-0012](0012-mcp-broker-transport-and-oauth.md); the Claude Code native-MCP
  exception in [ADR-0009](0009-backend-contract-and-claude-code.md)
- Applies to: `internal/backend/claudecode`, `internal/lima`, `internal/cli`,
  MCP operator documentation

## Context

ADR-0012 correctly selected the transport, OAuth client and kernel custody
boundary, but two implementation facts make parts of its provisioning sequence
too weak or impossible.

First, Claude Code 2.1.220 supports a root-owned managed MCP document and the
root-owned `allowManagedMcpServersOnly` setting. Treating its user-owned
`.claude.json` as the active declaration would knowingly retain a bypass that
the pinned client can exclude.

Second, the broker deliberately refuses to begin OAuth and refuses readiness
when any policy service lacks a stored session. Starting it from `mcp install`
before the operator has run the interactive login flow can only create a
failing systemd unit. Installation and authorization are separate human steps,
so activation has to follow the last successful login rather than installation.

## Decision

For Claude Code, Torio installs `/etc/claude-code/managed-mcp.json` as
`root:root 0644`, containing exactly one credential-free stdio relay entry per
root-owned policy service. The pinned managed settings set
`allowManagedMcpServersOnly: true`. Installation removes native `mcpServers`
entries from the user and per-project sections of `/home/claude/.claude.json`;
status rejects their return. The file under the Claude uid remains a drift
detector, while the root-owned managed documents are the client configuration
the pinned backend honors.

Hermes has no equivalent root-owned managed configuration. Torio reconciles
its agent-owned `config.yaml` to the exact policy service names and relay
arguments, and status treats any difference as drift. This does not become the
authorization boundary: policy and credentials remain inaccessible to the
Hermes uid, and the broker still intersects every request with root-owned
policy.

`torio mcp install` atomically installs the broker, relay and unit and reloads
systemd. It leaves or makes the unit stopped while OAuth is incomplete; with a
complete existing session set it ensures the unit is enabled and active,
restarting only for changed transport/configuration or a changed running policy
digest. `torio mcp login <service>` stores one OAuth session as `torio-mcp`.
After each successful login Torio verifies the private session metadata for
every policy service and enables and starts the unit only when the set is
complete. A multi-service policy therefore has no partly ready broker.

`torio mcp status` reports pending logins as a valid installed-but-not-active
state. Once every OAuth session exists, an absent runtime is an unmet
precondition; successful status then requires the exact unit, live socket set
and running policy digest.

## Consequences

- Claude Code no longer keeps the released MCP route or OAuth tokens under the
  agent uid. Former provider grants may still exist upstream and must be
  revoked by the operator after migration.
- A fresh installation is intentionally dormant rather than a flapping failed
  service. The human authorization step is what can activate it.
- Root ownership prevents the agent from changing Claude's declared MCP route,
  but it does not turn a client setting into an OS sandbox. The Unix socket,
  peer uid, root-owned policy and private broker home remain the enforcement
  boundary.
- Install remains idempotent and secret-free; login remains interactive and
  does not support `--json`.

## Rejected alternatives

**Keep Claude's user-owned MCP configuration because the socket still enforces
tool scope.** Rejected because direct native HTTP entries can carry separate
credentials and bypass the broker entirely, while the pinned backend provides
a root-owned way to exclude them.

**Start the unit during install and accept a failed state until login.**
Rejected because a known-impossible start is not an idempotent postcondition and
teaches operators to ignore failed services.

**Start one service at a time.** Rejected because the delivered broker has one
policy generation and one readiness claim. Publishing only a subset would make
the runtime digest and the operator-visible grant describe different states.
