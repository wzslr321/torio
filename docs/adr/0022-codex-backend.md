# ADR-0022: Codex CLI is the third backend, and every control it obeys lives in a root-owned file

- Status: Accepted
- Date: 2026-08-14
- Applies to: `internal/backend/codex`, `internal/lima`, `internal/cli`,
  `docs/contracts`

## Decision

**Torio ships a third backend, `codex`, on the contract ADR-0009 already
defines.** It declares no project registry and no guest service, and it
declares an interactive session. Its binary is a pinned release archive proven
against a digest committed in this repository, never against a checksum fetched
from the host that serves the binary. Its controls are
`/etc/codex/managed_config.toml` and `/etc/codex/requirements.toml`, both
root-owned: the first shapes the agent, the second is the allowlist an MCP
grant is written into. The agent's own `~/.codex/config.toml` is read as a
drift detector and is never described as a boundary. The waiting marker is
written by managed hooks, so `torio status` can say the agent is waiting.

### Premises

- P1. The release publishes checksums for its `codex-package-*` archives only,
  so the archive Torio installs has no vendor checksum to be verified against.
- P2. On Linux, `/etc/codex/managed_config.toml` and
  `/etc/codex/requirements.toml` are read at the pinned version with no cloud
  enrolment, and outrank the agent's own configuration.
- P3. `requirements.toml` matches a server by executable and by each argument,
  so a grant can name both the relay and the one service it may reach.
- P4. Managed hooks fire on `Stop`, `PermissionRequest`, `UserPromptSubmit` and
  `SessionEnd`, and receive the session id on standard input.
- P5. Codex discovers skills under `$CODEX_HOME/skills`, so the vault's
  retrieval skill has a directory to be installed into.
- P6. A non-empty `auth.json` in the profile means a credential is held, and
  its absence means none is.

## Walkthrough

Before: an operator who works in Codex has no box. They run it on the host
against a checkout the host owns, which is the arrangement Torio exists to
replace, or they run it inside a `claude-code` box as a user who was never
provisioned for it.

After: `torio vm init --backend codex`, then create, start and bootstrap. The
report names the guest identity, the pinned version and the absent credential.
`torio backend login` opens a device-code prompt, so the credential arrives
without forwarding a port into the guest. `torio project add` attaches the
checkout, Enter on it in the hub opens a Codex session as the `codex` identity,
and `torio status` shows the box working or waiting.

## Context

The freeze on #43 was lifted for this. ADR-0009 claimed the contract was
narrow enough that a further backend costs an implementation package and one
registration, and nothing had tested that claim: Claude Code was designed
alongside the contract, so it could not falsify it. Codex was chosen because it
is the agent operators ask for next and because it is unlike Claude Code in the
places that matter here. It keeps configuration in TOML rather than JSON, it
has a first-class system layer rather than one managed file, and it publishes
release archives without checksums for them.

## Consequences

Every pull request now pays a third guest in the platform matrix, and the wall
clock of that job is set by the slowest leg. The digests are maintained by
hand: a version bump that changes the pin without changing them fails closed at
install, which is the intended failure and is still a step somebody has to
remember. Torio gains a TOML rendering surface it must keep byte-exact, because
the allowlist is verified by comparing bytes rather than by parsing them, and
Go's standard library will not write TOML. The guardrail and boundary
distinction gets harder to state on this backend than on the last one: two
root-owned files with different jobs sit behind one word in the documentation,
and the reference has to separate them. Brain Kit ships without a Codex eval
runner, so the vault's behavioural evidence stays Claude Code's alone.

## Rejected

**Installing from npm.** It pulls a Node runtime into a guest that needs one
for nothing else, and it moves the integrity question to a package manager
whose verification Torio does not perform and cannot record as a check.

**Verifying against the published `codex-package_SHA256SUMS`.** The checksummed
archives are a different set from the one carrying the agent binary, so this
would mean installing a different artifact for the sake of the file. Even then
the file is served by the host that serves the archive, so it proves the
transfer and not the contents. A digest committed here is read once in review
and cannot be rewritten later by the origin.

**The `notify` program instead of hooks.** It carries one event, delivered as
an argument rather than on standard input. It can say a turn finished, which is
half of a waiting marker, and it has nothing to say when the operator answers,
so the marker would set and never clear.

**Scrubbing the agent's own `config.toml`.** Torio would be editing a file the
agent owns and rewrites, on a schedule neither controls, to enforce something
the allowlist already enforces. It would also read as a boundary in the report,
which it is not: the file is evidence of what the agent declared, and the
control is the root-owned allowlist that decides which declaration runs.
