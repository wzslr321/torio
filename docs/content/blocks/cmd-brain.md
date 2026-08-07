## Command surface — `torio brain` {#brain}

Manages the private Second Brain: a Markdown vault at `/home/hermes/brain`,
versioned by a local Git repository, owned by `hermes`, and registered with
Hermes as its own project so any session can search it. The parent takes no
action itself; an absent or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio brain init` | Create the canonical scaffold atomically through private guest staging, make the first local commit, and register the Hermes project. Then install or refresh the global `torio-brain` retrieval skill so other projects can search the Brain. Idempotent for managed state; refuses to touch non-empty data it did not create. Configures no remote and pushes nothing. |
| `torio brain status` | Report state (`initialized`, `uninitialized`, or drift), the canonical path, native filesystem, ownership and mode, Git worktree state, aggregate counts, Hermes project registration, and skill state. Changes nothing. |
| `torio brain import <host-directory>` | Import an existing Markdown vault through private host and guest staging, verified by checksum on the guest. Accepts `--into SUBDIR` to land the import as one new contained subtree, and `--dry-run` to preflight without transferring anything. |

**Output never contains note names or note content** — not in success output, not
in `error.details`. Every command reports bounded aggregate metadata only: file
counts, total bytes, a manifest digest, and stable drift markers. This is the
Brain's privacy boundary, not a matter of brevity.

`import` refuses or skips credential-shaped files, repository metadata, links,
hardlinks, special files, and executables. Existing data is **never** overwritten
— the single exception is an untouched scaffold that Torio itself created.

Sessions that were already open when `init` ran will not see the retrieval skill:
Hermes caches a skill's prompt per backend process, so restart them.

### Getting the Brain back out {#brain-export}

Torio brings data in and does not take it out. There is no `torio brain export`.
Copying the Brain to your host is an explicit thing you do:

```bash
limactl copy torio:/home/hermes/brain/ <host-destination>/
```

That is your command, not a Torio feature: nothing verifies the result, and
Torio does not call it a backup.

### On a backend that keeps no registry or no skills {#brain-backends}

The vault, its git history and the import pipeline are the same on every
backend: the vault lives in the backend identity's own home, owned by it, and
`brain import` verifies and promotes exactly as it does elsewhere.

Two things are per-backend, and `brain status` reports each as a state rather
than as a fault:

- **Registration.** A backend that keeps no project registry has nothing to
  register the vault with. The vault is reached by path, so nothing is lost.
- **The retrieval skill.** Torio installs it only where the backend discovers
  skills *and* a skill exists that is written for that backend. The one Torio
  ships names another backend's tools and vault path; installing it elsewhere
  would tell an agent to call tools it does not have. The state is then
  `not_applicable`, which is deliberately not `not_installed` — reporting a
  missing thing where nothing is missing teaches operators to ignore the report
  that matters.
