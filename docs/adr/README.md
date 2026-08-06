# Architecture Decision Records

Six records cover the decisions that govern the delivered binary.

| ADR | Decision |
|---|---|
| [0001](0001-control-plane-and-trusted-host-inputs.md) | The control plane is one Go binary; config and instance name are proven, not assumed |
| [0002](0002-lima-vm-is-the-trust-boundary.md) | The Lima VM is the trust boundary; the image is pinned and drift fails closed |
| [0003](0003-ownership-split-and-operator-carried-write.md) | The guest holds the Brain and checkouts; write against an origin is carried by the operator's session |
| [0004](0004-mcp-credential-custody-and-egress.md) | MCP credentials live under a separate guest identity; what is still unsolved is named |
| [0005](0005-repository-and-documentation-governance.md) | English, five ADRs, and what leaves the tree instead of rotting in it |
| [0006](0006-destination-egress-allowlist-rejected.md) | The destination allowlist is rejected; exfiltration stays unsolved and the documentation keeps saying so |

## Rules

An accepted ADR is an immutable record. Changing a decision means adding a new
ADR with `Supersedes:`, not editing the old rationale. [ADR-0005](0005-repository-and-documentation-governance.md)
authorized exactly one consolidation of the record that existed before it; the
nineteen originals are at `git show archive/pre-oss:docs/adr/…`, and an older cut
is at `archive/pre-v1`.

When a later ADR replaces only part of an earlier one, the earlier record gains a
`Superseded in part by:` line in its header block, naming the later ADR and the
exact part it replaces. The line is a pointer, never an edit: the superseded
prose stays as it was written, because the record of what was believed then is
the thing being kept. A reader arriving at the old ADR — often from a source
comment citing one of its clauses — learns from the header that the question
moved on, without the header pretending the old text ever said something else.
[ADR-0004](0004-mcp-credential-custody-and-egress.md) is the only record in this
tree that carries one.

Every ADR carries context, decision, consequences and rejected alternatives. The
rejected alternatives are not decoration — they are how a reader tells a decision
from a default.

A change to a security boundary also updates [`../03-architecture.md`](../03-architecture.md)
and the invariants in [`../../AGENTS.md`](../../AGENTS.md).
