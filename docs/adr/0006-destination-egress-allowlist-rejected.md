# ADR-0006: The destination egress allowlist is rejected

- Status: Accepted
- Date: 2026-08-06
- Supersedes: the **Keyed by destination** paragraph under "Blocked — egress
  control" in [ADR-0004](0004-mcp-credential-custody-and-egress.md), which
  recorded the question as open and left the choice to the operator. Nothing else
  in ADR-0004 changes.
- Applies to: `AGENTS.md`, `SECURITY.md`, `docs/03-architecture.md`,
  `internal/mcpbroker`, `cmd/torio-mcp-broker`

## Context

[ADR-0004](0004-mcp-credential-custody-and-egress.md) split guest egress control
into two halves. One is keyed on uid: a netfilter rule that compares the fsuid of
the process that created the socket, which `hermes` has no primitive to change or
bypass. The other is keyed on destination: an enumerable list of hosts the guest
may reach at all, everything else refused.

The destination half was proposed for one thing. ADR-0004 states: it "is the
only part of this work that addresses data exfiltration at all". Every other mechanism in that record governs what the
agent may **hold** or **invoke**. Custody moves the tokens under `torio-mcp`;
root-owned policy decides which tools are reachable; the audit log records the
decision. None of them touches what happens to bytes the agent has already and
legitimately read. A destination list was the one proposal aimed at that, and it
was aimed at it weakly: it constrains where content can go, not whether it goes.

It never got built, because `AGENTS.md` section 4 names "a domain network
allowlist" among the things Torio must not implement, and `AGENTS.md` is the first
source of truth. ADR-0004 recorded the contradiction instead of resolving it, and
`AGENTS.md` carried a matching block saying the conflict stays open and that
nothing is built on it until someone chooses. Both documents named the same two
exits: amend the prohibition, or reject the allowlist and keep saying plainly that
exfiltration is unsolved.

Leaving it open has its own cost. A question parked in the first source of truth
blocks work on the uid-keyed half as well, because a reader cannot tell how much
of "egress control" is still live. And an open question a year old is
indistinguishable from an oversight.

## Decision

**The destination-keyed egress allowlist is rejected. The `AGENTS.md` section 4
prohibition stands unamended.**

1. Torio does not implement a domain network allowlist, a DNS filter or an SNI
   filter. The prohibition is not narrowed, qualified or scoped to a component; a
   later decision to build one requires an ADR that supersedes this record.

2. The open question in `AGENTS.md` section 4 closes. The list of things Torio
   must not implement is unchanged — what changes is that the entry is now a
   settled boundary rather than a contested one.

3. The documentation keeps saying, without qualification, that exfiltration is
   unsolved. `SECURITY.md` and [`docs/03-architecture.md`](../03-architecture.md)
   already do, and softening that wording is a defect.

The reasoning is about where this product is used, not about the strength of the
mechanism. The box runs in the operator's own workplace against the operator's own
Jira and Confluence. A control aimed at an adversary who has already been given
legitimate access to that content is not what this box is for, and the threat it
would mitigate is not worth the machinery. The threat model in `SECURITY.md`
already excludes an adversarial agent; this rejection follows from that exclusion
rather than adding an exception to it.

### What this does not decide

**The uid-keyed half of egress control is a separate, still-open question.** It
keys on the identity of the calling process, not on a destination, so
`AGENTS.md` section 4 never blocked it and this record does not reject it. It
remains what ADR-0004 left it: proposed and unbuilt. It is also the piece that
would close the inference-credential gap ADR-0004 names, where any process on the
guest that can open the broker's loopback port spends the subscription. Reading
this ADR as a rejection of egress control in general would be a misreading, and
building the uid-keyed rule needs its own accepted ADR, not this one.

## Consequences

- **Exfiltration is unsolved, has no owner and no plan.** An agent that reads
  content legitimately through a permitted tool has unrestricted egress and can
  send that content anywhere: its own shell, a Git remote, an HTTP request, a DNS
  query.

- **No mechanism in this repository partially mitigates it, and none may be
  described as doing so.** Exfiltration does not pass through the broker at all,
  so no condition on broker calls can bound it.

- A reader auditing this box gets an honest answer to "what stops the agent
  sending my Confluence page somewhere else" — nothing does — instead of a
  control they would have to test to find out it was cosmetic.

- The uid-keyed rule now has to be argued on its own merits when someone builds
  it, since it can no longer arrive as one half of a two-part egress design.
  That is the intended effect.

- ADR-0004 §8, the configurable upstream endpoint, keeps its stated purpose: it
  is a seam that lets a later decision put a proxy in front of an MCP upstream
  without a redesign. It was never a commitment to this allowlist and does not
  become dead code with it rejected.

## Rejected

- **Amending `AGENTS.md` section 4 to permit the allowlist.** The other exit both
  documents named, and the one actually on the table. Rejected for the reasoning
  under the decision: the threat is not worth the machinery in the context this
  product is used in.

- **Leaving the question open.** An unresolved conflict in the first source of
  truth stops work on both halves of egress control and, read a year later, is
  indistinguishable from nobody having thought about it. Deciding wrongly costs
  one superseding ADR; not deciding costs the reader the ability to tell a
  decision from an omission.

- **A filtering proxy in front of the MCP upstream, using the ADR-0004 §8 seam.**
  Cheaper than a guest-wide ruleset, and the seam would carry it. It would also
  filter the single channel that is already enumerable and policy-governed while
  leaving the agent's shell, Git and plain HTTP untouched — a control positioned
  exactly where it is least needed, and one whose existence would invite the
  claim it does not earn.

- **TLS interception in the guest.** Already rejected in ADR-0004 on its own
  terms, for putting a trusted CA in the guest. Recorded again only to note that
  it no longer has a purpose to argue for: content inspection was the one thing it
  bought that a destination list could not, and content inspection is out of scope
  by the same reasoning as the list itself.

- **Listing the allowlist as future work instead of rejecting it.** A roadmap
  entry for a control nobody intends to build reads as a promise, and
  [`docs/03-architecture.md`](../03-architecture.md) already states that the list
  of things Torio deliberately does not do is not a roadmap.
