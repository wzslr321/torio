# CLI contract

> This document is **normative**: it describes the command surface of the
> delivered binary. A disagreement with the binary's behaviour is a defect to
> fix, not a record to preserve —
> [ADR-0005](../adr/0005-repository-and-documentation-governance.md). Scope is
> set by
> [ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md).

## Binary and output

The binary is `torio`. By default it writes human-readable output to stdout and
diagnostics to stderr. `--json` returns exactly one JSON document on stdout and
never mixes logs into it.

### JSON envelope

```json
{
  "schema_version": "1",
  "ok": true,
  "command": "vm.status",
  "data": {},
  "warnings": [],
  "error": null
}
```

An error:

```json
{
  "schema_version": "1",
  "ok": false,
  "command": "project.add",
  "data": null,
  "warnings": [],
  "error": {
    "code": "CONFLICT",
    "message": "project id is already registered",
    "details": {
      "notes": "cloned,shared"
    }
  }
}
```

`command` is the same on success and on failure of the same command, so a machine
caller does not have to guess what failed.

`message` **and every value in `details`** must not contain credentials, raw
environment or full command lines carrying secrets; the final renderer redacts
both by known shapes as a last line of defence.

`warnings` is currently always an empty array: no command has a non-fatal
condition that does not already belong in `data`. The field stays so a caller
parsing the envelope can rely on it being present.

## Exit codes

| Exit | Class | Example |
|---:|---|---|
| 0 | success / idempotent success | a matching existing VM on `vm init` |
| 2 | usage / schema validation | missing argument, invalid config, unknown flag |
| 3 | unmet precondition | VM stopped, backend not installed, Brain absent |
| 5 | stale / conflict | id or remote already taken |
| 6 | verification failed | bootstrap drift, endpoint does not answer 200 |
| 7 | permission / capability denied | the guest may not read the remote |
| 8 | external dependency failed | no `limactl`, non-zero exit from a guest command |
| 9 | reconciliation required | guest work succeeded, the registry write did not |

Code **4** is produced by no command. It stays unused rather than being
reassigned: recycling a code would silently change the meaning of an existing 4.

## Global flags

```text
--json                 machine output
--config PATH          explicit non-secret config
--backend NAME         the agent this invocation is about; selects its instance
--timeout DURATION     bounded operation; cannot exceed the policy maximum
--verbose              more redacted diagnostics on stderr
```

That is the full list. All five are real persistent flags, accepted before and
after a subcommand; an unknown flag is rejected (usage, exit 2), never silently
accepted. `--config` resolves to the typed configuration (see
[`config.md`](config.md)) used by the command — it is not merely parsed. A
resolution or validation failure is a usage/schema error (exit 2).

There is no global `--force`. A command may have a narrow, documented recovery
flag, but none may bypass verification or the credential boundary: `vm init` does
not recreate a non-matching instance, `brain import` does not overwrite existing
data, and `project remove` does not delete a checkout.

### `--backend` and the instance it selects

One instance runs one agent identity, and that has not changed. What `--backend`
changes is who has to remember which box that is: the operator names the agent,
and the instance follows.

The mapping is derived, not recorded. Every backend derives `torio-<backend>`,
and the default backend is `claude-code`, so an unflagged command talks to
`torio-claude-code`. The bare `torio` instance is deliberately unclaimed: it was
the instance of the removed backend, and re-pointing it would hand a box built
for one identity to another ([ADR-0028](../adr/0028-the-hermes-backend-is-removed.md)).
There is no table of instance names to maintain and so no second place that can
disagree about which box runs which agent.

`TORIO_INSTANCE` still names a box directly and **wins over the flag**. It is the
only way to reach an instance whose name Torio did not derive — a test VM, or a
second box running the same backend — so a flag must not be able to redirect an
invocation that already named its target. Given both, the instance's declared
backend and the flag must agree; they cannot on a derived instance, and a
disagreement on a named one is a usage error (`BACKEND_MISMATCH`, exit 2) rather
than a guest built for one identity being driven as another. An instance with no
config document yet declares nothing, which is the ordinary state before
`vm init` — there the flag is the declaration.

### The project registry is shared, the checkouts are not

The registry lives at `projects.json` in the config root and is read by every
instance under it. A project is something the operator attached, not something an
instance owns, so switching which box a command talks to does not switch which
projects exist. Everything an instance does own — the backend it was provisioned
for, the settings a command against it runs under — stays in that instance's own
`config.json`.

Checkouts do not follow, and cannot: each is owned by one backend's guest
identity, under that backend's workspace root. So a registered project exists in
zero or more guests, and `project add <id> --backend NAME` materializes it in one
more, using the remote already on record. That is a separate step rather than
something `project agent` does on demand, because cloning reaches a Git remote.

An installation that predates the shared registry keeps its projects in the
instance document's `projects` array; that array is read until `projects.json`
exists and is **not deleted** when it does. Reversing the migration is removing
one file.

### `--help` and `--json`

`--help`/`-h` is the one narrow exception to "exactly one JSON envelope on stdout
in `--json` mode". Help is an affordance for a human: it prints usage text to
stdout and exits 0 even when `--json` is given, emitting no envelope. Every other
output in `--json` mode MUST be exactly one envelope.

## Command surface

This is the full list. Any parent (`vm`, `brain`, `project`, `mcp`)
without a subcommand, or with an unknown one, is a usage error (exit 2),
fail-closed. An unknown command is a usage error at the root as well.

The root with **no** command has one carve-out, and only the root has it
(ADR-0019). Where standard input and standard output are both a terminal and
`--json` was not given, it opens the interactive hub and exits 0 when the
operator quits. Everywhere else it is the usage error it has always been, with
the same message and the same exit code: a piped invocation, a job with no
terminal, and a `--json` caller all read
`torio: no command given; run 'torio --help'` on stderr and exit 2.

### Informational

```text
torio version [--json]
torio ui
torio status [--json] [--format table|tmux|prompt]
torio status setup tmux|zsh [--json]
```

`ui` names the hub that bare `torio` opens on a terminal, so a wrapper or a
keybinding can ask for it explicitly.

- It is interactive and emits no JSON. `--json` is a usage error (exit 2)
  rather than an empty envelope, for the reason every interactive command
  refuses it: there is no document to emit.
- Where standard input and standard output are not both a terminal it is a
  precondition failure (`NOT_A_TERMINAL`, exit 3), not a usage error. The
  command is spelled correctly and would work on a terminal; what is missing is
  the machine's, not the operator's.
- It runs the same operations the individual commands run and adds none of its
  own. Every machine-readable answer stays with the command that produces one.
- It silences the diagnostic logger while it owns the screen, so `--verbose`
  has no effect on it.

`status` is the only command that answers across boxes. Every other command
addresses the one instance this invocation selected; this one polls every box
Torio owns and reports, per box, whether it is running, which backend it was
provisioned for, what that backend has running, whether anything there is
waiting on a human, and when it last provably did work.

- Human output is a header line and one row per box, padded into columns:
  `INSTANCE  BOX  BACKEND  SESSION  WAITING  PROGRESS`. A host with no boxes
  prints `no instances` and a next step.
- Every field is one of three things, and never a fourth: a proven value, `?`
  for a question that was asked and could not be answered, or `—` for one the
  box's backend does not answer at all. Absence is never rendered as a zero.
- It exits **0 whenever the poll completes**. A box that could not be reached, a
  config document that could not be read, a fact that could not be proven — each
  costs one field and nothing else. Only failing to list the boxes at all is an
  error (exit 8), because then there is nothing to report on.
- The poll covers the default instance, every instance whose name Torio derived
  from a backend (`torio-<backend>`), and the instance `TORIO_INSTANCE` names
  for this invocation. A box named directly by any other means is outside it.
- `--config` does **not** redirect what is read here. Each box's backend comes
  from the document that box owns, because a poll that read one document for
  every box would report them all as running the same agent.
- It is not a replacement for the per-box commands: `torio backend status`
  answers one box's bootstrap checks in full, and `torio serve status` answers
  whether one box's guest service is ready.

`--format` collapses the same report onto one line for a surface that is glanced
at rather than read: `tmux` carries tmux's own `#[...]` style sequences, `prompt`
carries none at all, because a shell counts the characters in a prompt to place
the cursor.

- `--json` and a non-default `--format` are a usage error (exit 2). The envelope
  is the machine contract and a line is a rendering of it; asking for both would
  break the single-envelope rule, and choosing one silently would leave the
  operator to discover which.
- A poll that could not complete prints `torio: ?` on the line **and still exits
  non-zero**. The exit code is unchanged; the line exists because a surface
  refreshed on a timer shows whatever arrives, and an empty one there reads as a
  quiet host.
- Both line formats are rendered from the same document `--json` carries. They
  are two opinions Torio maintains, not the interface — that is the document.

`status setup` prints the configuration that puts one of those lines on a
surface, and prints **only**: it writes no file, and no flag makes it. A dotfile
belongs to the operator, and the rule is the one `vm bootstrap` holds about a
managed file it did not install — report, never repair in place.

- The snippet calls the binary by the path of the executable that printed it,
  not by name, because an older `torio` earlier on `PATH` exits 2 into an empty
  surface with no error on any stream.
- The zsh snippet works with the shell's default prompt options. Each shell gets
  an unpredictable private cache file; a background refresh is coalesced while
  one is running, and `precmd` reads only the last completed poll. The prompt
  may therefore show the previous refresh after a very short command, but it
  never waits for a VM and never renders a half-written line.
- An unknown surface, or no surface, is a usage error (exit 2).
- Under `--json` it is one envelope carrying `surface` and `configuration`, so
  machine mode stays machine mode.

The document `--json` carries, the probe a backend declares to be included in
it, and the waiting-marker convention are specified in
[`status.md`](status.md).

### VM

```text
torio vm init [--backend NAME] [--cpus N] [--memory SIZE] [--disk SIZE]
torio vm start
torio vm stop
torio vm bootstrap
torio vm status
torio vm ssh -- COMMAND...
```

- `init` creates the VM from the embedded, pinned template, or succeeds
  idempotently when an existing instance matches the trusted pins (image digest
  and URL, `mounts: []`, `ssh.forwardAgent=false`, and the host profile's
  hypervisor driver and guest architecture — `vz`/`aarch64` on macOS,
  `qemu`/`x86_64` on Linux; see ADR-0002). A
  non-matching instance is **fail-closed** (exit 6): there is no `--force`, and
  Torio never recreates, resets or deletes an existing VM.
- The global `--backend NAME` both selects the instance and, on `init`, is
  recorded in that instance's config before the VM is created. It defaults to
  `claude-code`. A document written before the field existed declares nothing and
  meant the removed backend, so it is refused with an error naming the removal
  rather than resolved to a live agent. A rerun
  without the flag keeps the declaration; a rerun naming a *different* backend
  is a usage error (`BACKEND_MISMATCH`, exit 2), because the guest is
  provisioned for one agent identity and re-declaring it would leave a guest
  built for one being driven as another. `torio vm init --backend NAME` is
  therefore how a second backend gets its box: it builds one rather than
  converting the one you have
  ([ADR-0009](../adr/0009-backend-contract-and-claude-code.md)).
- `--cpus`/`--memory`/`--disk` size the VM at **creation**; defaults are 4 vCPU,
  `8GiB` and `60GiB`. These are the only operator values substituted into the
  template besides the login identity, and `--memory`/`--disk` must be single
  tokens. Because `init` never recreates, changing them after creation means
  removing the instance outside Torio.
- Every other template field is fixed. Changing one requires a new ADR, not a
  flag.
- `stop` removes no VM and no data. It is idempotent (already `Stopped` → exit 0)
  and does not trust a clean exit code: after `limactl stop` it re-queries and
  requires state `Stopped`, otherwise fail-closed (exit 3). It never uses
  `--force`.
- `bootstrap` reconciles and verifies the declared backend and its persistent
  guest layout on a native Linux filesystem. It runs only against an existing
  target in a verified `Running` state, through the typed Lima/execx boundary
  (fixed argv, no `sh -c`, no concatenated strings, bounded and redacted output).
  It is idempotent and narrow: it may install the backend's pinned runtime, but
  does **not** recreate or re-image the VM, install a model or provider, accept
  secrets, or create a service (that is `serve install`). Backend-specific
  checks are declared by the backend and reported in the result.
- **Docker: a backend identity MUST NOT be in the `docker` group.** Membership
  is root-equivalent on the guest, so rootful Docker for an agent identity is forbidden by
  [ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md).
  `bootstrap` **verifies the absence** of that membership and fails closed if it
  finds it; the provisioning template removes the agent identity from `docker` if
  the group exists. No Docker Engine is installed at all, and `bootstrap` does
  **not** check Docker reachability. A future container runtime requires a
  rootless, agent-owned design behind its own ADR.
- `bootstrap` **verifies** rather than trusting an exit code: the backend
  identity exists, reaches `torio-projects` with the operator, is outside
  `docker`, and satisfies its declared runtime checks; `uname -m` is the host
  profile's guest architecture; `git --version` works; every backend-required
  path has the expected owner, group and mode on a native Linux filesystem rather
  than a host share; and no broad host mount is present. Any unknown, unverifiable or
  drifted state (architecture, version, ownership, mount) is reported and
  fail-closed (exit 6), never papered over. A rerun is a success only when every
  postcondition is proven.
- Backend-required paths have disjoint roles, and each backend declares its own
  (`Identity` in `internal/backend`). The profile is **not** a knowledge base.
  For Claude Code:

  | Field | Path | Role |
  |---|---|---|
  | `Home` | `/home/claude` | home of the agent identity |
  | `ProfilePath` | `/home/claude/.claude` | agent profile and application state |
  | `BrainPath` | `/home/claude/brain` | Second Brain vault |
  | `WorkspacePath` | `/home/claude/projects` | shared project workspace |

  Bootstrap verifies the profile and the Brain **independently**; neither path
  is an alias of the other.
- `bootstrap` runs several bounded guest probes and may install a backend from
  source; run it with the largest timeout policy allows: `--timeout 10m`
  (`config.MaxTimeout`).

### Backend

```text
torio backend status
torio backend login
```

- `status` reports the backend identity, its installed version, credential state
  and declared capabilities. It verifies state and never repairs it.
- `login` opens the backend's own interactive login flow as its guest identity.
  It is interactive and rejects `--json`; no SSH agent is forwarded.

### Brain

```text
torio brain init
torio brain status
torio brain import <host-directory> [--into SUBDIR] [--dry-run]
```

The Second Brain is a private Markdown vault under the declared backend's Brain
path, versioned by its own local Git repository and registered with the backend
when it declares a project registry
([ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md)).

- **Output never contains note names or note content.** All three commands report
  bounded aggregate metadata only: file counts, total bytes, a manifest digest,
  stable drift markers. This applies to `error.details` as well. It is the Brain's
  privacy boundary, not a matter of brevity.
- `init` creates the canonical scaffold atomically through private guest staging,
  makes the first local commit and registers it where the backend declares a
  registry. After verification it installs or refreshes that backend's declared
  `torio-brain` skill surface. Idempotent for matching managed state; refuses
  non-empty unmanaged data. It configures no remote and pushes nothing.
- `status` reports state (`initialized`/`uninitialized`/drift), the native
  filesystem, ownership and mode, the Git worktree state, aggregates, declared
  project-registration state and skill state. It modifies nothing.
- `import` moves allowlisted files (Markdown, Canvas, local attachments) through
  private host and guest staging, verified by a guest-side checksum.
  Credential-shaped files, repository metadata, symlinks, hardlinks, special
  files and executables are rejected or skipped. Existing data is **never**
  overwritten — the sole exception being an exactly pristine Torio scaffold.
  `--into` imports as one new contained subtree, which is how a collision is
  avoided; `--dry-run` performs preflight without transfer and without changing
  Brain data.

**Torio brings data in and does not take it out.** `brain export` does not exist
([ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md)). Copying
the Brain to the host is an explicit operator action:

```bash
limactl copy torio-claude-code:/home/claude/brain/ <host-destination>/
```

Torio does not claim that this is a backup and verifies nothing about it.

### Projects

```text
torio project add <name> [remote] [--id SLUG] [--use]
torio project list
torio project show <id>
torio project use <id>
torio project sync <id>
torio project remove <id>
torio project agent <id> [--push-grant]
torio project enter <id>
torio project shell <id>
```

- **A workspace path is not an input.** It is always derived from the configured
  backend's workspace and the identifier — `/home/claude/projects/<id>` on
  Claude Code, `/home/codex/projects/<id>` on Codex — never accepted from the
  operator and never stored in the config (see [`config.md`](config.md)).
  Without `--id` the identifier is `<name>` itself, which must be a lowercase
  slug.
- **No backend keeps a project registry.** A project is a directory the agent is
  started in, so `add` clones, verifies and records the checkout and there is
  nothing else to register it with ([ADR-0028](../adr/0028-the-hermes-backend-is-removed.md)).
  ([ADR-0009](../adr/0009-backend-contract-and-claude-code.md)).
- **Torio holds no Git credential on the host.** A remote the guest cannot read
  non-interactively is fail-closed (exit 7). For an SSH remote, `add` generates a
  deploy key owned by the backend identity and offers it to that remote alone;
  the exit-7 error then carries `deploy_key` details (`public_key`, `host`,
  `key_path`, `generated`) and the human path prints the public key on stderr.
  Authorizing it on the forge is a human act, after which the same command
  succeeds. A rerun before authorization reports the same key and does not
  generate another. The private half is never read, transported or stored by
  Torio. The key is read-only only if it is added to the repository as a deploy
  key without write access; Torio asserts no more than that, because verifying
  it would take a push it does not run
  ([ADR-0018](../adr/0018-guest-held-deploy-key-for-read-access.md)).
- **Deploy key state is reported in `notes`**, never in prose parsed from output:
  `deploy_key_generated` (this run created it), `deploy_key_pending_authorization`
  (one was already held and the remote still does not read),
  `deploy_key_used` (the attach reached the remote through it), and
  `deploy_key_retained` on `remove`. Removal forgets a project and deletes no
  key: the file stays on the guest and the authorization stays on the forge
  until an operator withdraws it there.
- `add` clones exactly the given remote into the derived path **or** verifies and
  adopts a checkout already there, gives the operator and the backend identity
  shared access, and registers the project where the backend keeps a registry
  before writing to the config. Nothing on the guest is reset, cleaned or
  deleted, so a rerun after an error finishes the work. `--use` makes the
  project active on success.
- `add <id>` with **no remote** materializes an already registered project in
  the selected backend's guest, from the remote on record. The remote is read
  rather than retyped: a typo would put a different repository behind an
  identifier that already means something. An unregistered id has nothing to
  complete from and is a usage error (exit 2).
- `sync` reconciles a project that has **no remote** with a bare repository on
  the host at `${XDG_DATA_HOME:-~/.local/share}/torio/projects/<id>.git`, which
  is where that project's boxes meet
  ([ADR-0029](../adr/0029-a-local-project-reaches-every-box-through-the-host.md)).
  Each side writes a Git bundle, the one-shot transport carries the file, and
  the other side reads refs out of it: nothing reaches a network and neither
  repository gains a remote. **A ref moves only forward** — it is written only
  where the value the other side holds is an ancestor of the one arriving, and a
  ref that moved on both sides is reported in `diverged` and left exactly as it
  was. Uncommitted work is never committed and never written over: the branch
  the checkout stands on moves through the worktree with `merge --ff-only`, so
  Git itself decides whether the tree can take it, and a refusal is reported in
  `held_back` rather than forced. A project that has a remote
  is refused (exit 4): its boxes already meet there. The host path is derived
  from the id and is never recorded, so every registry entry still means the
  same thing on every machine
  ([ADR-0023](../adr/0023-recorded-remotes-are-resolvable-from-a-guest.md)).
- `add <id>` with no remote, for a project the record says is **local**,
  materializes the checkout from that host repository where one exists. It is
  the third source a session may draw a checkout from, after the remote on
  record and a carried bundle
  ([ADR-0024](../adr/0024-a-session-materializes-the-checkout-it-needs.md)).
  With none, the refusal names all three ways of getting one.
- `list` reads only the config and runs no guest command — it works with the VM
  shut down.
- `show` reports the registry entry and the checkout state.
  It **reports drift as stable markers rather than repairing it** and never
  returns file names, diffs or raw Git output.
- `remove` drops the config entry. The checkout
  directory is **never** deleted, and the output says where it still is.
  There is no `--delete`.
- `agent` starts the configured backend in the checkout, running as the
  **backend's** guest identity rather than the operator's. The transport is
  `enter`'s — agent forwarding and multiplexing both disabled — so an agent
  session can neither reach a Git remote nor inherit a connection that can. The
  remote argv is the backend's root-owned helper plus one validated project
  path; the command the helper runs is a constant inside it. A backend that
  declares no interactive session answers `BACKEND_NO_SESSION` (exit 3): a
  service backend's surface is its service, not a terminal
  ([ADR-0009](../adr/0009-backend-contract-and-claude-code.md)).
- `enter` opens an ordinary interactive session in the checkout with agent
  forwarding and SSH multiplexing disabled. That session can edit and commit
  locally but receives no write capability toward the remote. It is preflighted
  like `shell`, except for the local SSH agent, which it neither checks nor reads.
- `--push-grant` opens the agent session with the mediated agent reachable, so
  the session may **ask** to push. Every signature it asks for waits for a
  confirmation on the host and is recorded before it is made; an unanswered
  dialog denies. It requires `operator_key`: without a pinned key there is
  nothing to mediate, and a socket handed over with nothing in front of it is the
  design that was rejected
  ([ADR-0016](../adr/0016-session-scoped-push-grant.md)). The grant is per
  invocation — no config field turns it on, and nothing remembers the last time
  it was used.

  It also refuses a remote the grant could not be used against: an origin that
  pushes over HTTPS never consults an SSH agent, and a host whose key is not in
  the **agent identity's** `known_hosts` stops a push before it reaches the key
  at all. Both are reported before the session opens, with the remedy, rather
  than at the end of one.
- `shell` opens an ephemeral operator session in the checkout with an SSH agent
  forwarded. It lives exactly until the session exits; the agent identity itself
  has read-only access to an origin. **What is forwarded is Torio's own agent when
  `operator_key` is set**: one pinned key, a confirmation on the host before
  every signature, and a decision log beside the config document
  ([ADR-0015](../adr/0015-mediated-agent-forwarding.md)). With no key pinned the
  operator's agent is forwarded whole, as it always was. Write capability arriving through an MCP
  server does not travel this path and does not end with the session — it is a
  separate, explicitly granted channel
  ([ADR-0004](../adr/0004-mcp-credential-custody-and-egress.md)). The session is
  preflighted (project registered, VM bootstrap-verified, checkout present with a
  registered origin and shared permissions, local agent holding an identity to
  forward), but Torio **never performs a test push** to prove anything. The
  session is not bounded by `--timeout`: the operator ends it. Afterwards Torio
  makes no claim about what was pushed — check the remote yourself. The decision
  log says what a mediated session was allowed to sign, which is a different and
  smaller statement.
  On opening, both `shell` and a granted `agent` session print what the checkout
  held at that moment — the branch and how far ahead of its upstream it was. It
  is a snapshot, not a claim about what follows.
- `agent`, `enter` and `shell` are interactive and **do not support `--json`**:
  there is no document to emit, so `--json` is a usage error (exit 2) rather than
  a silently ignored flag.

### MCP

```text
torio mcp install
torio mcp login <service>
torio mcp status
```

MCP servers are reached through a broker running under its own guest
identity `torio-mcp`, so that an upstream credential does not exist under the
identity the agent has a shell as
([ADR-0004](../adr/0004-mcp-credential-custody-and-egress.md),
[ADR-0012](../adr/0012-mcp-broker-transport-and-oauth.md),
[ADR-0013](../adr/0013-mcp-managed-client-config-and-activation.md)).

- `install` creates the unprivileged `torio-mcp` identity, its `0700` credential
  store, the `torio-mcp-clients` group and the root-owned policy directory — then
  **proves** the result instead of trusting the exit codes of the commands that
  produced it. Idempotent (`changed:false` on an unchanged run), accepts no
  secrets, and grants the selected backend identity (`claude` or `codex`) only
  client-group access to broker sockets. `torio-mcp` never lands in
  `torio-projects`, and the agent never lands in the `torio-mcp` group.
- The release carries guest-Linux broker and relay payloads beside the host
  binary. `install` verifies them, writes them and the systemd unit atomically as
  root-owned files, reloads systemd and configures the selected backend with one
  credential-free relay entry per policy service. The unit remains stopped until
  every policy service has completed login.
- Policy is an explicit operator grant, so `install` neither generates nor guesses
  it. On a fresh guest the first run may create the root-owned policy directory
  and end as a precondition with `changed:true`; the operator then writes at least
  one `/etc/torio-mcp/policy.d/<service>.json` as `root:root 0644` and reruns
  `install`. An empty or invalid policy does not yield an apparently healthy
  boundary with an empty grant.
- `install` **does not block** on credentials left under the agent profile. They
  are exactly what the broker exists to eliminate, but refusing to install while
  they are present is a deadlock: the operator cannot build the thing they are
  meant to migrate to. That continuous invariant belongs to `status`; revoke a
  migrated native provider grant upstream.
- For Claude Code, Torio installs root-owned
  `/etc/claude-code/managed-mcp.json`, pins `allowManagedMcpServersOnly: true`,
  and removes native MCP declarations from agent-owned `.claude.json`. For Codex,
  Torio installs root-owned `/etc/codex/requirements.toml`, whose `mcp_servers`
  allowlist permits only the relay path carrying one named service, and writes
  the declarations themselves through `codex mcp add`; an empty allowlist is
  written when the policy grants nothing, because an absent one permits
  everything. `status` reads `codex mcp list --json`, so a declaration the
  allowlist disabled is reported rather than counted as configured.
- When the selected identity has just joined the client group, `install` reports
  `restart_required`. A long-lived process does not acquire a
  group because the group database changed underneath it — the backend keeps what
  it started with until the agent session is reopened.
- `login <service>` is interactive and does not support `--json`. It opens one
  explicitly loopback-bound SSH local forward for the OAuth callback, disables
  agent forwarding and multiplexing, and runs the fixed broker login command as
  `torio-mcp`. Torio accepts no token, client secret or bearer-token file. OAuth
  discovery, dynamic client registration, PKCE S256, exchange and refresh use
  the pinned official MCP Go SDK. After the last required login Torio enables
  and starts the broker unit.
- `status` **proves and reports; it repairs nothing.** It verifies that the broker
  identity exists, that its credential store is readable by nobody else, that
  the selected agent can open the broker socket but is **not** in the broker's
  own group, has no sudo and no group outside the managed set. For Claude it
  rejects any native MCP declaration and proves the root-owned managed-only
  configuration. It runs no mutating command.
- It also verifies **the two documents that decide what this custody is for**.
  Policy files must be `root:root 0644` regular files (never symlinks) in a
  directory nobody but root writes to — a policy document the agent can write
  voids the decision while leaving every other check green. Their contents pass
  the same strict parser the broker uses. While any policy service still requires
  login, an absent runtime is the valid dormant state. Once OAuth state is
  complete, the runtime is required: the exact trusted unit must be active, the
  service set must equal the set of ordinary listening sockets exactly, and the
  running process's policy digest must match the verified documents.
- A guest where the broker was never provisioned is an **unmet precondition
  (exit 3)**, not drift. A boundary that has stopped holding is **verification
  failed (exit 6)**. The distinction is part of the contract: an operator who
  simply has not run the installer yet must not get the alarm that means a
  guarantee broke, or they will learn to ignore the one that matters.
- **Tool scope is explicit; secrets are not.** Policy lives in
  `/etc/torio-mcp/policy.d/<service>.json` as `root:root 0644` — readable by the
  agent, unwritable by it. Deny by default; only tools named explicitly pass, with
  no inference from names and no patterns.
- Streamable HTTP is the only delivered upstream transport. Before publishing a
  socket the broker enumerates upstream tools and intersects them with the exact
  root-owned grant. Calls are audited without arguments or results; missing peer
  uid or an unwritable audit fails closed.
- The broker **does not fully prevent a confused deputy**: an injected instruction
  can use any tool the policy grants, including a writing one, against any
  permitted target. Granting a write stays an explicit operator decision recorded
  in root-owned policy rather than a side effect of installation.

## Idempotency

Every state-changing command is idempotent, and idempotent success is exit 0:

- `vm init` — a matching existing instance gives `created:false`. A non-matching
  one is fail-closed, never a recreate.
- `vm start`/`stop`, `serve start`/`stop`/`restart` — the desired state is
  **re-queried** after the action; a clean exit code is not itself a
  postcondition.
- `brain init` — matching managed state is a success with no action.
- `project add` — a rerun after an error finishes the work, because nothing is
  rolled back or cleaned.
- `mcp install` — an unchanged rerun gives `changed:false`.
