# ADR-0029: A local project reaches every box through the host

- Status: Accepted
- Date: 2026-08-17
- Applies to: `internal/projects`, `internal/cli`, `internal/tui`,
  `docs/contracts`
- Supersedes in part: ADR-0027 (its deferral of carrying local projects between
  boxes)

## Decision

**A project with no remote reconciles through a bare repository on the host**,
one per project, at `${XDG_DATA_HOME:-~/.local/share}/torio/projects/<id>.git`.
It is derived from the id the way a workspace path is, and it is recorded
nowhere: the registry keeps meaning the same thing on every machine.

**`torio project sync <id>` carries branches and tags both ways**, each side
writing a Git bundle that the one-shot transport carries, as `brain sync` does.
A project that has a remote is refused: the remote is already where its boxes
meet.

**A ref moves only forward.** A ref is written only where the value the other
side holds is an ancestor of the one arriving. Every other ref is reported by
name and left exactly as it was. Nothing is merged and nothing is committed. The
only working tree write is a fast-forward of the checked-out branch, and whether
that may happen is Git's own answer: `merge --ff-only` writes the tree only
where nothing in it would be written over, and a refusal is the ref being held
back rather than the sync failing.

**Materializing a local project reads the host repository.** It is the third
source ADR-0024 draws from, after the recorded remote and a carried bundle.
Where there is no host repository yet, the refusal stands as it is.

**Nothing syncs on its own.** A rebind carries the Brain (ADR-0026) and does not
carry a project.

### Premises

- P1. A working tree is not a vault: it has branches, uncommitted work and
  conflicts an operator must see. That is why ADR-0027 deferred this transfer,
  and it bounds the design rather than forbidding it.
- P2. A fast-forward is the one ref update that discards no history and needs no
  merge. Refusing every other update leaves a divergence with the person who
  made it.
- P3. A bare repository has no working tree, so the host side can never hold
  uncommitted state and can never be the side that conflicts.
- P4. `limactl copy` carries a file in one shot, a Git bundle is a file, and the
  Brain proved that transport, its ownership trap and its mode trap on a real
  box (ADR-0025).
- P5. A host path in the registry resolves on one machine out of many, which is
  the failure ADR-0023 settled. A path derived from an id on the machine that
  needs it is not in the registry at all.
- P6. `git fetch` follows tags on its own unless told not to, so a fetch that
  only mirrors refs would still write tags nothing decided on. Proven with Git
  2.53.0 on 2026-08-17: a fetch of `+refs/heads/*` into a private namespace also
  created `refs/tags/v1` in the destination.

## Walkthrough

Before: `prezka` is local on the codex box. Every other box lists it and none
can open it, because there is nothing to clone it from. Moving it means running
`git bundle create` inside the guest, copying the file out with `limactl` by
hand, and attaching it as a new project from that bundle, once per move, with
nothing tracking it afterwards.

After: `torio project sync prezka` on the box that holds it, or `y` on the hub's
Projects tab, writes the host repository. On any other box, opening the project
materializes it from there, and `torio project sync prezka` on either box
afterwards carries the work both ways.

## Context

ADR-0027 gave a project the right to have no remote and left it stranded on the
box that made it, with the transfer deferred on the grounds that a project is a
working tree rather than a corpus. The operator feedback that opens this record
is that moving one has become ordinary rather than rare, and that a repository
kept only in Git is versioned enough without a forge behind it. The deferral
named the ADR to write when that happened; this is it.

## Consequences

The host repository is not a backup and is not called one. It holds what a sync
carried, and Torio neither schedules it nor checks what an operator does to that
directory afterwards.

A diverged ref stays diverged. Torio names it and stops, and resolving it means
opening a session on one of the boxes and merging there, which is the only place
the person who caused it can see what they did.

Deleting is not a fast-forward, so a branch deleted in one guest is still on the
host and arrives back at the next sync. Removing a branch everywhere means
removing it on the host as well, with ordinary Git, in a directory whose path
the report prints.

Uncommitted work is never carried. `brain sync` commits what an agent left
because a vault has no one to commit it; a project's tree belongs to whoever is
working in it, and a commit Torio wrote would be a commit nobody chose.

Every local project's whole history now exists on the host as well as in the
guests that hold it. That is the cost of the boxes being able to meet at all.

A materialized checkout arrives on one branch, the one the host repository's HEAD
names, because that is what a clone from a bundle creates. The rest are ordinary
missing refs and arrive at the first sync on that box. Cloning and then writing
every other ref would mean a second way of creating a checkout, for a state one
command already resolves.

## Rejected

**Record the host path as the project's remote.** It would need no new command
and no new transport, and `git ls-remote` would accept it. It puts an address
that resolves on one machine into a registry every machine reads, which
ADR-0023 settled and ADR-0027 P4 restated. The host repository here is derived
where it is needed and written nowhere.

**Mount the host repository into the guest.** A shared directory would remove
the transport entirely. Every guest pins `mounts: []` and bootstrap fails closed
on a host share (ADR-0002), because the VM edge is the trust boundary; a
mount for projects would be that boundary removed for the general case.

**Merge, the way the Brain does.** One vault with one owner can be merged
automatically and its conflicts reported. A project has branches whose
divergence is a decision someone made, and an automatic merge commit would be
Torio writing history into a repository it does not own.

**Commit the guest tree before carrying it, the way the Brain does.** It would
make every sync carry everything. The tree is the operator's working state, and
a commit made to satisfy a transfer is a commit they have to undo before they
can work.

**Sync on rebind, or when a session ends.** The Brain does the first (ADR-0026)
and the machinery would transfer. A rebind is a move between boxes and a session
end is a stopping point, neither of which is a claim that the tree is in a state
its owner wants carried anywhere.

**One host repository holding every project under a namespace.** It would halve
the directories. Forgetting one project would then mean editing refs inside a
repository holding the others, and a repository per id stays something an
operator can read, copy or delete with plain Git.
