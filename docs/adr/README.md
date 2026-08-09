# Architecture Decision Records

Fifteen records cover the decisions that govern the delivered binary, the vault
standard it writes against, how both are published, and how the behaviour that
standard asks for is measured.

| ADR | Decision |
|---|---|
| [0001](0001-control-plane-and-trusted-host-inputs.md) | The control plane is one Go binary; config and instance name are proven, not assumed |
| [0002](0002-lima-vm-is-the-trust-boundary.md) | The Lima VM is the trust boundary; the image is pinned and drift fails closed |
| [0003](0003-ownership-split-and-operator-carried-write.md) | The guest holds the Brain and checkouts; write against an origin is carried by the operator's session |
| [0004](0004-mcp-credential-custody-and-egress.md) | MCP credentials live under a separate guest identity; what is still unsolved is named |
| [0005](0005-repository-and-documentation-governance.md) | English, five ADRs, and what leaves the tree instead of rotting in it |
| [0006](0006-destination-egress-allowlist-rejected.md) | The destination allowlist is rejected; exfiltration stays unsolved and the documentation keeps saying so |
| [0008](0008-mcp-broker-daemon-deleted.md) | The dormant MCP broker daemon and relay are deleted; the policy-document parser they held moves into `internal/lima` |
| [0009](0009-backend-contract-and-claude-code.md) | An instance runs one backend, declared by a contract; a backend declares what it has, and verification checks exactly that; Claude Code is the second one |
| [0010](0010-okf-vault-standard-and-brain-kit.md) | The vault format is written down as a profile of OKF, and it ships as a kit installable without the VM; the kit is content, Torio is mechanics |
| [0011](0011-measured-brain-behaviour.md) | What the brain does autonomously is measured against a committed benchmark; scenarios are backend-neutral, the vault diff is the evidence, and no CI gate exists until its cost is known |
| [0012](0012-mcp-broker-transport-and-oauth.md) | The broker carries Streamable HTTP through operator-authorized OAuth; policy is intersected with upstream discovery and the daemon ships as a release payload |
| [0013](0013-mcp-managed-client-config-and-activation.md) | Claude's MCP route is root-managed configuration, and the broker unit activates only after the last required login |
| [0014](0014-okf-profile-divergence-and-log-files.md) | The OKF profile adopts `log.md`, drops frontmatter from directory indexes, and names its one remaining divergence |
| [0015](0015-mediated-agent-forwarding.md) | An operator session forwards an agent Torio serves, holding one pinned key, that asks before every signature |
| [0016](0016-session-scoped-push-grant.md) | An agent session may ask to push; the signature it needs stops at the operator, and no pinned key means no grant |

## Rules

An accepted ADR is an immutable record. Changing a decision means adding a new
ADR with `Supersedes:`, not editing the old rationale. [ADR-0005](0005-repository-and-documentation-governance.md)
authorized exactly one consolidation of the record that existed before it; the
nineteen originals are at `git show archive/pre-oss:docs/adr/…`, and an older cut
is at `archive/pre-v1`. Both tags survive the publication rewrite and still carry
`docs/adr/`.

When a later ADR replaces only part of an earlier one, the earlier record gains a
`Superseded in part by:` line in its header block, naming the later ADR and the
exact part it replaces. The line is a pointer, never an edit: the superseded
prose stays as it was written, because the record of what was believed then is
the thing being kept. A reader arriving at the old ADR — often from a source
comment citing one of its clauses — learns from the header that the question
moved on, without the header pretending the old text ever said something else.
[ADR-0004](0004-mcp-credential-custody-and-egress.md) carries one.

A later record may also correct a *measurement* in an earlier one without
touching its decision. That is not a supersession and does not earn a header
line; the correction is carried in the later record's own text, under an errata
heading, so the earlier text stays as it was written.

Every ADR carries context, decision, consequences and rejected alternatives. The
rejected alternatives are not decoration — they are how a reader tells a decision
from a default.

A change to a security boundary also updates [`../03-architecture.md`](../03-architecture.md)
and the invariants in [`../../AGENTS.md`](../../AGENTS.md).
