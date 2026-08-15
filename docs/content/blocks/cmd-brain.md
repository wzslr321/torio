## Command surface — `torio brain` {#brain}

Manages the private Second Brain. There is one Brain. Its canonical vault is a
Markdown corpus in a Git repository on the host, under
`${XDG_DATA_HOME:-~/.local/share}/torio/brain/vault`, and each backend's guest
keeps a replica of it in that identity's own home, owned by that identity and,
on a backend that keeps a project registry, registered as its own project so
any session can search it. `torio brain sync` makes a replica and the host
vault agree; `brain status` prints the path on the box you are talking to. The
parent takes no action itself; an absent or unknown subcommand is a usage
error.

| Command | What it does |
| --- | --- |
| `torio brain init` | Create the canonical scaffold atomically through private guest staging, make the first local commit, and register the Hermes project. Then install or refresh the global `torio-brain` retrieval skill so other projects can search the Brain. Idempotent for managed state; refuses to touch non-empty data it did not create. Configures no remote and pushes nothing. |
| `torio brain status` | Report state (`initialized`, `uninitialized`, or drift), the canonical path, native filesystem, ownership and mode, Git worktree state, aggregate counts, Hermes project registration, and skill state. Changes nothing. |
| `torio brain import <host-directory>` | Import an existing Markdown vault through private host and guest staging, verified by checksum on the guest. Accepts `--into SUBDIR` to land the import as one new contained subtree, and `--dry-run` to preflight without transferring anything. The hub offers the same import on `m` on its Brain tab, and always preflights first: what would move is shown, in counts, before a second enter moves it. |
| `torio brain sync` | Reconcile this backend's replica with the vault on the host, both ways, by carrying Git bundles over the same one-shot transport `brain import` uses. Unsaved work in the guest vault is committed first. Neither vault gains a network remote. A merge that cannot be made automatically stops that direction, leaves it as it was, and names the host vault where you resolve it with Git. Counts are reported; note names and content are not. Rebinding the hub runs the same reconciliation on both sides of the move, and its note reports what each carried. |

**Output never contains note names or note content** — not in success output, not
in `error.details`. Every command reports bounded aggregate metadata only: file
counts, total bytes, a manifest digest, and stable drift markers. This is the
Brain's privacy boundary, not a matter of brevity.

`import` refuses or skips credential-shaped files, repository metadata, links,
hardlinks, special files, and executables. Existing data is **never** overwritten
— the single exception is an untouched scaffold that Torio itself created.

Sessions that were already open when `init` ran will not see the retrieval skill:
Hermes caches a skill's prompt per backend process, so restart them.

### The vault on your host {#brain-export}

`torio brain sync` puts the vault on your host, at
`${XDG_DATA_HOME:-~/.local/share}/torio/brain/vault`, and keeps it and this
box's replica reconciled. It is a Git repository holding Markdown, so it is
readable, greppable and backup-able with the tools you already have, and it is
where a merge conflict is resolved.

There is still no `torio brain export`. What sync carries is a Git bundle read
once and removed, in both directions, and neither vault ever gains a network
remote. Backing the host vault up is your decision and your command; Torio
reconciles the copies and makes no claim beyond that.

### On a backend that keeps no registry or no skills {#brain-backends}

The vault, its git history and the import pipeline are the same on every
backend: the vault lives in the backend identity's own home, owned by it, and
`brain import` verifies and promotes exactly as it does elsewhere.

Two things are per-backend, and `brain status` reports each as a state rather
than as a fault:

- **Registration.** A backend that keeps no project registry has nothing to
  register the vault with. The vault is reached by path, so nothing is lost.
- **The retrieval skill.** Each backend ships its own, because a retrieval skill
  names the tools one agent has and the vault path one identity owns: the Hermes
  skill calls `search_files` under `/home/hermes/brain`, the Claude Code skill
  calls `Grep` under `/home/claude/brain`, and the Codex skill searches
  `/home/codex/brain` with the `grep` and `find` the guest image ships. Torio
  installs the one the backend
  declares, at the path that backend discovers skills in, and `brain status`
  names that path. A backend that declares none reports `not_applicable`, which
  is deliberately not `not_installed` — reporting a missing thing where nothing
  is missing teaches operators to ignore the report that matters.
