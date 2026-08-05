# Architecture Decision Records

Five records cover the decisions that govern the delivered binary.

| ADR | Decision |
|---|---|
| [0001](0001-control-plane-and-trusted-host-inputs.md) | The control plane is one Go binary; config and instance name are proven, not assumed |
| [0002](0002-lima-vm-is-the-trust-boundary.md) | The Lima VM is the trust boundary; the image is pinned and drift fails closed |
| [0003](0003-ownership-split-and-operator-carried-write.md) | The guest holds the Brain and checkouts; write against an origin is carried by the operator's session |
| [0004](0004-mcp-credential-custody-and-egress.md) | MCP credentials live under a separate guest identity; what is still unsolved is named |
| [0005](0005-repository-and-documentation-governance.md) | English, five ADRs, and what leaves the tree instead of rotting in it |

## Rules

An accepted ADR is an immutable record. Changing a decision means adding a new
ADR with `Supersedes:`, not editing the old rationale. [ADR-0005](0005-repository-and-documentation-governance.md)
authorized exactly one consolidation of the record that existed before it; the
twenty originals are at `git show archive/pre-oss:docs/adr/…`, and an older cut
is at `archive/pre-v1`.

Every ADR carries context, decision, consequences and rejected alternatives. The
rejected alternatives are not decoration — they are how a reader tells a decision
from a default.

A change to a security boundary also updates [`../03-architecture.md`](../03-architecture.md)
and the invariants in [`../../AGENTS.md`](../../AGENTS.md).
