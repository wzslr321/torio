## Command surface: `torio mcp` {#mcp}

Prepares and verifies the guest boundary that a future MCP broker will use. The
parent command takes no action itself; an absent or unknown subcommand is a
usage error.

| Command | What it does |
| --- | --- |
| `torio mcp install` | Provision the unprivileged `torio-mcp` identity, its private `0700` home, the `torio-mcp-clients` group, and the root-owned policy directory. Then verify the resulting custody boundary. A settled rerun reports `changed:false`. |
| `torio mcp status` | Verify the identity, group, home, policy, Hermes-profile, and optional runtime invariants without repairing them. A never-provisioned guest is an unmet precondition; drift is a verification failure. |

`install` accepts no secrets. It may report `changed:true` together with an
error when a durable mutation succeeded before a later verification failed.
That partial state is retained in JSON details and the human error explains the
required rerun or backend restart.

Both subcommands report the grant they verified: every service in
`/etc/torio-mcp/policy.d/`, its upstream endpoint, how many tools it allows and
how many of those write. Nothing about a service is built into the CLI — a
service is a policy document, and adding one means writing a second file as
root. Under `--json` the grant is a `policy` object with a `services` array
ordered by name, alongside the generation digest a running broker publishes, so
a report and the process enforcing policy can be compared rather than assumed
equal.

A reported write tool is a count, not a capability. The count exists because a
document that marks writes must be able to say how many it grants; no released
binary sends MCP traffic upstream.

**This is custody preparation, not an active integration.** The released CLI
does not package, install, or activate the dormant broker and relay binaries.
It does not perform OAuth or send MCP traffic upstream. Runtime transport and
credential lifecycle remain blocked by ADR-0027 until a complete contract is
accepted.

The multi-service Atlassian implementation under
`spikes/001-multi-mcp-write-window/` is evidence and design exploration only.
Its `PARTIAL` verdict does not promote the spike into the released command
surface.
