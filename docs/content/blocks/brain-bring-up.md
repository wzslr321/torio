## Create the Second Brain {#brain-bring-up}

The Brain is a private Markdown vault on the guest at `/home/hermes/brain`,
versioned by a local Git repository and registered with Hermes as its own
project, so any session can search it without you opening it first.

```bash
torio brain init
torio brain status
```

`init` builds the scaffold atomically through private guest staging, makes the
first local commit, registers the Hermes project, and installs the global
`torio-brain` retrieval skill. It is idempotent on state it manages, and it
refuses to touch non-empty data it did not create — so a second run is safe and
an existing vault is never silently absorbed.

It configures no remote and pushes nothing. The Brain stays on the VM.

> If a Hermes Desktop session was already open, restart it. Hermes caches a
> skill's prompt per backend process, so an open session will not see
> `torio-brain` until it reconnects.

### Bring an existing vault in {#brain-import}

If you already keep Markdown notes on your Mac — an Obsidian vault, say —
import them once:

```bash
torio brain import ~/path/to/vault --dry-run   # preflight, transfers nothing
torio brain import ~/path/to/vault
```

Start with `--dry-run`: it runs the full preflight and reports what would move,
without transferring a byte or touching Brain data.

The import is deliberately narrow. It carries Markdown, Canvas, and local
attachments, and refuses or skips credential-shaped files, repository metadata,
links, hardlinks, special files, and executables. Existing data is **never**
overwritten — the one exception is a scaffold Torio itself created and you have
not touched. To land the vault as one contained subtree instead of merging it
into the root, pass `--into notes`.

Output is counts, bytes, and a manifest digest. No note name and no note content
ever appears in it.

### Getting it back out {#brain-out}

Torio brings data in and does not take it out — there is no export command.
Copying the Brain to your Mac is something you do explicitly:

```bash
limactl copy torio:/home/hermes/brain/ ~/torio-brain-copy/
```

That is your command. Nothing verifies the result, and Torio does not call it a
backup.
