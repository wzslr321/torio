## Attach a repository {#project-attach}

Torio manages only repositories in the registry. It discovers nothing from
disk, and removing a registry entry leaves the checkout on the guest.

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

**A private repository takes the same command.** Torio copies no host Git
credential into the guest. A remote the guest cannot read still fails closed,
at exit `7`. For an SSH remote that failure comes with the way through: the
guest generates its own deploy key and prints the public half.

```text
The guest generated a deploy key for this project. Torio holds no copy of its private half.

ssh-ed25519 AAAA…

Add that key to the repository on github.com as a deploy key, with write access off,
then run the same command again. Adding it to your account instead would give
the guest write access to every repository that account can reach.
Private half, on the guest, owned by the backend identity: /home/hermes/.ssh/torio/my-service
```

On GitHub that is the repository's own **Settings → Deploy keys → Add deploy
key**, with **Allow write access** left unchecked. Then run the same `add`
again and the second run clones. A key you authorized before the first run
attaches in one command, and a rerun before you authorize it reports the same
key rather than making another.

Where you paste the key is the whole of what keeps it read-only. Torio cannot
check which you did, because proving a key cannot write would take a push and
Torio runs none. A key added to your account rather than to the repository
attaches the project equally well and leaves the guest able to write everywhere,
which is the one way this path can widen what the VM can do.

The private half is generated on the guest, stays there, and is never read,
copied, or stored by Torio. It is readable by the backend identity, which is the
identity the model runs as, so treat it as a credential that lives where the
agent lives. Push is unaffected: it still travels through the agent you forward
with `project shell`.

A private HTTPS remote has no such path, because reading one takes a stored
credential. Use the SSH remote. Do not work around any of this by copying a
checkout from your host: a recursive copy drags host Git config, hooks, and keys
across the VM boundary, which is exactly the thing this path exists to prevent.

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

**A deploy key is never deleted either**, and unlike a checkout it is not inert.
`remove` reports it as retained and touches nothing: the key stays on the guest
and stays authorized on the forge until you withdraw it there. If you removed the
project because the guest should no longer read that repository, deleting the
deploy key on the forge is the step that makes it true. Deleting the guest file
the output names is what makes the next `add` generate a fresh key, which is also
how you rotate one.
