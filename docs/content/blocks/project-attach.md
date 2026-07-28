## Attach a repository {#project-attach}

The model can see the repositories you registered, and nothing else. Nothing is
discovered, scanned, or picked up because it happened to be on disk.

```bash
torio project add my-service https://github.com/you/my-service --use
torio project list
```

`add` clones the exact remote into the derived workspace path, gives you and
`hermes` shared access to it, and registers the project with Hermes before it
records anything in config. `--use` makes it the active project. If a valid
checkout of that remote is already at the path, `add` verifies and adopts it
rather than touching it.

**You do not choose the path.** It is always `/home/hermes/projects/<id>`,
derived from the project id — never taken from you, never stored in config.
Without `--id`, the id is the name you gave, which must be a lowercase slug;
pass `--id` to pick a different one.

**Read access is your job.** Torio stores no Git credentials, prompts for none,
and passes none to the model. A remote the guest cannot already read without
prompting fails closed:

```text
torio: project add: auth: the guest cannot read the remote noninteractively; provision access for the hermes user out of band
```

That is exit `7`.

The fix is to grant the guest read access yourself, on the guest, outside Torio
— not to re-run the command. Do not work around it by copying a checkout from
your Mac: a recursive copy drags host Git config, hooks, and keys across the VM
boundary, which is exactly the thing this path exists to prevent.

Nothing on the guest is reset, cleaned, or deleted, so if `add` fails partway a
rerun finishes the work instead of starting over.

### Inspect and forget {#project-inspect}

```bash
torio project show my-service     # registry entry, checkout state, Hermes registration
torio project use my-service      # switch the active project
torio project remove my-service   # forget it
```

`show` reports drift as stable markers rather than repairing it, and returns no
file names, diffs, or raw Git output. `list` reads config only and runs nothing
on the guest, so it works with the VM stopped.

`remove` archives the Hermes project and drops the config entry. **The checkout
is never deleted** — the output tells you where it still is. There is no
`--delete`.
