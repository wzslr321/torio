## Command surface — `torio project` {#project}

Attaches repositories to the guest, inspects them, and forgets them. The parent
takes no action itself; an absent or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio project add <name> [remote]` | Clone the exact remote into the derived workspace path, or verify and adopt a checkout already there; give the operator and the backend identity shared access; register the project where the backend keeps a registry, before recording it. `--id SLUG` picks an id other than `<name>`; `--use` makes it active on success. With the id alone and no remote, materializes an already registered project in the selected backend's guest, using the remote on record. |
| `torio project list` | List the registered projects. Reads config only, runs nothing on the guest, and works with the VM stopped. |
| `torio project show <id>` | Report the registry entry, checkout state, and Hermes registration. Reports drift as stable markers instead of repairing it, and returns no filenames, diffs, or raw Git output. |
| `torio project use <id>` | Make a registered project the active one in Hermes. |
| `torio project remove <id>` | Archive the Hermes project and drop the config entry. The checkout is never deleted, and the output says where it still is. |
| `torio project enter <id>` | Open an ordinary interactive terminal in the checkout with SSH agent forwarding disabled. Interactive, so it does not support `--json`. |
| `torio project agent <id>` | Start the configured backend inside the checkout, running as the backend identity rather than as you. No SSH agent is forwarded and the connection is never multiplexed, so the session cannot reach a Git remote or inherit a connection that can. Interactive; `--json` is a usage error. A backend that declares no interactive session has nothing to open. |
| `torio project shell <id>` | Open an ephemeral operator session in the checkout with your SSH agent forwarded. Interactive, so it does not support `--json`. |

**The workspace path is not an input.** It is always derived as
`<backend workspace>/<id>` — never taken from you, never stored in config. On
the default backend that is `/home/hermes/projects/<id>`. Without `--id`, the id
is `<name>` itself, which must be a lowercase slug.

**One registry, one checkout per backend.** The registry is shared by every
instance, so a project you attached while talking to one backend is on record
for all of them and `project list` says the same thing whichever you select. The
checkouts are not shared and cannot be: each is owned by one backend's guest
identity. `torio project add <id> --backend NAME` clones it into that backend's
guest from the remote already on record — a separate step rather than something
`project agent` does for you, because cloning reaches a Git remote. The two
checkouts are independent working trees; what passes between them is what you
push.

**Torio stores no Git credentials.** A remote the guest cannot read without
prompting fails closed. For an SSH remote, `add` generates a read-only deploy
key on the guest and prints the public half; authorizing it on the forge is a
human act, and the same command run again finishes the attach.

`add` resets, cleans, and deletes nothing on the guest, so a rerun after a
failure finishes the work rather than starting over.

### Three sessions, one of which can push {#project-shell}

`enter` is you without push capability, `shell` is you with it, `agent` is the
backend — which never has it. Only the middle one forwards your SSH agent.

Use `torio project enter <id>` for ordinary editing, checks, and local commits.
The SSH transport disables agent forwarding and connection multiplexing, so it
cannot reuse a push-capable operator connection.

Use `torio project agent <id>` to put the backend to work in the checkout. It
runs as the backend's own guest identity, not as you, on the same transport as
`enter`: no forwarding, no multiplexing. The agent owns the tree and can commit
in it; it has no route to a remote, so what it did is still sitting there for
you to read.

Inside the box the backend runs without permission prompts, and that is not a
weakening. A prompt is a control inside the agent's own process. The box
replaced it with controls the agent cannot reach: an unprivileged identity with
no `sudo`, a closed group set, no credential that reaches a remote, and the edge
of the VM.

The persistent backend service, where a backend runs one, has read access to an
origin and nothing more. `project shell` forwards your SSH agent for exactly as
long as the session lasts, and the capability leaves with you when you exit.

The session is preflighted first — the project registered, the VM
bootstrap-verified, the checkout present with the registered origin and shared
permissions, your local agent holding an identity to forward. **Torio never
test-pushes to prove any of it**, and once you exit it makes no claim about what
you pushed. Check the remote yourself.

The session is not bounded by `--timeout`; you end it.
