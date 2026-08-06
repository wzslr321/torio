# ADR-0008: The dormant MCP broker daemon is deleted

- Status: Accepted
- Date: 2026-08-06
- Supersedes: the sentence "The unfinished daemon code stays in the repository
  and stays tested. It is not a delivered product surface." under "Not
  delivered — the broker daemon" in
  [ADR-0004](0004-mcp-credential-custody-and-egress.md). Nothing else in
  ADR-0004 changes: the custody boundary, `torio mcp install`/`status`, the
  uid-keyed half of egress control, and the remaining "not delivered" items —
  the write window and inference-credential custody — stand exactly as
  recorded.
- Applies to: `internal/lima`, `internal/mcpbroker`, `cmd/torio-mcp-broker`,
  `cmd/torio-mcp-connect`

## Context

Pre-publication cleanup audited the tree for code with no consumer.
`internal/mcpbroker` (the JSON-RPC engine, socket listener, peer-credential
checks, audit logger and policy parser), `cmd/torio-mcp-broker` (the daemon
binary) and `cmd/torio-mcp-connect` (the relay binary) — roughly 5,650 lines
including their own tests — are fully dormant. ADR-0004 already recorded this
precisely: "The broker and relay binaries are not release payloads. The public
install command installs no unit and activates no daemon until a separate
accepted ADR defines upstream transport and OAuth lifecycle end to end." Having
recorded that, ADR-0004 chose to keep the code anyway: "The unfinished daemon
code stays in the repository and stays tested."

That was a reasonable call for a private, growing tree — tested-but-unshipped
code costs little to carry and preserves optionality for whoever eventually
settles the upstream transport and OAuth lifecycle the daemon needs.
Publication changes that arithmetic. A public repository presents everything
in it as something the project stands behind. Five thousand lines of a
daemon that has never been connected to a real upstream, complete with its
own test suite, reads as a shipped feature to a reader who has not also read
ADR-0004's delivery-status section, and even to one who has, it is code that
cannot run against anything because the design decision it depends on —
upstream transport and OAuth lifecycle — was never made. Carrying it forward
is optionality nobody has drawn on since the daemon's own commits landed;
deleting it is a maintenance-cost cut, not a redesign.

The complication is that the daemon package is not purely unused.
`internal/lima/mcppolicy.go` backs `torio mcp install` and `torio mcp
status` — the part of ADR-0004 that *is* delivered — and its fail-closed
verification of the guest's root-owned policy directory (ADR-0004 §6) calls
`internal/mcpbroker.ParseDocuments` to validate those documents against the
same strict schema a broker would enforce. `ParseDocuments`, and the `Set`,
`Grant` and `Digest` types it produces, are shared logic, not daemon-only
logic. Deleting `internal/mcpbroker` outright breaks a command this project
ships.

## Decision

**The dormant broker and relay code is deleted. The policy-document parser it
contained moves to `internal/lima`, which is now its sole owner.**

1. `internal/mcpbroker/`, `cmd/torio-mcp-broker/` and `cmd/torio-mcp-connect/`
   are removed from the repository in full: the JSON-RPC engine, the socket
   listener, the peer-credential checks, the audit logger, the upstream
   relay, and their tests.

2. The parsing logic `internal/lima/mcppolicy.go` actually depends on —
   `ParseDocuments`, `Set`, `Grant`, `Digest`, `ServiceGrant`, `ToolGrant`,
   `ValidateServiceName`, and the schema bounds and validation rules they
   close over — moves unchanged into a new file,
   `internal/lima/mcppolicydoc.go`. `internal/lima` is now the sole owner of
   the policy-document schema. A future broker, if one is ever built, is
   expected to import it from there rather than reintroduce a second copy.

3. What is verified does not change. `torio mcp install` and `torio mcp
   status` parse and enforce the exact same schema, with the exact same
   bounds, against the exact same on-guest files; the call that used to
   resolve to an imported package now resolves to a package-local function.

### What this does not do

- **It does not resolve any item ADR-0004 lists as not delivered** — upstream
  transport and OAuth lifecycle, the write window, inference-credential
  custody, or egress control. Those remain exactly as undelivered as before;
  nothing here builds toward them or narrows them.
- **It does not touch the custody boundary** — identity, socket ownership,
  policy legibility, fail-closed verification — which is the part of
  ADR-0004 that is delivered and unaffected. The guest-side naming that
  boundary anticipates (`torio-mcp-broker.service`,
  `/usr/local/bin/torio-mcp-broker`, `/usr/local/bin/torio-mcp-connect` in
  `internal/lima/mcpunit.go`, `mcpconfig.go` and `profile.go`) describes what
  `torio mcp status` would look for on a guest running a broker; it names the
  guest, not a Go package in this repository, and nothing here changes it.
- **It is not a claim that the daemon's design was wrong.** It is only a
  claim that carrying five thousand lines of code with no consumer, in a
  repository about to be published, costs more than it returns.

## Consequences

- `internal/mcpbroker`, `cmd/torio-mcp-broker` and `cmd/torio-mcp-connect` no
  longer exist. `go build ./...` never built them into a release artifact —
  `make package-release` only ever builds `./cmd/torio` — so this removes no
  release payload.
- `internal/lima` gains the policy-document parser and its test suite.
  `internal/lima/mcpbroker_test.go` is unaffected by this change: it tests
  the broker-*identity* verification in `internal/lima/mcpbroker.go` (uid,
  group membership, home directory, unit file — the kept custody boundary),
  which is a different file from the deleted `internal/mcpbroker` package
  and was never part of it. Its name does not need to change.
- A later ADR that defines upstream transport and OAuth lifecycle and
  decides to ship a broker starts from `internal/lima`'s policy parser
  instead of reintroducing the deleted package.
- ADR-0004 gains a `Superseded in part by:` header pointer to this record,
  alongside its existing pointer to ADR-0006.

## Rejected

- **Delete `internal/mcpbroker` and reimplement the parser from scratch in
  `internal/lima`.** Rejected: it is the same ~200 lines of validation logic
  — schema bounds, strict decoding, upstream-endpoint rules — that already
  has a passing test suite. Rewriting it by hand to avoid a straight move
  would risk exactly the drift the original code's own comments warn
  against: a second implementation free to disagree with the first.
- **Leave `internal/mcpbroker` in place as a library-only package with the
  daemon binaries removed.** Rejected: the package's name and doc comments
  describe a broker. A caller reading `mcpbroker.ParseDocuments` would
  reasonably infer a broker exists to call it, and leaving a headless
  library under that name invites exactly the misreading this ADR exists to
  prevent.
- **Keep the daemon and relay as ADR-0004 originally decided, and wait for
  the upstream-transport ADR before touching them.** Rejected on the
  publication timeline: publication proceeds now, and the transport decision
  has no date attached to it. Waiting means publishing five thousand lines of
  dead code with no stated plan for when that stops being true.
- **Relocate `internal/mcpbroker` to an experimental or build-tag-gated
  path instead of deleting it.** Rejected: this project has no
  experimental-code convention — a spike lives in `spikes/` and never
  graduates into `internal/`, and `spikes/` itself was cut from the tree
  ahead of publication for the same reason. Adding a second such convention
  for this one case relocates the maintenance and misreading cost without
  removing it.
