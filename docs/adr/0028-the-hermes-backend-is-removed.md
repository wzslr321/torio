# ADR-0028: The Hermes backend is removed, and the contract collapses to one tier

- Status: Accepted
- Date: 2026-08-16
- Supersedes in part:
  [ADR-0009](0009-backend-contract-and-claude-code.md) — "Two tiers, one
  contract", "The contract, and what 'declared' means" (the registry and
  service capabilities), and "Where the Hermes implementation lives".
- Invalidates: ADR-0009 P — that an instance which declares no backend runs
  Hermes and must keep doing so.
- Applies to: `internal/backend`, `internal/lima`, `internal/serve`,
  `internal/projects`, `internal/brain`, `internal/config`, `internal/cli`,
  `internal/tui`, `docs/`, `site/`

## Decision

Torio no longer implements Hermes. `internal/serve` and the `torio serve`
command are gone with it, and so are the two capabilities no remaining backend
declares: `Registry()` and `Service()`. Every backend Torio ships is a process
backend reached by opening a session inside a checkout.

`torio project use` is removed — it selected the active project in a backend's
own registry, and there is no registry left to select in. `backend status` no
longer reports `registry_declared` or `service_declared`; `project show` no
longer carries a `hermes` object or `registry_declared`; `project remove` no
longer claims an archival; `brain status` no longer reports
`project_registered` or `project_conflict`; `vm bootstrap` no longer emits the
`hermes_home` alias for `home`.

The default backend is `claude-code`. No backend derives the bare `torio`
instance any more: every one of them derives `torio-<backend>`, which is the
name the two surviving backends already had.

An instance that still resolves to `hermes` — including every document written
before the config carried a `backend` field, which declares none — is refused
with an error that names the removal and the way forward. It is never silently
re-pointed at a live agent.

### Premises

- P1. No shipped backend declares a project registry or a guest service; both
  capabilities exist in the contract for one implementation that is gone.
- P2. A config document that predates the `backend` field meant Hermes when it
  was written, and its on-disk schema version says so.
- P3. Every instance except Hermes' already derived `torio-<backend>`, so
  leaving the bare name unclaimed strands no box that exists.

## Walkthrough

Before: `torio` reached the Hermes box; `torio serve install` and `torio serve
start` were setup steps four and five; `torio project use <id>` chose the
active project; the hub had a Serve tab and a gateway panel that told the
operator which port to forward for Hermes Desktop.

After: `torio` reaches the `claude-code` box. Setup is create, start,
bootstrap, log in, brain, project — the two serve steps are gone from the
wizard because no backend has them. A project is opened with `torio project
enter <id>` or from the hub, which now has five tabs.

An operator whose box ran Hermes gets, on any command:

```
backend "hermes" was removed; this box cannot be reached as it is. Create a box
on a supported backend with `torio vm init --backend claude-code`, or rebind
this instance from the hub.
```

## Context

Hermes was the backend Torio was built around, and the contract in ADR-0009 was
written by generalizing away from it: the service tier, the project registry
and the readiness probe are all descriptions of one implementation. Two
backends have shipped since, and neither uses any of it.

Carrying the tier had a running cost beyond the dead code. Every command that
touched a project branched on whether the backend declared a registry, and the
branch that reported "there is nowhere to register" existed only so the other
branch could report a registration. `torio serve` was a whole command group
that failed closed on both live backends.

## Consequences

- This is a breaking change for boxes running Hermes. They are not migrated;
  they are told what happened. Their checkouts and vault stay on disk, and the
  guest is still reachable with `limactl` for anyone who wants to copy
  something out.
- The bare `torio` instance and the root `config.json` become unclaimed. An
  operator whose settings lived there moves them to
  `instances/torio-claude-code/config.json`, which is where the default
  backend's document already resolves.
- `internal/lima` no longer holds a backend, so it no longer needs to. The
  guest layout constants it exported for one implementation are gone, and the
  Second Brain derives every path from the backend identity it was already
  given.
- The JSON envelope loses fields. Readers of `registry_declared`, `hermes`,
  `hermes_archived`, `hermes_home`, `project_registered` and `activated` break
  rather than reading `false` forever, which is the honest failure.
- A future service backend would have to reintroduce the tier. That is the
  right trade: the contract should describe what Torio runs, and a shape kept
  for a hypothetical implementation is a shape nothing verifies.

## Rejected

**Keep the backend, stop shipping it.** A registered backend nobody runs still
has to compile, still has to be verified by the e2e matrix, and still shapes
every branch in the project and MCP code. The cost is the tier, not the file.

**Migrate Hermes boxes to `claude-code` automatically.** The declaration exists
precisely so a guest built for one identity is never driven as another: the
Hermes box has a `hermes` user, a `/home/hermes` layout and a systemd unit that
`claude-code` knows nothing about. Rewriting the field would produce a box that
fails every check while claiming to be something it is not.

**Let `claude-code` take over the bare `torio` instance.** It reads well for a
fresh install and breaks two existing populations: every `claude-code` box is
named `torio-claude-code` and would go unreachable, and every Hermes box is
named `torio` and would be re-pointed at a different agent — the exact failure
the declaration prevents.

<!-- The whole record fits in 120 lines. Cut, do not append. ADR-0020. -->
