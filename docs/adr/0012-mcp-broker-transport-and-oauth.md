# ADR-0012: The MCP broker carries Streamable HTTP through operator-authorized OAuth

- Status: Accepted
- Date: 2026-08-09
- Supersedes: [ADR-0008](0008-mcp-broker-daemon-deleted.md) in full; the
  "Not delivered — upstream transport and OAuth lifecycle" part of
  [ADR-0004](0004-mcp-credential-custody-and-egress.md); and "MCP is a chosen,
  named hole" in [ADR-0009](0009-backend-contract-and-claude-code.md).
- Applies to: `cmd/torio-mcp-broker`, `cmd/torio-mcp-connect`,
  `internal/mcpbroker`, `internal/lima`, `internal/backend`, release packaging

## Context

ADR-0004 defined the custody boundary but did not define how the broker reaches
an upstream MCP server or completes OAuth. ADR-0008 consequently removed the
dormant broker and relay before publication. ADR-0009 later admitted Claude
Code with a native MCP configuration whose credentials live under the agent's
own uid, explicitly recording that invariant 9 was not met. That exception
contradicts the higher-authority invariant and blocks the next release.

The pinned Hermes Agent commit supports MCP servers launched over stdio with a
`command` and `args`. Claude Code 2.1.220 supports the same transport. Both can
therefore launch a credential-free relay while a separate guest identity owns
the upstream connection and its OAuth state.

The official MCP Go SDK v1.7.0 implements Streamable HTTP, OAuth 2.1 protected
resource and authorization-server discovery, PKCE S256, dynamic client
registration, refresh and token-source restoration. Implementing those
protocols independently would create a second security-sensitive OAuth client
without adding a Torio-specific control.

## Decision

### The delivered path has three hops

For every configured service the path is:

1. the backend launches `/usr/local/bin/torio-mcp-connect <service>` over
   stdio;
2. the relay copies bytes unchanged to
   `/run/torio-mcp/<service>.sock`;
3. `torio-mcp-broker`, running as `torio-mcp`, terminates the local MCP session
   and opens a Streamable HTTP session to the policy document's
   `upstream_endpoint`.

The relay holds no credential, parses no MCP content and is not a control. The
Unix socket's owner, group and mode decide who may connect. The broker obtains
the peer uid from the kernel, not from a field supplied by the client.

Only Streamable HTTP over an endpoint accepted by the existing policy schema is
delivered. SSE and arbitrary stdio upstreams are not silently downgraded or
adapted. An unsupported upstream fails closed before a service socket is
published.

### Policy is intersected with upstream discovery

The root-owned policy remains schema version 1: one service, one endpoint and an
exact list of tool names, each explicitly classified as writing or read-only.
There are no wildcards, prefixes or inferred write classifications.

At start, the broker lists upstream tools and requires every policy tool to
exist upstream. It publishes a service socket only after that check succeeds,
and exposes only the policy-listed subset with the schemas discovered from the
upstream. A missing tool, duplicate service, malformed document, unavailable
upstream or incomplete OAuth session prevents readiness. `torio mcp status`
continues to compare the running digest and exact socket set with the parsed
root-owned documents.

Every `tools/call` decision records time, peer uid, service, exact tool name,
declared write classification and allow/deny result. Arguments, results,
protocol bodies, authorization URLs, codes and tokens never enter the audit or
diagnostic log. If the peer uid cannot be read or the audit record cannot be
written, the call is refused.

### OAuth is an explicit operator operation

`torio mcp login <service>` is the only operation that may begin authorization.
It runs the broker binary's fixed login mode as `torio-mcp` through the existing
guest SSH transport, with a fixed loopback callback forwarded to the host. It
prints the authorization URL for the operator to open and waits for the
callback. The daemon never opens a browser, asks the agent for input or starts a
new authorization flow.

The first delivered registration method is OAuth dynamic client registration.
The SDK performs discovery and PKCE S256. A server that does not support this
method is unsupported and fails closed; pre-registered secrets are not accepted
by Torio's CLI or policy document.

OAuth configuration and tokens are stored per service below
`/home/torio-mcp`, owned by `torio-mcp:torio-mcp` and mode 0600 inside the 0700
home. Every update is temp file, file fsync, atomic rename and directory fsync.
The token source is restored at daemon start and refreshes are persisted by the
same path. An absent or unreadable session is an unmet login precondition, not a
reason to place credentials under an agent uid.

### Both backends use the same custody boundary

Provisioning adds the selected backend identity — `hermes` or `claude` — to
`torio-mcp-clients` and reconciles its MCP entries to the relay command and
service argument. The agent-owned configuration remains a drift detector, not
the boundary: an agent can rewrite it, but cannot read broker credentials,
rewrite root-owned policy or widen what the broker exposes.

Native MCP token stores below either backend's home must be empty. A configured
direct HTTP or foreign-command MCP entry is drift and makes `torio mcp status`
fail.

### The daemon is a release payload

Release archives carry `torio`, `torio-mcp-broker` and
`torio-mcp-connect`. Host platform and guest platform differ on macOS, so the
archive carries broker and relay binaries built for the profile's Linux guest
architecture. `torio mcp install` verifies and installs those payloads as
root-owned 0755 files, installs the root-owned systemd unit, reloads systemd and
restarts the unit only after identity, policy and backend configuration have
been reconciled.

The broker uses the official `github.com/modelcontextprotocol/go-sdk` v1.7.0;
the module version is pinned in `go.mod` and release evidence records it.

### What this does not solve

This decision does not add a destination allowlist. ADR-0006 rejected one, and
the broker identity can still send data to arbitrary network destinations by
running other code available to that uid. The VM boundary, not an MCP profile,
remains the sandbox, and exfiltration by allowed tools remains possible.

This decision does not put inference credentials in broker custody, alter the
operator-carried Git push window, authorize autonomous release, or turn
agent-writable backend configuration into an enforcement mechanism.

## Consequences

- Invariant 9 has one kernel-backed implementation for Hermes and Claude Code:
  credentials are outside the agent uid, the grant is exact and enumerable,
  upstream availability is checked, and calls are policy-mediated and audited.
- `torio mcp install` now installs a running component instead of only
  provisioning its future identity. A policy service without a completed login
  is visibly unavailable and prevents a successful runtime status.
- OAuth login is deliberately interactive and human-only. It cannot run during
  bootstrap, unattended daemon restart or an agent session.
- The SDK expands the dependency and release-artifact surface. Pinning it and
  exercising an OAuth test server are part of the release gate.
- ADR-0008 remains as history explaining why the first dormant implementation
  was removed; this ADR is the new decision that permits a tested, delivered
  implementation to return.

## Rejected alternatives

**Use each backend's native HTTP/OAuth client.** Rejected because its token
store is readable by the same uid that executes agent-chosen commands, and its
tool filter is agent-writable. That recreates the contradiction this ADR
closes.

**Ship the old raw JSON-RPC broker and fill in only its pending HTTP round
trip.** Rejected because Streamable HTTP is a session protocol, not one
independent POST per line, and OAuth refresh is part of that session. The
official SDK already implements both.

**Let the daemon start OAuth on first agent connection.** Rejected because an
agent could cause browser authorization and influence its timing. Authorization
is a separate operator action and the daemon is non-interactive.

**Accept static bearer tokens or client secrets through `torio mcp login`.**
Rejected because command argv, stdin handling and machine output would become
new secret ingress surfaces. The only delivered credential ingress is the OAuth
callback handled under the broker identity.
