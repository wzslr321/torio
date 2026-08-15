# ADR-0003: Ownership split — Brain, projects, and operator-carried write

- Status: Accepted
- Superseded in part by:
  [ADR-0015](0015-mediated-agent-forwarding.md), for what `ssh -A` puts inside the
  guest: the forwarded agent is Torio's, holds one pinned key, and asks before
  every signature.
- Superseded in part by:
  [ADR-0025](0025-one-second-brain-with-the-host-as-its-hub.md), for where the
  canonical vault is and for whether Brain data leaves a guest. "The canonical
  Brain is `/home/hermes/brain`" now names a replica: the canonical vault is on
  the host, each backend's guest keeps one copy of it, and `brain sync` carries
  Git bundles between them. This record's own condition for that return is met:
  "if a full Brain backup ever becomes a product requirement it deserves its own
  ADR". No host mount is introduced and no vault gains a network remote.
- Date: 2026-08-05
- Consolidates: the onboarding/Brain/multi-project scope decision and the removal
  of `brain export`. The superseded originals are recoverable at
  `git show archive/pre-oss:docs/adr/…` (`0015`, `0018`).
- Applies to: `internal/brain`, `internal/projects`, `internal/lima`,
  `internal/transfer`

## Context

Two things were routinely confused, in code and in documentation. The Hermes
profile (`/home/hermes/.hermes`) is application state; a Second Brain is a
knowledge vault. A field named `HermesKBPath` pointed at the former. Anything
built on that confusion inherits it.

Separately, the boundary that matters most in daily use is write access to a Git
remote. A backend that runs continuously and holds a durable write credential can
push at any time, for any reason, including a reason that arrived inside a
document the agent was asked to read.

## Decision

**The guest holds knowledge and checkouts. Write capability against an origin is
carried by the operator's own session and ends with it.**

### Second Brain

- The canonical vault is `/home/hermes/brain`, on the guest's native filesystem.
  `/home/hermes/.hermes` remains only the Hermes profile. Code and documentation
  must distinguish the two.
- The Brain is filesystem-first, Markdown-first and private. There is no cloud
  sync, no embeddings, no vector database, no automatic connector ingestion.
- Cross-project context reaches the model through a retrieval skill and explicit
  file search. Injecting the whole vault into a prompt, or adding
  `/home/hermes/brain` as a folder of every project, is forbidden — both turn a
  private vault into ambient context.
- `torio brain init`, `status` and `import` exist. Import is a one-shot,
  bounded transfer with no broad host mount; payload content never reaches
  stdout, logs or evidence — only counts, byte totals and a manifest digest.

### Projects

- The registry lives in the host config document and holds only non-secret
  fields: `id`, `display_name`, `remote`.
- The workspace path is always derived as `/home/hermes/projects/<project-id>`.
  The operator does not supply an arbitrary path.
- A remote must carry no password, token, query or fragment. A non-secret SSH
  username (`git@`) is fine. A remote the guest cannot already read without
  prompting fails closed — Torio provisions no credentials and causes no
  credential prompt.
- `project add` clones, or non-destructively adopts a matching guest checkout,
  and registers the project through the public `hermes project` CLI.
- `project remove` forgets the registry entry and leaves the checkout on disk.
  There is no `--delete`.

### Write capability

- The persistent service identity `hermes` has **read** access to an origin and
  nothing more.
- Write capability comes only from a short-lived interactive session:
  `torio project shell <id>` → ordinary Git commands → `exit`.
- That session uses a separate login identity and ephemeral SSH agent forwarding
  (`ssh -A` for the session). A global `ssh.forwardAgent: true` in the Lima
  template is forbidden, and the Hermes service identity never inherits
  `SSH_AUTH_SOCK`.
- Torio stores no Git write token or key and automates no push, merge, tag or
  release.

This invariant is about writes against an origin. It is **not** a claim that no
write capability of any kind exists in the guest; MCP is a separate capability
channel, governed by [ADR-0004](0004-mcp-credential-custody-and-egress.md).

### Data does not leave through Torio

`torio brain export` was removed. It cost roughly 1 260 lines of Go — exclusive
rename on three platforms, host-side manifest verification, a guest manifest
parser, a copy-from-guest path — to produce an incomplete backup: working-tree
only, with `.git` explicitly excluded, so the history the Brain has kept since
`brain init` never came out. A command named `export` implies the data left. Part
of it left. A missing command is visible; an incomplete backup is visible only
when someone tries to restore it.

Getting data out is an explicit operator action:

```bash
limactl copy <instance>:/home/hermes/brain/ <host-dest>/
```

Torio does not perform it, does not verify it, and does not call it a backup.

## Consequences

- The strongest correctness gate in the repository was lost with export:
  import → export → recompute manifest → compare digests. Import is still
  verified by a guest-side checksum of the promoted payload, but nothing
  re-derives the manifest from a copy that came back out. This is a real loss of
  coverage and the largest cost of that decision.
- `torio brain export <anything>` is exit 2, an unknown subcommand. No stub is
  left behind: a command that exists only to refuse is a worse API than no
  command.
- A person who wants their vault on the Mac runs one documented `limactl copy`.
  The claim that the result is a backup belongs to them, like the operation.
- The operator must have an `ssh-agent` holding a key that can push; `project
  shell` preflights this and refuses to open a session without one.

## Rejected

- **Treating `/home/hermes/.hermes` as the Second Brain.** The confusion this ADR
  exists to remove.
- **Bulk injection of the vault into every project's system prompt.**
- **Persistent `forwardAgent`, or sharing `SSH_AUTH_SOCK` with the `hermes`
  process.** Both make write capability durable, which is the thing being
  prevented.
- **Storing a workspace path or any credential in the config document.**
- **`project remove --delete`.** Deleting a checkout on a registry operation is a
  destructive side effect of a bookkeeping command.
- **Keeping `export` and adding `brain.bundle`.** Closes the history gap but needs
  its own transport spike and enlarges the surface this change reduces. If a full
  Brain backup ever becomes a product requirement it deserves its own ADR.
- **Renaming `export` to `copy-out`.** Fixes the misleading name and keeps 1 260
  lines for something one `limactl copy` already does.
- **Keeping the export path only for the round-trip test.** A test that keeps a
  production function alive has stopped being a test.
