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
--timeout DURATION     bounded operation; cannot exceed the policy maximum
--verbose              more redacted diagnostics on stderr
```

That is the full list. All four are real persistent flags, accepted before and
after a subcommand; an unknown flag is rejected (usage, exit 2), never silently
accepted. `--config` resolves to the typed configuration (see
[`config.md`](config.md)) used by the command — it is not merely parsed. A
resolution or validation failure is a usage/schema error (exit 2).

There is no global `--force`. A command may have a narrow, documented recovery
flag, but none may bypass verification or the credential boundary: `vm init` does
not recreate a non-matching instance, `brain import` does not overwrite existing
data, and `project remove` does not delete a checkout.

### `--help` and `--json`

`--help`/`-h` is the one narrow exception to "exactly one JSON envelope on stdout
in `--json` mode". Help is an affordance for a human: it prints usage text to
stdout and exits 0 even when `--json` is given, emitting no envelope. Every other
output in `--json` mode MUST be exactly one envelope.

## Command surface

This is the full list. Any parent (`vm`, `serve`, `brain`, `project`, `mcp`)
without a subcommand, or with an unknown one, is a usage error (exit 2) —
fail-closed, like the root command.

### Informational

```text
torio version [--json]
```

### VM

```text
torio vm init [--cpus N] [--memory SIZE] [--disk SIZE]
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
- `bootstrap` reconciles and verifies the guest: a stable, non-interactive
  `hermes` command and the layout of persistent guest directories on a native
  Linux filesystem. It runs only against an existing target in a verified
  `Running` state, through the typed Lima/execx boundary (fixed argv, no
  `sh -c`, no concatenated strings, bounded and redacted output). It is
  idempotent and narrow: it may install the pinned Hermes Agent launcher and the
  `/usr/local/bin/hermes` PATH symlink, but it does **not** recreate or re-image
  the VM, install a model or provider, accept secrets, or create the backend
  service (that is `serve install`).
- **Docker: `hermes` MUST NOT be in the `docker` group.** Membership is
  root-equivalent on the guest, so rootful Docker for the `hermes` service
  identity is forbidden by
  [ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md).
  `bootstrap` **verifies the absence** of that membership and fails closed if it
  finds it; the provisioning template removes `hermes` from `docker` if the group
  exists. No Docker Engine is installed at all, and `bootstrap` does **not** check
  Docker reachability. A future container runtime requires a rootless,
  hermes-owned design behind its own ADR.
- `bootstrap` **verifies** rather than trusting an exit code: the `hermes` user
  exists; the `torio-projects` group exists with both `hermes` and the operator
  (the Lima login identity) in it; `hermes` is **not** in `docker`;
  `uname -m` is the host profile's guest architecture; `hermes --version` works through the documented stable
  path; `git --version` works; every required path is a directory with the
  expected owner, group and mode on a native Linux filesystem rather than a host
  share; and no broad host mount is present. Any unknown, unverifiable or
  drifted state (architecture, version, ownership, mount) is reported and
  fail-closed (exit 6), never papered over. A rerun is a success only when every
  postcondition is proven.
- The required paths have disjoint roles (constants in
  `internal/lima/bootstrap.go`) — `/home/hermes/.hermes` is **not** a knowledge
  base:

  | Constant | Path | Role |
  |---|---|---|
  | `HermesHome` | `/home/hermes` | home of the service identity |
  | `HermesProfilePath` | `/home/hermes/.hermes` | Hermes profile and application state (`$HERMES_HOME`) |
  | `HermesBrainPath` | `/home/hermes/brain` | Second Brain vault |
  | `HermesWorkspacePath` | `/home/hermes/projects` | shared project workspace |

  `bootstrap` verifies the profile and the Brain **independently**; neither path
  is an alias of the other.
- `bootstrap` runs several bounded guest probes and may install Hermes from
  source; run it with the largest timeout policy allows: `--timeout 10m`
  (`config.MaxTimeout`). Reaching Hermes afterwards stays operator-controlled
  (for example `torio vm ssh -- sudo -u hermes -- hermes --version`).

### Backend

```text
torio serve install
torio serve start|stop|restart|status
torio serve logs [--lines N]
```

- Every `serve` subcommand acts on the guest service the configured backend
  **declares**. A backend that declares none (a process backend, such as Claude
  Code) has no unit to manage: `serve status` exits 0 and reports
  `service_declared:false` without running a single guest command, while
  `install`, `start`, `stop`, `restart` and `logs` fail closed with
  `NO_SERVICE` (exit 3) naming the backend. Asking after a service is a
  question with an answer; asking Torio to manage one that was never declared
  is an operator mistake ([ADR-0009](../adr/0009-backend-contract-and-claude-code.md)).
- `serve install` manages its own **user** service (a custom systemd unit for the
  backend identity). It generates a deterministic `hermes-serve.service` with a
  pinned loopback bind
  (`--host 127.0.0.1 --port 9119`), `HERMES_HOME=/home/hermes/.hermes` and
  `Restart=always`, validates it with `systemd-analyze --user verify` **before
  activation**, then runs `daemon-reload` and `enable`. It ensures `linger` for
  `hermes` so a `Restart=always` service survives without an interactive session
  and across reboots. It is idempotent (an unchanged rerun is `changed:false`),
  accepts no secrets, and does **not** start the backend. The unit write is
  atomic (staging → verify → rename); an invalid unit is never activated. Several
  bounded guest probes — use a larger `--timeout` (for example `--timeout 2m`).
- `serve start`/`restart` start the backend and **verify** readiness: a re-query
  of systemd state (`is-active == active`) **and** an actual
  `GET /api/status == 200` over loopback. An active process with a dead endpoint
  is a failure (exit 6). Both are idempotent. `serve stop` is graceful and
  idempotent (the re-query requires a non-active state) and removes no unit,
  profile or state.
- `serve status` proves **both**: the user-systemd state and actual endpoint
  readiness over loopback. Exit 0 only when `active` and `/api/status == 200`; not
  installed or inactive → exit 3; active with a dead endpoint → exit 6. It
  modifies nothing. Its data carries `backend` and `service_declared` first,
  because those decide whether the remaining fields mean anything: on a backend
  with no service the rest is absent state, not a service that is down.
- `serve logs [--lines N]` returns bounded, redacted journal entries for the unit
  **only** (`journalctl --user -u hermes-serve.service -n N --no-pager`) — scoped
  to the unit and redacted through execx, so it does not expose Torio's own
  configuration. That is not an absolute guarantee: the Hermes backend's own
  stdout and stderr may in principle contain text derived from user data. Treat it
  as a runtime exposure limit, not a formal privacy guarantee.
- `serve` binds the guest loopback. Reaching it from the host is an
  operator-controlled SSH tunnel to the guest's `127.0.0.1:9119` (see the
  [runbook](../runbooks/first-run.md)); `torio` adds no tunnel feature of its own.
  `serve` is the Desktop backend.

### Brain

```text
torio brain init
torio brain status
torio brain import <host-directory> [--into SUBDIR] [--dry-run]
```

The Second Brain is a private Markdown vault under `/home/hermes/brain`,
versioned by its own local Git repository and registered as a separate Hermes
Project ([ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md)).

- **Output never contains note names or note content.** All three commands report
  bounded aggregate metadata only: file counts, total bytes, a manifest digest,
  stable drift markers. This applies to `error.details` as well. It is the Brain's
  privacy boundary, not a matter of brevity.
- `init` creates the canonical scaffold atomically through private guest staging,
  makes the first local commit and registers the Hermes Project. After
  verification it installs or refreshes the global `torio-brain` skill so the
  Brain is searchable from other projects; Hermes caches a skill prompt per
  backend process, so open sessions must be restarted. Idempotent for matching
  managed state; refuses non-empty unmanaged data. It configures no remote and
  pushes nothing.
- `status` reports state (`initialized`/`uninitialized`/drift), the native
  filesystem, ownership and mode, the Git worktree state, aggregates, Hermes
  Project registration and skill state. It modifies nothing.
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
limactl copy torio:/home/hermes/brain/ <host-destination>/
```

Torio does not claim that this is a backup and verifies nothing about it.

### Projects

```text
torio project add <name> <remote> [--id SLUG] [--use]
torio project list
torio project show <id>
torio project use <id>
torio project remove <id>
torio project enter <id>
torio project shell <id>
```

- **A workspace path is not an input.** It is always derived as
  `/home/hermes/projects/<id>`, never accepted from the operator and never stored
  in the config (see [`config.md`](config.md)). Without `--id` the identifier is
  `<name>` itself, which must be a lowercase slug.
- **Torio stores no Git credentials.** A remote the guest cannot already read
  non-interactively is fail-closed (exit 7) — the remedy is a human granting
  access outside Torio, not a retry.
- `add` clones exactly the given remote into the derived path **or** verifies and
  adopts a checkout already there, gives the operator and `hermes` shared access,
  and registers the project with Hermes before writing to the config. Nothing on
  the guest is reset, cleaned or deleted, so a rerun after an error finishes the
  work. `--use` makes the project active on success.
- Whether a Hermes project already holds the slug is decided from command
  **output**, never from an exit code: `hermes project show` has exited both 0
  and non-zero for a project that does not exist, depending on its version. When
  `show` describes nothing, `hermes project list` answers the existence
  question. `list` failing, or naming a slug `show` will not describe, is
  unverifiable state and is fail-closed (exit 6).
- `list` reads only the config and runs no guest command — it works with the VM
  shut down.
- `show` reports the registry entry, the checkout state and Hermes registration.
  It **reports drift as stable markers rather than repairing it** and never
  returns file names, diffs or raw Git output.
- `remove` archives the Hermes Project and drops the config entry. The checkout
  directory is **never** deleted, and the output says where it still is.
  There is no `--delete`.
- `enter` opens an ordinary interactive session in the checkout with agent
  forwarding and SSH multiplexing disabled. That session can edit and commit
  locally but receives no write capability toward the remote. It is preflighted
  like `shell`, except for the local SSH agent, which it neither checks nor reads.
- `shell` opens an ephemeral operator session in the checkout with the SSH agent
  forwarded. **This is the only way write capability toward a Git remote reaches
  the guest**, and it lives exactly until the session exits; the persistent Hermes
  has read-only access to an origin. Write capability arriving through an MCP
  server does not travel this path and does not end with the session — it is a
  separate, explicitly granted channel
  ([ADR-0004](../adr/0004-mcp-credential-custody-and-egress.md)). The session is
  preflighted (project registered, VM bootstrap-verified, checkout present with a
  registered origin and shared permissions, local agent holding an identity to
  forward), but Torio **never performs a test push** to prove anything. The
  session is not bounded by `--timeout`: the operator ends it. Afterwards Torio
  makes no claim about what was pushed — check the remote yourself.
- `enter` and `shell` are interactive and **do not support `--json`**: there is no
  document to emit, so `--json` is a usage error (exit 2) rather than a silently
  ignored flag.

### MCP

```text
torio mcp install
torio mcp status
```

MCP servers are to be reached through a broker running under its own guest
identity `torio-mcp`, so that an upstream credential does not exist under the
identity the agent has a shell as
([ADR-0004](../adr/0004-mcp-credential-custody-and-egress.md)). Torio currently
provisions and verifies the custody boundary that broker needs; it does not yet
deliver the daemon or the upstream transport.

- `install` creates the unprivileged `torio-mcp` identity, its `0700` credential
  store, the `torio-mcp-clients` group and the root-owned policy directory — then
  **proves** the result instead of trusting the exit codes of the commands that
  produced it. Idempotent (`changed:false` on an unchanged run), accepts no
  secrets, and **grants nothing** beyond the client-group membership `hermes`
  needs to open a socket and `torio-mcp` needs to hand its own socket to the
  group. `torio-mcp` never lands in `torio-projects`, and `hermes` never lands in
  the `torio-mcp` group; those are the two mistakes that would void the decision
  while leaving every other check green.
- `install` **installs and activates no daemon.** Upstream transport and OAuth
  lifecycle need their own accepted contract; until that exists, the release
  publishes neither the broker nor the relay binary. The public command
  provisions only the custody boundary a future daemon will need.
- Policy is an explicit operator grant, so `install` neither generates nor guesses
  it. On a fresh guest the first run may create the root-owned policy directory
  and end as a precondition with `changed:true`; the operator then writes at least
  one `/etc/torio-mcp/policy.d/<service>.json` as `root:root 0644` and reruns
  `install`. An empty or invalid policy does not yield an apparently healthy
  boundary with an empty grant.
- `install` **does not block** on credentials left under the Hermes profile. They
  are exactly what the broker exists to eliminate, but refusing to install while
  they are present is a deadlock: the operator cannot build the thing they are
  meant to migrate to. That continuous invariant belongs to `status`.
- When `hermes` has just joined the client group, `install` reports
  `restart_required`. A long-lived process does not acquire a
  group because the group database changed underneath it — the backend keeps what
  it started with until `torio serve restart`.
- `status` **proves and reports; it repairs nothing.** It verifies that the broker
  identity exists, that its credential store is readable by nobody else, that
  `hermes` can open the broker socket but is **not** in the broker's own group, has
  no sudo and no groups outside the managed set (`hermes`, `torio-projects`,
  `torio-mcp-clients`), and that no MCP credential has appeared under the Hermes
  profile. It runs no mutating command.
- It also verifies **the two documents that decide what this custody is for**.
  Policy files must be `root:root 0644` regular files (never symlinks) in a
  directory nobody but root writes to — a policy document the agent can write
  voids the decision while leaving every other check green. Their contents pass
  the same strict parser the broker uses. An absent runtime is a valid state
  regardless of a dormant unit: it is the presence of `/run/torio-mcp`, not of a
  unit file, that triggers daemon verification. When a runtime exists, the exact
  trusted unit must be active, the service set must equal the set of ordinary
  listening sockets exactly, and the running process's policy digest must match
  the verified documents. And `mcp_servers` in `config.yaml` must point only at
  the relay: that file is writable by the agent, so an entry pointing elsewhere is
  an MCP server the broker will never see — no policy, no audit.
- The `mcp_servers` check reads **one shape of YAML and refuses the rest**. A block
  in inline syntax, with an anchor, alias, merge key or tab, or in a second
  document, is reported as drift rather than guessed at. This is not a boundary
  and must not be described as one: the file belongs to the identity the check
  constrains. It detects drift and a hand-run `hermes mcp add` — not an adversary
  writing to the gap between parsers.
- A guest where the broker was never provisioned is an **unmet precondition
  (exit 3)**, not drift. A boundary that has stopped holding is **verification
  failed (exit 6)**. The distinction is part of the contract: an operator who
  simply has not run the installer yet must not get the alarm that means a
  guarantee broke, or they will learn to ignore the one that matters.
- Detecting credentials under the Hermes profile reports **a file count and never
  file names**. The ordinary source of that drift is `hermes mcp add` run directly
  on a managed guest, which authenticates upstream and writes the token back under
  the agent's identity.
- **Tool scope is explicit; secrets are not.** Policy lives in
  `/etc/torio-mcp/policy.d/<service>.json` as `root:root 0644` — readable by the
  agent, unwritable by it. Deny by default; only tools named explicitly pass, with
  no inference from names and no patterns.
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
- `serve install` — an unchanged rerun gives `changed:false`.
- `brain init` — matching managed state is a success with no action.
- `project add` — a rerun after an error finishes the work, because nothing is
  rolled back or cleaned.
- `project remove` — a missing or already archived Hermes Project is not an error.
- `mcp install` — an unchanged rerun gives `changed:false`, like `serve install`.
