# Torio architecture

This document is not a summary of the code. It describes the only thing the code
cannot show on its own: where the trust boundary runs, why it runs there, and
what it does **not** cover.

## What Torio is

A thin control plane over Lima, Hermes Agent and Git. It is not an agent
framework, a task queue or a worktree manager — those layers either belong to
Hermes or deliberately do not exist.

## The trust boundary

One Lima virtual machine — `vz`/aarch64 on a macOS arm64 host, `qemu`/x86_64 on
a Linux amd64 host
([ADR-0002](adr/0002-lima-vm-is-the-trust-boundary.md) §4). Everything the agent does happens
on that machine's native filesystem. **The boundary is the edge of the VM** — not
a process, and not the Hermes profile.

Two consequences set up the rest of the architecture.

**No broad host mount.** `mounts: []` in the guest template. Repositories and the
Brain live on the VM's disk, not in a host home directory the guest can see. That
is why bringing data in (`torio brain import`) is a one-shot, bounded `limactl
copy` through private staging rather than a copy across a shared path. The Hermes
profile is not a sandbox and no attempt is made to turn it into one; the isolation
comes from the VM edge.

**The service identity is not root-equivalent.** The guest has a dedicated
`hermes` user which is **not in the `docker` group** — the template removes it
during provisioning and `torio vm bootstrap` verifies the absence and fails
closed. No rootful Docker Engine is installed. Membership of `docker` is root on
the guest, so it would hand the agent exactly the authority the VM boundary
exists to remove.

## Threat model

The full statement — scope, the accepted trade-offs, and the reporting path — is
in [`SECURITY.md`](../SECURITY.md). Two pieces are load-bearing for this
document and stay here.

**Explicitly out of scope: an adversarial agent.** This is the load-bearing
sentence of the whole document. The agent's job is to produce text a human reads,
so the operator's terminal is itself an egress channel. No arrangement of
credentials, sockets or firewall rules changes that. **"The box prevents
exfiltration" is not a claim this project makes**, and any document that starts
implying it is wrong and should be corrected.

**`sudo hermes` runs agent-writable code as root.** `/usr/local/bin/hermes` is a
root-owned symlink to `/home/hermes/hermes-agent/venv/bin/hermes`, which the
`hermes` uid owns and can rewrite. That path is first on sudo's `secure_path`,
and the login user has passwordless sudo — provisioning itself relies on it
(`sudo -n usermod …` in `internal/lima/templates/torio.yaml`). An operator who
types `sudo hermes …` therefore executes whatever is at the target, as root.
This is an accepted trade-off of reaching Hermes through one stable command
path, not an oversight — but Hermes should be invoked as the `hermes` identity
and never through `sudo`.

The controls that exist do one thing: they keep the agent from acquiring a
durable, transferable capability, and they make every granted capability legible
and revocable. That is a smaller claim than "safe".

## Ownership split

| Layer | Owner |
| --- | --- |
| Lima lifecycle, provisioning, guest verification | Torio |
| Declaration of attached projects (non-secret) | Torio (`config.json`) |
| Workspace and vault paths | Torio (derived, never supplied) |
| The operator session that carries write capability | Torio (`project shell`) |
| Model execution, sessions, memory, profiles | Hermes Agent |
| Agent-side project registry | Hermes Agent (`hermes project` CLI) |
| Kanban, dispatch, retry | Hermes Agent |

Torio never writes to Hermes' internal state. A project is registered through the
public `hermes project` CLI, not by touching `~/.hermes/kanban.db`.

## Data paths

Three directories under `/home/hermes`, separated on purpose:

- `/home/hermes/.hermes` — Hermes' **profile and application state**
  (`HermesProfilePath`), `hermes:hermes 0750`;
- `/home/hermes/brain` — the **Second Brain**, a private Markdown vault
  (`HermesBrainPath`), `hermes:hermes 0750`;
- `/home/hermes/projects` — **workspaces**, `hermes:torio-projects 2770` (setgid).

Separating the first two is a decision, not cosmetics. `torio vm bootstrap`
checks the ownership and mode of each path.

The setgid bit on `projects` is what lets the operator and the `hermes` identity
work on the same checkout: both accounts are in `torio-projects`, so files created
by one are writable by the other. Without it, an operator session would leave
behind a checkout the agent could not continue in.

**A workspace path is not an input.** It is derived from the project id as
`/home/hermes/projects/<id>`. The operator supplies an id and a remote, never a
path, and the config document has no field to hold one.

## Where write access to an origin comes from

This is the one place the architecture says "no" to something that would be
convenient.

The persistent Hermes has **read** access to an origin and nothing else. It holds
no token, does not inherit `SSH_AUTH_SOCK`, and the guest template sets
`ssh.forwardAgent: false` globally.

Write capability exists only for the duration of one interactive session:

```text
torio project shell <id>
  → ssh -A -t lima-torio /usr/local/bin/torio-project-shell <workspace>/<id>
  → ordinary Git commands under the operator's identity, in group torio-projects
  → exit — the forwarded agent goes with the session
```

The guest-side helper is `root:root 0755`. It is materialized by the Lima
template on every start, and bootstrap installs it when the path is **absent** —
the template is rendered once, at `vm init`, so without that a corrected helper
could reach an existing box only by recreating the VM. A helper that is present
and wrong is still reported and left alone: drift is never repaired. The reason
is direct: the operator's forwarded agent passes through this path, so nothing
the agent identity or the operator can overwrite may sit on it.

The helper takes the declared backend's workspace rather than naming one. It
serves every backend, and a directory written into it refused every project on
the others while the host derived the path correctly.

Torio stores no Git write credential, automates no push, merge or release, and
runs no test push to prove anything. A remote carrying an embedded password,
token, query or fragment is rejected.

## MCP custody boundary

MCP credentials belong to a separate unprivileged identity `torio-mcp`, whose
home `/home/torio-mcp` is `0700`. The selected agent (`hermes` or `claude`) is
not in the owning group and cannot read that directory; it gets only membership
of `torio-mcp-clients`, which permits connection to policy-specific Unix
sockets. The explicit tool grant lives outside the agent's profile, in
root-owned `/etc/torio-mcp/policy.d`.

The backend launches a credential-free stdio relay. The broker terminates that
local MCP session, identifies the caller uid through peer credentials, verifies
the policy tool set against upstream discovery, and carries allowed calls over
Streamable HTTP using OAuth state owned only by `torio-mcp`. Calls are audited
without content. Claude Code's MCP declaration is root-managed; Hermes' own
configuration is agent-writable and therefore checked only as drift.

Installation is dormant while any policy service lacks an operator-authorized
OAuth session. The last `torio mcp login <service>` enables and starts the unit;
from then on successful status requires the exact unit, live policy socket set
and running policy digest
([ADR-0004](adr/0004-mcp-credential-custody-and-egress.md),
[ADR-0012](adr/0012-mcp-broker-transport-and-oauth.md),
[ADR-0013](adr/0013-mcp-managed-client-config-and-activation.md)).

## The Second Brain inside projects

The Brain is a separate Hermes Project for working on it directly. Other projects
reach it through a **global `torio-brain` skill** — retrieval through file and
search tools, not injection of content.

The choice is deliberate. Injecting the whole vault into every project's system
prompt would invalidate the prompt cache on every note change and move private
content into the context of projects that do not need it. Adding
`/home/hermes/brain` as a folder of every project has the same effect and is
forbidden.

## The backend, and reaching it from the host

`hermes serve` runs as a **user systemd service** on the guest, bound to
`127.0.0.1:9119` — the guest's loopback, not the VM interface. From the host the
only route is an SSH tunnel the operator opens. Torio opens no tunnel and starts
no chat session.

## Where Torio stops

Deliberately absent: an agent loop, a second Kanban, a dispatcher, a queue, a
retry engine, per-task worker containers, a fresh verifier, automatic
merge/push/release, a Vault-class secret manager, a domain egress allowlist,
importing a host checkout, and any broad mount of a host directory.

That list is not a roadmap; the earlier exploration behind it is under the
`archive/pre-v1` and `archive/pre-oss` tags
([ADR-0005](adr/0005-repository-and-documentation-governance.md)).
