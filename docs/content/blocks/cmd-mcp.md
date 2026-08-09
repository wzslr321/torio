## Command surface — `torio mcp` {#mcp}

Installs and verifies the MCP custody boundary and carries traffic through it.
The parent command takes no action itself; an absent or unknown subcommand is a
usage error.

| Command | What it does |
| --- | --- |
| `torio mcp install` | Provision the `torio-mcp` identity and private home, verify root-owned policy, atomically install the broker, relay and unit shipped with this release, and wire the selected backend to the relay. A settled rerun reports `changed:false`. |
| `torio mcp login <service>` | Run interactive OAuth as `torio-mcp` through a loopback-only callback forward. No SSH agent is forwarded and `--json` is not supported. The unit starts after every policy service has logged in. |
| `torio mcp status` | Verify identity separation, private OAuth state, exact policy and backend configuration, and—when login is complete—the active unit, sockets and running policy digest. It repairs nothing. |

Policy is written by the operator as root-owned
`/etc/torio-mcp/policy.d/<service>.json`. Each document names one Streamable
HTTP endpoint and an exact tool list; every tool is explicitly classified as
writing or read-only. `install` does not generate or guess a grant, and an empty
policy is an unmet precondition.

The release archive contains the host `torio` binary plus Linux guest binaries
for `torio-mcp-broker` and `torio-mcp-connect`. Install accepts no secrets. It
may report `changed:true` with an error when a durable mutation succeeded before
a later verification failed; rerun it after fixing the named precondition.

`login` prints the provider authorization URL and waits for the callback. OAuth
discovery, dynamic client registration, PKCE S256, exchange and refresh use the
pinned official MCP Go SDK. Tokens stay below `/home/torio-mcp/oauth` as
`torio-mcp:torio-mcp 0600` files inside the private `0700` home. Torio accepts no
token, client secret or credential file from the host.

Both backends launch the credential-free relay over stdio. Hermes' `config.yaml`
is agent-writable, so its exact relay check is a drift detector, not a boundary.
Claude Code uses root-owned `/etc/claude-code/managed-mcp.json` together with
`allowManagedMcpServersOnly: true`; install removes native MCP entries from the
agent-owned `.claude.json`, and status rejects their return.

Every report enumerates the verified grant by service and endpoint, including
tool and write-tool counts plus the policy generation digest. While any service
still requires login, a missing runtime is the valid dormant state. Once OAuth
state is complete, successful status requires the trusted unit, the exact live
socket set, and a running digest equal to the root-owned documents.

At startup the broker enumerates upstream tools and refuses readiness if a
policy tool is absent. Each call is checked against the exact grant and audited
with time, peer uid, service, tool, write classification and allow/deny result.
Arguments, results, protocol bodies and credentials never enter the audit. A
missing peer uid or unwritable audit fails the call closed.

The broker does not solve confused-deputy use of an explicitly granted tool and
does not prevent exfiltration through unrestricted guest egress. Granting a
write remains an explicit human decision in root-owned policy.
