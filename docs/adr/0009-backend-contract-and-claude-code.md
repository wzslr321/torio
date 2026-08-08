# ADR-0009: One backend per instance, declared by a contract; Claude Code is the second one

- Status: Accepted
- Date: 2026-08-08
- Applies to: `internal/backend`, `internal/lima`, `internal/serve`,
  `internal/projects`, `internal/brain`, `internal/config`, `internal/cli`

## Context

Torio ran one backend. The name was hardwired: the guest user and its paths, the
systemd unit, bootstrap verification, project and Brain registration, readiness
proven by `GET /api/status`. Nothing in the boundary was Hermes-specific — a
service on the guest's own loopback, checkouts the agent owns but cannot push,
write capability carried by the operator's own SSH agent for the length of one
session — but every layer said `hermes` anyway, so the contract existed only as
the shape of one implementation.

The forcing function is a second agent someone runs daily: Claude Code. It fits
the boundary and breaks the implied contract, because it is a *process*, not a
*service*. There is no daemon to install, no loopback endpoint to probe, no
project registry to drive: a project is a directory, trust is granted on first
entry, per-project state is files inside the checkout. Every question Torio's
commands ask a backend has an answer for Hermes and no answer for Claude Code,
and a contract that cannot say "this backend has none of that" would force one
of two lies — a fabricated failure, or a check that passes without checking.

## Decision

### One backend per instance

An instance declares its backend once, at `vm init`, and the declaration lives
in that instance's config document. Multi-backend means multiple Lima instances;
an existing box keeps running what it already runs, because a config that names
no backend means Hermes.

Rejected: a backend per project. Two agent identities would then share
`torio-projects` and contend over the same checkouts, and every custody
statement in ADR-0003 would have to be made twice, per project, for no daily
value. The unit of isolation in this product is the VM; making it the project
would weaken it without making anything easier.

### The operator names the agent, not the box

The rule above is about the guest. The first version of it leaked into the
operator's hands: reaching a second backend meant knowing an instance name,
exporting `TORIO_INSTANCE` on every command, and keeping a separate project
registry per box. That made a daily task — try this repository under the other
agent — into a setup. The isolation was the constraint; the operator paying for
it was an accident of where the config happened to live.

So `--backend NAME` is a global flag that names the agent an invocation is
about, and the instance follows. The mapping is **derived**: the default backend
keeps `torio`, every other is `torio-<backend>`. A recorded mapping — a table of
instance names — would make the operator responsible for a fact Torio can
compute, and would give two places to disagree about which box runs which agent.

`TORIO_INSTANCE` keeps naming a box directly and wins over the flag. It is the
only way to reach an instance whose name Torio did not derive, so a flag must
not be able to redirect an invocation that already named its target. Given both,
the flag and the instance's declaration must agree; an absent declaration is
compared as the default backend rather than as "unset", or `--backend
claude-code` against a legacy Hermes box would read as a match. On an instance
with no document yet — the ordinary state before `vm init` — the flag is the
declaration.

### One registry, one checkout per backend

The project registry moved out of the instance document into `projects.json` in
the config root, shared by every instance under it. A project is something the
operator attached, not something an instance owns, so switching which box a
command talks to must not switch which projects exist. What an instance does own
— the backend it was provisioned for, the settings a command against it runs
under — stays in its own document, and a registry write no longer re-persists
either.

Checkouts are not shared and cannot be: each is owned by one backend's guest
identity, under that backend's workspace root. A registered project therefore
exists in zero or more guests, and `project add <id>` with no remote
materializes it in one more, from the remote on record. It stays a separate step
rather than something `project agent` does on demand, because cloning reaches a
Git remote — the same reason nothing else in this product reaches the network
behind an interactive command.

Migration is a read, not a command. The **default** instance's legacy `projects`
array is used until `projects.json` exists — whichever instance a command
selects, because that is where the one registry lived — and it is left in place
when the first write creates the shared document. Downgrading has to find its
projects where it left them, and reversing this has to be removing one file.

Rejected: two agent identities in one VM, with separate per-identity workspaces.
It buys boot time and disk, and it does not weaken the boundary against the host
— two unprivileged uids on one Linux box is ordinary isolation. What it costs is
that every isolation proof has to be made pairwise, so `claude` not reaching
`hermes`' credential, workspace and socket becomes a standing obligation rather
than something the VM edge already answers. That is paying the clearest sentence
this product has for gigabytes.

Rejected: merging a non-default instance's legacy registry into the shared one.
Those were separate registries per instance, which is the thing being abolished;
choosing what a merge means is not a decision this layer can make safely. The
entries stay in that document, to copy across or leave.

### The contract, and what "declared" means

`internal/backend` states what Torio requires and imports no guest mechanics:
an identity and its paths, a required-path table, and hooks bootstrap runs in a
fixed order — identity, membership, isolation, install-and-pin, version,
guardrails, credential presence. Three capabilities are *declarable*: a project
registry, a guest service, an interactive session. Nil is a first-class answer.

A backend also declares its Brain retrieval skill — where it discovers skills,
and the document installed there. The document travels with the backend for the
same reason the session helper does: it names the tools one agent has and the
vault path one identity owns, so there is no backend-neutral wording to share.
A single shared skill would have to name one backend's tools, and installing it
into another would tell that agent to call tools it does not have.

This is the load-bearing half of the decision, so it is stated as a rule rather
than left to each command:

> Whatever a backend declares, `vm bootstrap` and `serve status` must be able to
> prove. Whatever it declares it has not got, they must not pretend to check.

Concretely: `serve status` against a backend with no service exits 0 and says
the backend declares none, and runs no guest command to discover what it was
already told. `serve install|start|stop|logs` against that backend is a
precondition error naming the backend, because asking Torio to manage a service
that was never declared is an operator mistake, not a broken guest. `project
add` against a backend with no registry clones, verifies and records the
checkout exactly as before and reports that there was nowhere to register it.
A bootstrap check is *recorded* only when it ran; a backend's absent capability
leaves no check behind, so nobody can mistake "there is no such thing here" for
"the thing here is fine".

A backend's steps reach the guest only through the bootstrap run handed to
them. They cannot acquire their own transport, their own truncation policy, or
their own idea of what a recorded check is: truncated output is not evidence
for a backend any more than it is for Torio.

### Two tiers, one contract

A *service backend* (Hermes) installs from a pin, runs a user unit bound to
guest loopback, answers an unauthenticated readiness endpoint, and keeps a
project registry Torio drives through a CLI. A *process backend* (Claude Code)
installs a pinned binary, is reached by opening a session inside a checkout as
its own identity, and keeps its project state as files. The contract is the
union; each backend declares its half.

### Claude Code's custody, stated plainly

- **Identity.** A dedicated guest user, `claude`: no sudo, and its supplementary
  group set is exactly the shared workspace group. Bootstrap proves the absent
  sudo by asking about the identity from a caller that already holds root —
  `sudo -n -l -U claude` — and reading the answer out of the output, not the
  exit code.

  This was first written against the exit code, on the assumption that exit 1
  meant "may run no commands". A real guest disproved it: sudo 1.9.15 exits 0
  for that query whether the identity may run everything or nothing, so the
  check could never pass and the backend could never bootstrap. Asking the
  question *as* `claude` instead does exit 1 — but with "a password is
  required", which is the same 1 a password-gated grant produces, and would
  report OK for exactly the identity this check exists to catch. So the two
  answers are matched positively in the C locale, and anything else, including
  silence, fails closed.

  Rejected: running the agent as the Lima login user. That user holds
  passwordless root on the guest, so an agent running as it would sit *above*
  every control the guest enforces — it could read the credential store it must
  not reach and rewrite the root-owned helpers that gate sessions. An agent
  identity that can become root is not an agent identity.

  Rejected: reusing `hermes`. Two agents under one uid make every custody
  statement ambiguous and every audit unreadable.

- **Install.** A pinned binary, root-owned, in a root-owned directory, reached
  through a root-owned symlink; auto-update off. This is deliberately *stricter*
  than the Hermes path, and the asymmetry is worth naming: `/usr/local/bin/hermes`
  is a root-owned symlink to a file the agent's own uid can rewrite, which
  `SECURITY.md` already records as a known one-step path from the agent to root
  because the login user has passwordless sudo. The Claude backend does not
  inherit that hole; Hermes still has it.

  Rejected: the vendor installer run as root (it installs into the invoking
  user's home and maintains a self-updating versions directory — the opposite of
  a pin) and the npm route (a Node runtime and global-install ownership sprawl
  for a single binary).

- **Credential.** A login performed in the guest, as the `claude` user, so the
  box holds a grant of its own that can be revoked without touching the
  operator's. Bootstrap probes only for its *presence*, offline, and never fails
  a run over it: a box has to bootstrap before anyone can log in to it.

  Rejected: copying the host's credential into the guest. It couples revocation
  to a machine the operator also uses and makes the box's activity
  indistinguishable from their own.

  The credential lives under the agent's own uid. That is the same accepted,
  documented class as the Hermes inference credential in `SECURITY.md`: the
  agent can read what it is authenticated with. It is not fixed here.

- **Push stays human.** The agent commits in the checkout it owns; the operator
  reviews and pushes from `torio project shell`, whose forwarded agent socket
  exists for the length of that session. Invariants 8 and 11 are unchanged.

  Rejected *for now*: a per-session grant handing the forwarded socket to the
  agent identity under an explicit flag. It is a real design with a real ACL
  mechanism, and it changes invariant 11, so it belongs to its own ADR rather
  than arriving as a flag.

- **MCP is a chosen, named hole.** Claude Code is a native MCP client and the
  operator will use it with their own tokens, which then sit under the `claude`
  uid, outside any policy and outside any audit — precisely the shape ADR-0004's
  custody boundary exists to end, and precisely the shape `torio mcp status`
  reports as drift on a Hermes guest. The broker that would fix it does not
  exist: it was deleted rather than shipped dormant (ADR-0008), and issue #2
  records that what blocks it is two undecided contracts, not code.

  So invariant 9's "explicit, enumerable and verified" is **not met for this
  backend**, and this ADR does not pretend otherwise. What Torio provides
  instead is legibility: bootstrap and `torio backend status` enumerate the MCP
  servers the guest is configured with, by name, never by value — a drift
  detector over an agent-writable file, which is what it is called everywhere it
  appears. Revocation is at the provider, by the operator. When the broker lands,
  Claude Code is served by it natively and this paragraph is superseded.

- **Managed settings are a guardrail, not a boundary.** The backend's
  root-owned settings file pins the permission default, disables the
  auto-updater and turns off non-essential traffic. The agent executes arbitrary
  code and could ignore any of it; what the file buys is that the agent cannot
  *silently retune* it, and that a drift is visible. Per the meta-invariant in
  `AGENTS.md`, every mention of it — in code comments, in docs, in this record —
  says drift detector, never boundary. The boundaries are the uid, the exact
  group set, the absent sudo, `ssh.forwardAgent: false`, and the VM edge.

- **Permission prompts are off inside the box.** This looks like a weakening and
  is the opposite: the prompt is a control that lives inside the agent's own
  process, and the box replaced it with controls the agent cannot reach. Torio's
  claim was never that the agent asks first; it is that what the agent can reach
  is bounded by the kernel.

### Where the Hermes implementation lives

It sits beside the guest transport, in `internal/lima`, rather than in its own
package. The guest layout it names is still the layout the MCP custody checks
and the session-path validation in that package are written against, and moving
those is separate work with its own risk. What makes a backend replaceable is
the contract, not the directory. The Claude backend gets its own package
because it shares none of that history.

## Consequences

- The config document gains a `backend` field and a schema version. An older
  binary reading a newer document fails closed on the unknown field. That is the
  desired behaviour: an old binary cannot know that its Hermes-shaped commands
  are pointed at a box running a different agent.
- An unknown backend name is an error that lists the known ones, never a
  fallback to the default. A document naming a backend this build does not have
  must stop, not quietly run every command against a different agent.
- Help text goes backend-neutral; backend-specific facts move into runtime
  output, which knows which backend answered. The JSON envelope gains `backend`
  and capability-declaration keys; the existing Hermes-named keys keep being
  emitted, unchanged, on a Hermes instance.
- `e2e/platform` gains a second, labelled journey. The Hermes journey is
  untouched and stays the default gate.
- The host writes a second document, `projects.json`. It is the first persistent
  host state besides `config.json` since ADR-0001, and it is held to the same
  rules: contained join, mode-private, owned-by-EUID, opened no-follow, strict
  decode, secret shapes refused, crash-safe write with a read-back.
- `project show` and `project remove` carry `registry_declared`, as
  `serve status` carries `service_declared`. When it is false they say nothing
  about a registration: an object of all-falses reads as one that went missing,
  which is a different statement from there being nowhere to register.
- `AGENTS.md` invariants that named `hermes` now name "the agent identity", and
  the meta-invariant gains managed settings as its second worked example.

## Rejected alternatives

Collected above, at the decisions they belong to: a backend per project; two
agent identities inside one VM; a recorded table of instance names instead of a
derived one; merging a non-default instance's legacy registry; the
agent as the Lima login user; reusing the `hermes` identity for a second agent;
the vendor installer as root and the npm install route; copying the host
credential into the guest; a session-scoped push grant (deferred, not refused);
and waiting for the MCP broker before admitting a second backend at all — which
would trade a named, legible hole today for an unbounded delay behind an ADR
whose hard part is a third party's transport and OAuth lifecycle.
