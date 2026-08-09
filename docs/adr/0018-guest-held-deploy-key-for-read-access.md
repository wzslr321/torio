# ADR-0018: A guest-held deploy key provisions read access

- Status: Accepted
- Date: 2026-08-09
- Applies to: `internal/projects`, `internal/cli`

## Context

Attaching a private repository was the one ordinary task Torio refused to help
with. `project add` proved the guest could read the remote, and an unreadable
one ended the command with a single line: provision access for the hermes user
out of band. Nothing said what that meant.

What it meant, in practice, was six things an operator had to know and none of
which any Torio surface stated:

1. the key belongs in the guest, generated as the backend identity, because that
   is the identity that runs the clone;
2. a forge deploy key is scoped to one repository, so several attached private
   repositories need several keys;
3. the ssh configuration selecting between them needs `IdentitiesOnly`, or ssh
   offers every key it holds and the forge authenticates the first one valid for
   the account rather than for this repository, which surfaces as
   `Repository not found` on a repository that plainly exists;
4. any per-repository alias has to be ordered ahead of the plain `github.com`
   entry that carries no `IdentityFile`, because that entry is the push path
   through the operator's forwarded agent (ADR-0003);
5. the key must be read-only, or the guest silently gains write capability that
   ADR-0003 places in an operator session and nowhere else;
6. all of it lives in a guest file the operator reaches by leaving Torio.

An operator who has done it once has the knowledge written down in a comment
inside that guest file, which is a document only a person who already knew the
procedure can have created. The prerequisite was real, but the cost of meeting
it was accidental, and it fell entirely on the operator every time.

The tempting fix is to forward the operator's SSH agent into the clone, the way
`project shell` forwards it into a session. It is rejected below: `add` is a
control-plane command that also runs noninteractively, and an agent forwarded
outside a session the operator opened is exactly the capability ADR-0003 keeps
scoped.

## Decision

`project add` provisions the guest's own read access when it cannot read an SSH
remote, and reports what a human has to do with it.

- The guest generates an ed25519 keypair as the backend identity, at
  `<identity home>/.ssh/torio/<project id>`, derived from the identity and the
  validated project id the same way the workspace path is.
- The private half stays in that file. Torio does not read it, copy it, transport
  it, or store it on the host. The only file read back is the `.pub`, and it is
  refused unless it parses as one public key line.
- The key is offered to that one remote through `GIT_SSH_COMMAND` with
  `IdentitiesOnly=yes`, on the preflight and on the clone, and on the run that
  generated it. A key authorized ahead of time therefore attaches in one command.
- When the remote is still unreadable, the command fails closed as before, at
  exit `7`, carrying the public half, the host to authorize it on, and the path
  of the private half. The rerun is the same command.
- A rerun before authorization reports the existing key rather than generating a
  second one. The missing act is on the forge and another key does not supply it.
- After a successful attach the identity gets a `gitdir`-scoped
  `includeIf` pointing at a config file that sets `core.sshCommand` for that one
  checkout, so a fetch the identity runs on its own reaches the remote the way
  Torio's run did.
- The recorded remote stays the canonical one. No host alias and no guest alias
  enters the record, so the entry keeps meaning the same repository everywhere.
- `project remove` reports a retained key and touches nothing. Removal forgets a
  project; withdrawing an authorization is a decision on the forge.

Authorizing the key stays a human act, with a human's account behind it. Torio
narrows the operator's work from six undocumented steps to one, and does not
take the step that requires an identity it does not have.

## Consequences

Attaching a private SSH repository is the README flow, twice: run it, authorize
the printed key, run it again. Public and private repositories differ by one
authorization rather than by a procedure.

The guest holds key material Torio created. That is new, and it is the cost of
this decision. Three properties bound it: the key is read-only, so it cannot
carry a write ADR-0003 reserves for an operator session; it is scoped to one
repository, so its blast radius is the repository already attached; and the host
holds no copy, so compromising the control plane yields nothing.

An operator who wants a different provisioning story keeps it. A key already at
the derived path is used as it is, and a guest configured by hand keeps working,
because the preflight asks whether the remote reads, not how.

A private HTTPS remote is unchanged and still fails closed. Reading one takes a
stored credential, and storing one remains outside what Torio does.

`project enter`, which forwards no agent, still cannot fetch a private remote as
the operator identity: the key belongs to the backend identity and stays
readable only by it. `project shell` covers the operator through the forwarded
agent, which is where operator capability is supposed to live.

## Rejected alternatives

**Forward the operator's SSH agent into `add`.** It would need no key at all.
It is rejected because `add` is a control-plane command that also runs from
scripts and against a second backend, and ADR-0003 puts operator write
capability inside a session the operator opened and nowhere else. An agent
forwarded into a noninteractive command is that capability leaving its scope,
for a read.

**Copy a key from the host.** The host key is the operator's identity, usually
with write capability, and moving it into the guest crosses the boundary
ADR-0002 draws. A key the guest generates is weaker than one the operator holds,
which is the property wanted.

**Store an HTTPS token.** It would work for both transports and it is exactly
the custody ADR-0004 refuses. Torio holding a token is the thing the product
exists not to do.

**Write per-repository `Host` aliases into the guest ssh configuration.** This
is the manual procedure, automated. It was rejected because the alias then has
to appear in the recorded remote for the record to be usable, which makes the
record depend on one guest's local configuration, and a second backend cannot
use the same entry. Recording the canonical remote and selecting the key through
`GIT_SSH_COMMAND` keeps the record portable.

**Generate one key for every repository.** Fewer keys and less machinery, but a
forge deploy key is scoped to one repository, so one key cannot serve two, and an
account-wide key would be a broader credential than the task needs.
