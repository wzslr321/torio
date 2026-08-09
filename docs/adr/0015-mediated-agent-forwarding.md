# ADR-0015: The agent an operator session forwards is Torio's, not the operator's

- Status: Accepted
- Date: 2026-08-09
- Superseded in part by:
  [ADR-0016](0016-session-scoped-push-grant.md), for the deferred session-scoped
  grant: it is no longer deferred, and this record is what made it answerable.
- Supersedes in part: the "Write capability" section of
  [ADR-0003](0003-ownership-split-and-operator-carried-write.md), for what
  `ssh -A` puts inside the guest.
- Applies to: `internal/sshagent`, `internal/lima`, `internal/cli`,
  `internal/config`, `internal/projects`

## Context

ADR-0003 made write capability ephemeral: it enters the guest with
`torio project shell` and leaves when the session exits. That is still true, and
it is not what this record is about.

What `ssh -A` forwards is the whole agent. Every identity the operator has loaded
becomes reachable from inside the VM, for any number of signatures, for the
length of the session — a work key, a personal key, a key for a machine that has
nothing to do with the project. The session boundary bounds *how long*; nothing
bounds *what* or *how often*. The known gap was already written down before this
record: the forwarded thing is the operator's keyring, not a repository-scoped
capability.

Two things follow from that. It is more capability than a push needs, and it is
silent: the operator learns a session used their key when they check the remote,
which `reportShellEnd` correctly tells them Torio cannot do for them.

The threat model is unchanged (`SECURITY.md`): a confused or injected agent, and
a guest that could be compromised. Against that, "the operator's whole keyring,
unattended, for as long as the shell is open" is the largest thing Torio
knowingly hands across the boundary.

## Decision

**`torio project shell` forwards an agent Torio serves, holding one key, that
asks before every signature.**

The proxy runs on the host, in the `torio` process, for the life of the session.
`-A` forwards whatever `SSH_AUTH_SOCK` names, so pointing that one variable at
the proxy socket changes *what* crosses the boundary without changing *how*: same
flags in the same order, same auth-agent channel, same root-owned guest helper,
same guest-side `SSH_AUTH_SOCK` written by sshd. Nothing on the guest side had to
be trusted, or even changed, to make this narrower.

It answers the four requests it has reasons for and refuses the rest:

- `REQUEST_IDENTITIES` is fetched from the operator's real agent and filtered to
  the pinned key. Fetched, not answered from memory, so a key unloaded mid-session
  stops being offered rather than being offered and then refusing to sign.
- `SIGN_REQUEST` for the pinned key waits for an explicit confirmation on the
  host. For any other key it is refused without asking, so a dialog never appears
  for a request that was never going to be allowed — the fastest way to train
  someone to click through the ones that matter.
- Everything else — adding a key, removing one, locking the agent, any extension
  — fails without being written to the operator's agent at all. Forwarding an
  unknown request to see what happens would be handling it.
- Every decision is recorded before it takes effect, and a decision that cannot
  be recorded is a denial. That is the rule
  [ADR-0012](0012-mcp-broker-transport-and-oauth.md) set for the MCP broker, and
  it applies here for the same reason.

The pin lives in the config document as `operator_key`, a fingerprint or a key
comment. **Absent, nothing changes**: the session forwards the operator's agent
whole, exactly as before. That is not an accidental fallback to the weaker mode.
A document with no pin was written by an operator who has not chosen a key, and
choosing one for them is choosing which key a guest may use.

The confirmation is a native dialog. On Darwin it is `osascript`, and the message
travels in an environment variable read by `system attribute` rather than being
concatenated into the AppleScript: a project name, branch or remote interpolated
into a program would be a program the guest helped write. "Deny" is both the
default and the cancel button, so Return, Escape and the close box all refuse;
approval takes an aimed click. Every host that is not Darwin denies, because a
proxy that signed without asking — on the grounds that there was nothing to ask
with — would be weaker than the `ssh -A` it replaces while claiming to be
stronger.

**What the dialog says, and does not.** A sign request is a key and a blob. It
carries nothing about Git. So the dialog names the project, remote, branch and
commits-ahead **as the preflight measured them when the session opened**, and then
says plainly that Torio cannot see what the key will be used for. Torio's existing
refusal to claim what a session pushed is unchanged; this record must not become
the place it quietly started claiming it.

The same snapshot is printed when the session opens, which is the smaller half of
this and the one an operator notices first: `git status` and `git diff` no longer
have to be the first two commands of every session.

## What this does not change

Invariants 8 and 11 stand. Write capability still comes only from a session the
operator opened and still ends with it; push, merge and release are still
human-only operations outside the CLI. The agent identity still has no route to a
remote, and `ssh.forwardAgent` is still false globally. This record narrows what
a session carries; it does not move where sessions come from.

The deferred design in [ADR-0009](0009-backend-contract-and-claude-code.md) — a
session-scoped grant letting the agent identity reach a forwarded socket — is
still deferred, and this makes it a smaller question than it was. What ADR-0009
declined was handing a socket to the agent under a flag. A grant on top of this
proxy would hand over the ability to *ask*, with every signature still stopping at
a dialog on the operator's Mac and landing in the decision log. That is a
different proposal and belongs to its own record; it is named here only so the
next one does not have to re-derive why it became answerable.

## Consequences

- The config schema goes to `4`. A binary that predates it refuses the document,
  by its version gate and by `DisallowUnknownFields` on `operator_key`. That is
  the desired failure: an older binary cannot know a session was configured to
  forward one mediated key, so it must stop rather than guess and hand over the
  keyring. A `3` document reads and normalizes as before; a `3` document
  *carrying* `operator_key` is rejected, because a document means what its
  declared version says it means.
- `ShellPreflight` gains a description of the checkout. It is deliberately not a
  precondition: it is absent from `shellPreconditions`, it appends nothing to
  `Verified`, and nothing refuses a session because it could not be assembled.
  A detached HEAD and a branch with no upstream are left unsaid rather than
  reported as zero.
- The preflight still never pushes. Both new commands are read-only Git that
  contacts no remote, and `TestShellPreflightNeverTestsThePush` holds over the
  whole preflight argv unchanged.
- `MediatedShellSpec` is a second constructor rather than a parameter on
  `OperatorShellSpec`, so the unmediated argv keeps its own pinned test. The two
  shapes differ in one environment variable, which is exactly the kind of
  difference a shared constructor loses. It is also the only session spec with a
  non-nil `Env`: one variable is replaced and the rest pass through, because a
  session that composed a fresh environment would lose the operator's terminal
  along with their keyring.
- The decision log sits beside the config document, mode-private, opened
  `O_NOFOLLOW`. It records time, request kind, fingerprint and outcome. It has no
  field that could hold a signature, the data under one, or anything else a guest
  supplied.
- One dialog appears per signature, which is roughly one per remote operation.
  There is no "allow for the next five minutes": a grace window is a durable
  capability with a timer, which is the shape ADR-0003 exists to prevent.
- The proxy runs as the operator, on the operator's host, with their real agent
  one dial away. It is a control against a compromised **guest**, not a
  compromised host, and it is called that everywhere it appears.
- **An SSH agent is used by the SSH transport and by nothing else.** A checkout
  whose origin pushes over HTTPS gets no pinned key, no dialog and no decision
  log, and none of this record applies to it. That is not a gap to be closed —
  there is nothing for an agent to sign — so a session says it plainly at the
  moment it opens rather than leaving an operator waiting for a prompt that is
  never coming. A granted session refuses outright: it exists to push, and a key
  that transport never consults is not a capability.
- **A host key must already be trusted by the identity that will use it.** Torio
  provisions no `~/.ssh` for anyone, and the operator and the backend identity
  have different home directories, so a key one of them trusts says nothing
  about the other. Without this the failure arrives mid-session, as
  `Host key verification failed`, which reads like a problem with the key the
  operator has just pinned and is not one. A granted session refuses when the
  key is absent; an operator shell says so and opens anyway, because a shell is
  opened to read and commit as often as to push.

## Rejected alternatives

- **`ssh-add -c`, or a hardware key with `verify-required`.** Both give a
  confirmation per use with no Torio code at all, and both are worth having —
  they add unforgeable human presence, which a host-side proxy cannot. Neither
  was enough on its own: the confirmation shows only the key comment, so it
  cannot say which project is asking; it does not narrow the keyring; it leaves
  no record; and Torio cannot verify that a key was loaded that way, so it could
  not honestly claim any of it. They compose with this rather than replace it.
- **A second `ssh-agent` holding a copy of the key.** A durable credential in a
  second place, which is the thing being prevented.
- **Reading the signed blob to describe the operation.** It would put a Git
  protocol exchange, and whatever a caller had placed in it, inside a host
  process with no reason to hold one — to produce a description that is still a
  guess. The proxy parses past the blob and never retains it.
- **Making mediation the default with no pin, by choosing the sole loaded key.**
  Tempting, and it is what happens when a pin is set and the agent holds one key.
  As a default it silently changes what an existing session forwards on upgrade,
  which is a capability change nobody asked for, in the direction that is harder
  to notice.
