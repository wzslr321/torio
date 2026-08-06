# ADR-0004: MCP credential custody, and what the box still leaks

- Status: Accepted for the custody boundary; the broker daemon, the write window,
  inference-credential custody and egress control are **not delivered** and are
  marked per section below.
- Date: 2026-08-05
- Superseded in part by: [ADR-0006](0006-destination-egress-allowlist-rejected.md),
  which replaces the **Keyed by destination** paragraph under "Blocked — egress
  control" below. That paragraph records the destination allowlist as an open
  question for the operator to settle; it is settled, and the allowlist is
  rejected. [ADR-0008](0008-mcp-broker-daemon-deleted.md) separately replaces
  the sentence "The unfinished daemon code stays in the repository and stays
  tested. It is not a delivered product surface." under "Not delivered — the
  broker daemon" below: the code is deleted rather than kept, and the
  policy-document parser it held moves into `internal/lima`. Nothing else here
  is superseded — in particular the custody boundary, `torio mcp
  install`/`status`, the uid-keyed half of egress control, and the remaining
  "not delivered" items (the write window and inference-credential custody)
  all stand exactly as recorded.
- Consolidates: the MCP credential broker, inference credential custody, guest
  egress control, the MCP write window, the destination allowlist, and the
  delivery boundary that separated custody from the daemon. The superseded
  originals are recoverable at `git show archive/pre-oss:docs/adr/…` (`0022`,
  `0023`, `0024`, `0025`, `0026`, `0027`).
- Applies to: `internal/lima`, `internal/cli`, `internal/mcpbroker`,
  `cmd/torio-mcp-broker`, `cmd/torio-mcp-connect`

## Context

MCP is how an agent in the box reaches Slack, Jira and Confluence. Without it the
box is not somewhere you can work. The question is not whether, but on what terms.

The terms Hermes provides on its own, verified in its sources and on a live guest
in July 2026:

- MCP OAuth tokens live in `$HERMES_HOME/mcp-tokens/`, server configuration in
  `$HERMES_HOME/config.yaml`, and the inference provider credential in
  `$HERMES_HOME/auth.json` — all owned by `hermes`, mode `0600`.
- The agent runs as `hermes` with `terminal.backend: local`, so it has a shell
  under that same uid.
- Hermes denies reads of those files to its own file tool and documents the limit
  itself: *"**This is NOT a security boundary.** The terminal tool runs as the
  same OS user with shell access; the agent can still `cat auth.json` […] and
  exfiltrate the file."*
- The only least-privilege mechanism offered for MCP is
  `mcp_servers.<name>.tools.include` in `config.yaml`. That file is not on the
  write denylist and `HERMES_WRITE_SAFE_ROOT` is unset, so the allowlist is a
  default value, not a control — the thing being limited can rewrite it.

Two separate properties are easy to merge into one unsolvable problem. **Custody**
is what the agent can touch. **Capability** is what it can do with access it was
given. Hermes' configuration settles neither: the token is stealable because it
sits under the agent's uid, and the scope is rewritable because it sits in the
agent's file.

One precondition makes a solution possible: `hermes` has no sudo and belongs only
to `hermes` and `torio-projects`. A level the agent cannot reach exists.

## Decision

**MCP credentials stop existing under the agent's identity. Hermes reaches an MCP
server only through a broker running under a separate uid.**

The numbering below is load-bearing: source comments cite these clauses as
`ADR-0004 §N`.

1. **Identity.** An unprivileged uid `torio-mcp` with its own home
   `/home/torio-mcp` (`0700 torio-mcp:torio-mcp`), no sudo, not in
   `torio-projects`. `hermes` cannot read that directory.

2. **Custody.** Every upstream secret — OAuth tokens, client secrets, API keys —
   belongs to `torio-mcp` and exists only in its home. After migration
   `$HERMES_HOME/mcp-tokens/` must be empty, and anything else is drift.

3. **Transport, and reachability as the only granted privilege.** The broker
   listens on unix sockets at `/run/torio-mcp/<service>.sock`
   (`0660 torio-mcp:torio-mcp-clients`). `hermes` is in `torio-mcp-clients`, and
   that membership means exactly one thing: *you may open a connection to the
   broker*. The control is the socket's ownership and the fact that the kernel
   establishes the peer's identity (`SO_PEERCRED`), not any presented secret.

   What `SO_PEERCRED` does **not** buy: it gives the uid of the connecting
   process — more than group membership, since it names an identity — but it
   cannot tell the Hermes MCP client from anything else running under that uid. A
   one-liner from the agent looks identical. No per-caller policy may rest on it,
   and operator documentation must not describe it as if it could.

   A service name is capped at 32 bytes because a unix socket address must fit
   in `sun_path` (~104 bytes), and
   `/run/torio-mcp/` plus the longest permitted name plus `.sock` is 52. An
   address too long is unreachable by construction instead of failing at
   `connect()`. Both sides — the broker binding and the relay searching — must
   hold the same rule; a name one accepts and the other rejects is a socket
   nothing reaches.

4. **Policy is legible; secrets are not.** Tool scope lives in
   `/etc/torio-mcp/policy.d/<service>.json`, `root:root 0644` — readable by the
   agent, unwritable by it. JSON rather than YAML because `internal/config`
   already has a fail-closed schema idiom, and adding a YAML parser to a trusted
   policy path is a dependency in the worst possible place. Deny by default: only
   tools named explicitly are passed, with no inference from names or patterns.
   Every entry that carries a write is marked as such, so a report can state how
   many write-capable tools are granted.

5. **Audit without content.** Every call is logged: timestamp, service, tool
   name, caller uid, allow/deny, and a reason. **Never arguments and never
   response bodies** — the contents of Jira and Confluence do not belong in a log
   file. The reason is recorded rather than derived, because deriving it needs the
   policy as it read at the moment of the decision and nothing stores that; after
   one edit, reconstruction is guessing. It comes from a closed enum, never from
   caller text.

6. **Fail-closed verification.** uid/gid existence and home mode; `hermes` in
   `torio-mcp-clients` and **not** in `torio-mcp`; emptiness of
   `$HERMES_HOME/mcp-tokens/`; no `mcp_servers` entry pointing anywhere but the
   relay; socket owner, group and mode; parseability and ownership of policy
   files. A socket file left behind by a broker that died passes every ownership
   and mode check and refuses every connection, so present-but-dead is drift, not
   health. The unit is validated with `systemd-analyze verify` before activation.
   Drift is a non-zero exit with stable markers, never a silent repair.

7. **Torio still touches no secret.** Logging in to a service is an interactive
   operator action performed as `torio-mcp`; the broker mints and stores the token
   in its own home. Torio neither sees, stores nor relays it.

8. **The upstream endpoint is configurable**, so a later decision about egress
   control can put a proxy in front of it without a redesign.

### Delivery status — only the custody boundary ships

`torio mcp install` provisions and verifies §1, §2, the client group, and the
root-owned policy directory. `torio mcp status` verifies that boundary without
repairing it.

### Not delivered — the broker daemon

Implementing the local daemon exposed an unresolved decision underneath it. The
installed Hermes client speaks stateful Streamable HTTP MCP `2025-11-25`, and
nothing here specifies the operator login callback, OAuth refresh ownership,
session lifecycle, SSE handling, or the credential-store contract.

Shipping a daemon with a placeholder upstream would claim custody without
carrying traffic. Shipping a bearer-token file would choose an authentication
contract by accident. So:

- The broker and relay binaries are not release payloads. The public install
  command installs no unit and activates no daemon until a separate accepted ADR
  defines upstream transport and OAuth lifecycle end to end.
- `torio mcp status` treats an absent broker runtime as a valid provisioned
  state. Runtime presence, not an installed unit file, triggers daemon
  verification; if runtime sockets exist, status requires the exact trusted unit
  to be active and verifies ownership, modes, listener equality with policy, and
  the running policy digest.
- The unfinished daemon code stays in the repository and stays tested. It is not
  a delivered product surface.

One thing about it is already settled. The relay (`torio-mcp-connect`) named in
the Hermes config is a protocol adapter and **not** part of the control: it holds
no secret, and the agent can ignore it and talk to the socket directly. That is
precisely why §4 policy is enforced by the broker.

### Not delivered — the write window

A time-bounded write capability (`torio mcp allow-write`) was designed and
implemented before its decision was accepted, which made delivered behaviour
depend on a non-binding record. It is withdrawn. Policy is the single
authorization condition today: a tool listed in root-owned policy is granted,
including one marked as writing. Reinstating a write window requires its own
accepted ADR.

The gap it addresses is real: a
grant says *what*, never *when*, and a poisoned Jira ticket reaches exactly the
tools the agent legitimately holds. Its shape was two independent conditions —
a grant in policy **and** an open, self-expiring window per service — with the
window as a file in the broker's home that the agent's identity cannot reach, read
on every call, exclusive at its own expiry instant, and closed on any uncertainty.

### Not delivered — inference credential custody

`$HERMES_HOME/auth.json` holds a live OAuth pair and is stealable exactly as MCP
tokens were. Moving it under a `torio-infer` uid is possible, and one structural
fact makes it attractive: the Codex refresh token rotates and is single-use, so
there can be exactly one holder, and a single writer under a file lock resolves
the race that multiple holders lose to `refresh_token_reused`.

One obstacle is real. `model.base_url` is an http(s) URL handed to the OpenAI SDK;
Hermes has no configuration surface for a unix-socket transport on the inference
path. So the kernel cannot establish the caller's identity there, and the shape
used for MCP does not carry over. The honest summary is that this trades **theft**
of the credential for **use** of it: any process on the guest that can open the
broker's loopback port spends the subscription. That is a real reduction and must
not be described otherwise. Closing it needs a uid-keyed netfilter rule, below.

### Blocked — egress control

Two halves, separated deliberately.

**Keyed by uid.** A netfilter rule keyed on uid is a real boundary against
`hermes`: `meta skuid` compares `sock->file->f_cred->fsuid` — the fsuid of the
process that *created the socket*, set once and never recomputed — and without
`CAP_SETUID` an unprivileged `hermes` cannot borrow another uid. `hermes` has no
sudo, no capability, no privileged executable path, and `unshare --net` is denied,
so it has no primitive to read, change or bypass the ruleset. This half is
proposed and unbuilt.

**Keyed by destination.** An enumerable allowlist of destinations is the only part
of this work that addresses data exfiltration at all, and it is **blocked**:
`AGENTS.md` names a domain network allowlist among the things Torio must not
implement, `AGENTS.md` is the first source of truth, and resolving that
contradiction is the operator's decision, not the implementer's. Two exits exist —
amend that rule, or reject the allowlist and keep saying that exfiltration
is unsolved. Nothing is built on it until one is chosen.

## Consequences

- Torio stops being only a control plane over Lima and starts delivering a guest
  identity. That is a real scope increase, taken deliberately, in the same shape
  as `serve install`: deterministic unit, validation before activation,
  idempotence, drift reported.
- **`hermes mcp add` is not a supported path on a managed guest.** It writes
  credentials into `$HERMES_HOME` and bypasses the broker, so its use is drift
  that verification must detect.
- The claim "the persistent backend is read-only" narrows to writes against an
  origin. MCP is a separate, explicitly described capability channel
  ([ADR-0003](0003-ownership-split-and-operator-carried-write.md)).
- **Membership of `torio-mcp-clients` is a capability grant** and must be verified
  like any other. It is not the equivalent of the `docker` group, which is
  root-equivalent; this one permits opening a connection to a service that
  validates every request anyway.
- **The audit log is a narrow write channel toward a privileged file.** On a
  denial the tool name comes entirely from the agent, so it can encode data in
  invented names and have the broker record them. A 128-byte cap with a
  truncation marker and JSON escaping bounds the bandwidth; it does not remove
  the channel.
- **Exfiltration is unsolved and this ADR does not close it.** The agent receives
  content legitimately through permitted read tools and has unrestricted egress.
  Saying otherwise would be false.

## Rejected

- **`mcp_servers.<n>.tools.include` as least privilege.** Lives in a file the
  agent can overwrite. A default, not a control.
- **Hermes' read denylist as a boundary.** Rejected on the strength of its own
  documentation. A control and its bypass at the same privilege level is not a
  boundary.
- **`HERMES_WRITE_SAFE_ROOT` as the answer.** Raises the cost of an accidental
  write through file tools and nothing else; a shell steps around it identically.
  Acceptable as hygiene, never describable as a boundary.
- **A broker under uid `hermes`.** Returns the token to the identity it is being
  protected from.
- **Loopback TCP with a bearer token.** Reintroduces a secret under the agent's
  uid and adds a second authentication layer to confuse with the first. A unix
  socket gets identity from the kernel with no secret at all.
- **stdio via `sudo -u torio-mcp`.** Hands the agent's uid a privilege-transition
  primitive and moves security into sudoers rules that must be pinned per-argv.
- **Isolating the broker in a container.** Rootful Docker for `hermes` is
  forbidden and no container runtime is installed. A separate uid gives the same
  separation with no new dependency.
- **TLS interception in the guest (a MITM CA).** Would permit content filtering at
  the cost of a trusted CA in the guest. Worse than the disease.
- **`hermes proxy` as the inference broker.** Its adapter registry has no
  `openai-codex` entry, it listens on loopback TCP, and it rejects the client's
  `Authorization` header — it authenticates nobody.
- **`hermes secrets` (Bitwarden / 1Password / `command`).** Fetches the secret
  into the agent's `os.environ` at process start and caches it under
  `<hermes_home>/cache/`. It moves the storage location, not custody.
- **A root-owned `auth.json` unreadable by `hermes`.** `$HERMES_HOME` is owned by
  `hermes` and writes go through tmp + replace inside it, so the first pool write
  silently recreates the file hermes-owned. Worse, a permission failure is caught
  and reported as *"failed to parse … starting with empty store"*,
  indistinguishable from corruption. A boundary that lies about why it broke is
  worse than none.
- **Two holders of the refresh token.** Guaranteed failure, not a hygiene
  preference: the second gets `refresh_token_reused`.
