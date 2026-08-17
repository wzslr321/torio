## Command surface — `torio project` {#project}

Attaches repositories to the guest, inspects them, and forgets them. The parent
takes no action itself; an absent or unknown subcommand is a usage error.

| Command | What it does |
| --- | --- |
| `torio project add <name> [remote]` | Clone the exact remote into the derived workspace path, or verify and adopt a checkout already there; give the operator and the backend identity shared access; register the project where the backend keeps a registry, before recording it. `--id SLUG` picks an id other than `<name>`; `--use` makes it active on success. With the id alone and no remote, materializes an already registered project in the selected backend's guest, using the remote on record. `--local` makes an empty repository instead, for a project that has no remote at all; `--from-bundle FILE` attaches from a Git bundle on this machine (`git bundle create FILE --all`), carried in over the same one-shot transport `brain import` uses. Neither reaches a network, and neither needs a deploy key. |
| `torio project list` | List the registered projects. Reads config only, runs nothing on the guest, and works with the VM stopped. |
| `torio project show <id>` | Report the shared entry, checkout state, and backend registry state where one is declared. Reports drift without repairing it, and returns no filenames, diffs, or raw Git output. |
| `torio project use <id>` | Make a project active in the backend registry. A backend with no registry refuses the command. |
| `torio project set-remote <id> <remote>` | Replace the remote of a project already on record. The registry is shared, so the correction applies to every backend. The checkout on the selected backend's guest is repointed when its origin still holds the remote being replaced; any other origin is reported and left alone. The id and display name do not change. It is also how a local project gets its first remote: the guest must be able to read it, so this is where a deploy key is provisioned for a private one. A remote cannot be removed — other guests' checkouts still point at it. |
| `torio project sync <id>` | Reconcile a project that has no remote with the bare repository on your host that its boxes meet in, carrying branches and tags both ways as Git bundles over the same one-shot transport `brain import` uses. A ref is written only where what the other side holds is an ancestor of what is arriving; a ref that moved on both sides is named and left as it was. Uncommitted work is never carried. The branch the checkout stands on moves through the worktree, and where Git refuses that because work in the tree would be written over, the branch is held back and named rather than forced. A project that has a remote is refused: its boxes already meet there. |
| `torio project remove <id>` | Archive the backend registry entry where declared, then drop the shared entry. The checkout and deploy key are retained and reported. |
| `torio project enter <id>` | Open an ordinary interactive terminal in the checkout with SSH agent forwarding disabled. A registered project with no checkout on this backend's guest is materialized from the remote on record first. Interactive, so it does not support `--json`. |
| `torio project agent <id>` | Start the configured backend inside the checkout, running as the backend identity rather than as you. A registered project with no checkout on this backend's guest is materialized from the remote on record first. No SSH agent is forwarded and the connection is never multiplexed, so it cannot inherit an operator write connection. The guest's own read route remains available. Interactive; `--json` is a usage error. A backend that declares no interactive session has nothing to open. |
| `torio project shell <id>` | Open an ephemeral operator session in the checkout with your SSH agent forwarded. A registered project with no checkout on this backend's guest is materialized from the remote on record first. Interactive, so it does not support `--json`. |

**The workspace path is not an input.** It is always derived as
`<backend workspace>/<id>` — never taken from you, never stored in config. On
the default backend that is `/home/claude/projects/<id>`. Without `--id`, the id
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

**A project needs no remote.** `--local` records a project with none: it is an
empty repository in the guest that made it, and it is on no forge. `--from-bundle`
records the same kind of project from a repository that already exists on your
machine. Both are listed on every backend, because the registry is shared. Where
the local checkout has no origin, that is agreement; an origin appearing on one
is ordinary drift.

**A local project reaches your other boxes through your host.**
`torio project sync <id>` writes a bare repository at
`${XDG_DATA_HOME:-~/.local/share}/torio/projects/<id>.git` and reconciles this
box's branches and tags with it, both ways. That path is derived from the id on
the machine that needs it and is recorded nowhere, so every registry entry keeps
meaning the same thing on every machine. Once a project has been reconciled
once, opening it on another backend's guest materializes the checkout from
there, the way a project with a remote is materialized from the remote. What
arrives is the branch the host repository points at; the other branches come in
at the first `torio project sync` on that box. Before
that first reconciliation there is nothing to make it from, and opening it says
so rather than guessing. The host repository holds what a sync carried; Torio
does not schedule one and does not call the directory a backup.

**Torio stores no host Git credential.** A remote the guest cannot read without
prompting fails closed. For an SSH remote, `add` generates a deploy key on the
guest and prints the public half — as does `set-remote` when it gives a local
project its first remote, which is the moment a key first has a remote to
authorize against. Add it to that repository with write access off, then run the
command again. Torio cannot verify the forge setting.

`add` resets, cleans, and deletes nothing on the guest, so a rerun after a
failure finishes the work rather than starting over.

### Session write paths {#project-shell}

`enter` is an operator terminal with no forwarded agent. `shell` forwards the
operator's agent. `agent` has no operator write route unless `--push-grant` is
used with a pinned key; then each signature still waits for host approval.

Use `torio project enter <id>` for ordinary editing, checks, and local commits.
The SSH transport disables agent forwarding and connection multiplexing, so it
cannot reuse a push-capable operator connection.

Use `torio project agent <id>` to put the backend to work in the checkout. It
runs as the backend's own guest identity, not as you, on the same transport as
`enter`: no forwarding, no multiplexing. The agent owns the tree and can commit
in it. A correctly authorized deploy key can fetch but not push; no operator
write credential reaches the session.

Inside the box the backend runs without permission prompts, and that is not a
weakening. A prompt is a control inside the agent's own process. The box
replaced it with controls the agent cannot reach: an unprivileged identity with
no `sudo`, a closed group set, no operator write credential, and the edge of the
VM.

`project shell` forwards your SSH agent for exactly as long as the session
lasts, and the capability leaves with you when you exit.

The session is preflighted first — the project registered, the VM
bootstrap-verified, the checkout present with the registered origin and shared
permissions, your local agent holding an identity to forward. **Torio never
test-pushes to prove any of it**, and once you exit it makes no claim about what
you pushed. Check the remote yourself.

The session is not bounded by `--timeout`; you end it.
