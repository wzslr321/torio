# Security Policy

## Reporting a vulnerability

Report privately through GitHub Security Advisories:
**[Report a vulnerability](https://github.com/wzslr321/torio/security/advisories/new)**.
Do not open a public issue.

Never put real secrets in a report, a log excerpt or a reproduction. Replace them
with `[REDACTED]`.

A useful report names:

- the invariant that was broken (the numbered list in [`AGENTS.md`](AGENTS.md) §5);
- a minimal reproduction;
- component versions (`torio version`, `limactl --version`, guest kernel);
- the blast radius;
- the fail-closed behaviour you would expect instead.

Only the latest released version is supported. There is no backport branch.

## What this project claims

Torio is a control plane that provisions a Lima VM, an agent identity and a
credential custody boundary. The full model is in
[`docs/03-architecture.md`](docs/03-architecture.md); this is the short form.

**The trust boundary is the edge of the VM.** Not a process, not the agent's
profile.

**The threat model is prompt injection and a confused agent** — a poisoned
ticket, page or README that the agent was asked to read, carrying an instruction
that reaches the tools the agent legitimately holds.

**An adversarial agent is explicitly out of scope**, and that is the important
part. The agent's job is to produce text a human reads, so the operator's own
terminal is an egress channel. **This project does not claim to prevent
exfiltration.** Any document that starts implying otherwise is wrong and is worth
reporting as a defect.

In scope, and where a report is valuable:

- an agent-reachable path that acquires a **durable** capability — a credential it
  can steal, reuse or transfer;
- a control that turns out to live somewhere the agent can write, so the thing
  being limited can rewrite its own limits;
- a verification that passes on a guest where the boundary does not actually
  hold — a check that proves less than it reports;
- a documented procedure that hands the reader a working credential, or that
  quietly weakens a boundary;
- host-side issues: config authority accepted through a symlink, a world-writable
  trusted directory, or a foreign-owned config being trusted.

## Known and accepted

Reporting one of these tells us nothing new. They are listed because an accepted
trade-off left unstated would misdescribe the boundary.

- **Data exfiltration is unsolved.** The agent legitimately receives content
  through permitted read tools and has unrestricted egress.
- **DNS is an accepted covert channel.** There is no DNS filter and no SNI proxy.
- **The login user has passwordless sudo.** Provisioning relies on it, so an
  operator who runs an agent-writable path through `sudo` executes it as root.
  Invoke the agent as its own identity, never through `sudo`.
- **`SO_PEERCRED` proves a uid, not a program.** A one-liner run by the agent
  looks identical to the real MCP client, so no per-caller policy rests on it.
- **The MCP audit log is a narrow write channel** toward a privileged file: on a
  denial the tool name comes from the agent. It is capped and escaped, which
  bounds the bandwidth without removing the channel.
- **The MCP client configuration is not an OS sandbox.** Every backend honors a
  root-owned declaration, but arbitrary code under the agent uid can open any
  socket its groups permit. The broker's peer-uid check, root-owned exact policy and
  private OAuth home are the enforcement boundary.
- **A granted MCP write can be used by prompt injection.** The broker prevents
  undeclared tools and unaudited calls; it cannot tell whether an allowed call
  reflects the operator's intent. Grant writes narrowly and review the audit.

## Out of scope

These need a different class of tooling than a VM and a control plane:

- a VM or kernel escape;
- a malicious or compromised agent runtime;
- anyone with administrative access to the guest;
- malware requiring an enterprise-grade sandbox;
- hostile multi-tenant use.

## Configurations that are never acceptable

- a broad mount of a host home directory into the guest;
- the agent identity in the `docker` group, or reaching `docker.sock`;
- host Git write credentials placed on the guest;
- a persistent forwarded SSH agent, or `SSH_AUTH_SOCK` shared with the agent
  process;
- automatic merge, push or release — a granted session may *ask* to push, and
  every signature waits for a person at the host, so an unattended one denies;
- a push grant without a pinned key, which would be a socket handed over with
  nothing in front of it;
- policy read from a file the agent can write.
