## Command surface — `torio project` {#project}

Attaches repositories to the guest, inspects them, and forgets them. The parent
takes no action itself; an absent or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio project add <name> <remote>` | Clone the exact remote into the derived workspace path, or verify and adopt a checkout already there; give the operator and `hermes` shared access; register the project with Hermes before recording it in config. `--id SLUG` picks an id other than `<name>`; `--use` makes it active on success. |
| `torio project list` | List the registered projects. Reads config only, runs nothing on the guest, and works with the VM stopped. |
| `torio project show <id>` | Report the registry entry, checkout state, and Hermes registration. Reports drift as stable markers instead of repairing it, and returns no filenames, diffs, or raw Git output. |
| `torio project use <id>` | Make a registered project the active one in Hermes. |
| `torio project remove <id>` | Archive the Hermes project and drop the config entry. The checkout is never deleted, and the output says where it still is. |
| `torio project enter <id>` | Open an ordinary interactive terminal in the checkout with SSH agent forwarding disabled. Interactive, so it does not support `--json`. |
| `torio project shell <id>` | Open an ephemeral operator session in the checkout with your SSH agent forwarded. Interactive, so it does not support `--json`. |

**The workspace path is not an input.** It is always derived as
`/home/hermes/projects/<id>` — never taken from you, never stored in config.
Without `--id`, the id is `<name>` itself, which must be a lowercase slug.

**Torio stores no Git credentials.** A remote the guest cannot already read
without prompting fails closed; the fix is a human granting access on the guest,
outside Torio, not a retry.

`add` resets, cleans, and deletes nothing on the guest, so a rerun after a
failure finishes the work rather than starting over.

### Routine terminals and push capability are separate {#project-shell}

Use `torio project enter <id>` for ordinary editing, checks, and local commits.
The SSH transport disables agent forwarding and connection multiplexing, so it
cannot reuse a push-capable operator connection.

The persistent Hermes backend has read access and nothing more. `project shell`
forwards your SSH agent for exactly as long as the session lasts, and the
capability leaves with you when you exit.

The session is preflighted first — the project registered, the VM
bootstrap-verified, the checkout present with the registered origin and shared
permissions, your local agent holding an identity to forward. **Torio never
test-pushes to prove any of it**, and once you exit it makes no claim about what
you pushed. Check the remote yourself.

The session is not bounded by `--timeout`; you end it.
