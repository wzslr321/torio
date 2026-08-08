# Changelog

## Unreleased

### Added

- **The vault has a written standard, and it ships as a kit you can install
  without the VM.** `brainkit/STANDARD.md` describes a Torio vault normatively —
  eight note types and their frontmatter, naming, which links each type owes,
  and what an agent may do to it — as a profile of the Open Knowledge Format.
  `brainkit/` is a Claude Code plugin, published from a marketplace at the
  repository root, carrying six skills (search, capture, triage, daily, meeting,
  people), five commands and a librarian subagent. Two clauses make it adoptable
  against notes that already exist: a note without frontmatter stays valid to
  read, and wikilinks are refused in favour of relative Markdown links a checker
  can verify. Nothing under `internal/`, `cmd/` or `e2e/` changed — the kit is
  content, Torio is mechanics
  ([ADR-0010](docs/adr/0010-okf-vault-standard-and-brain-kit.md)).
- **A backend contract, and a second backend behind it.** Torio ran one agent
  and the name was hardwired into every layer. `internal/backend` now states
  what Torio requires — an identity and its paths, an install and pin, a version
  probe, credential presence — and three capabilities a backend *declares*:
  a project registry, a guest service, an interactive session. Nil is a
  first-class answer, and the rule that follows is the point: whatever a backend
  declares, `vm bootstrap` and `serve status` must prove; whatever it declares
  it has not got, they must not pretend to check
  ([ADR-0009](docs/adr/0009-backend-contract-and-claude-code.md)).
- **Claude Code as the second backend.** A process backend, not a service one:
  no daemon, no readiness endpoint, no project registry. It runs as a dedicated
  guest identity with no sudo and a closed group set, both proven rather than
  assumed, from a version-pinned binary verified against the vendor's published
  checksum and installed root-owned — which closes, for this backend, the
  agent-writable shim that `SECURITY.md` records as a known path to root.
- `torio project agent <id>` starts the backend inside a checkout as the backend
  identity, on a transport that forwards no SSH agent and reuses no connection.
  The triad is now `enter` (you, no push), `shell` (you, push), `agent` (the
  agent, never push).
- `torio backend status` and `torio backend login`: what this instance runs and
  what it declares, and the terminal where an operator grants the box a
  credential of its own. Torio stores, forwards and reads none of it.
- **`--backend NAME`, a global flag: name the agent, and Torio finds its box.**
  One instance still runs one agent identity — that is what makes every custody
  statement provable — but the operator no longer carries the bookkeeping. The
  instance is *derived* from the backend (`torio` for the default one,
  `torio-<backend>` for the rest), so there is no table of names to maintain and
  no second place that can disagree about which box runs which agent.
  `TORIO_INSTANCE` still names a box directly and wins over the flag; given
  both, a disagreement between the flag and what the instance declares is a
  usage error rather than a guest built for one identity being driven as
  another. `torio vm init --backend NAME` is how a second backend gets its box.
- **One project registry, shared by every instance.** It moved out of the
  instance document into `projects.json` in the config root. A project is
  something the operator attached, not something an instance owns, so switching
  which box a command talks to no longer switches which projects exist —
  `project list` says the same thing whichever backend is selected, and the
  workspace path it reports moves with the backend that owns the checkout.
  Migration is a read, not a command: the legacy `projects` array is used until
  `projects.json` exists, and is **left in place** when it does, so reversing
  this is removing one file.
- `torio project add <id>` with no remote materializes an already registered
  project in the selected backend's guest, from the remote on record. Checkouts
  cannot be shared — each is owned by one backend's guest identity — so this is
  the explicit step that gives a second backend its own working tree. It stays a
  separate command rather than something `project agent` does on demand, because
  cloning reaches a Git remote.

### Changed

- `project show` and `project remove` finish what the declared-absent registry
  started. `show` printed `hermes: absent` and an object of all-falses on a
  Claude Code box — naming a backend that is not running there and reporting its
  registration as gone; `remove` claimed it had archived a Hermes project when
  there was none to archive. Both now carry `registry_declared`, as `serve
  status` carries `service_declared`, and say nothing about a registration when
  it is false. A Hermes instance's envelope is unchanged.
- Config schema `"3"` carries the backend. `"2"` is still read: it predates the
  field, names no backend, and an instance that names none is running Hermes —
  which is what every existing box already is. An older binary refuses a `"3"`
  document, which is the intended failure rather than a gap: it cannot know its
  Hermes-shaped commands are aimed at a box running a different agent.
- `vm bootstrap` and `serve status` carry `backend`, and `serve status` carries
  `service_declared` first — on a backend with no service the remaining fields
  are absent state, not a service that is down. Every existing Hermes-named key
  is still emitted, unchanged, on a Hermes instance.
- The Second Brain's vault, staging and lock follow the backend identity that
  owns them, instead of being fixed at one backend's home.
- The Brain's retrieval skill is now the backend's own, declared alongside the
  root it is discovered in. Claude Code gets a skill written for Claude Code —
  `Grep`, `Glob` and `Read` over `/home/claude/brain`, installed at
  `~/.claude/skills/torio-brain/` and uncategorized, because it routes by
  reading descriptions rather than by position in a static index. The Hermes
  skill and its category grouping are unchanged.
- `brain status` and `brain init` name the skill path from the report instead of
  a constant. The constant was the Hermes profile path, which every Claude Code
  box would have been told to look at.

### Fixed

- `backend status` reported a Claude Code box's credential as **not-applicable**
  — the answer that means Torio has no way to ask — on a box whose auth probe
  had run and found one. The renderer built its lookup key by appending `_auth`
  to the name the backend is registered under, which happened to be what the
  first backend called its check and matched nothing on the second, so the
  version and the MCP server list went missing the same way and `vm bootstrap`
  never told an unauthenticated operator to log in. A backend now *declares*
  the checks the report is read by. The credential answer gained a fourth
  state, **unknown**, for a check that was declared and produced no result:
  having no way to ask and getting no answer are different facts, and both are
  different from being logged out.
- `backend login` opened Claude Code in the operator's home directory. `sudo -H`
  sets `HOME` and leaves the working directory inherited from the ssh session,
  and the agent identity is deliberately unable to traverse that directory —
  so the first thing an operator saw was the agent reporting two unreadable
  settings files and offering to repair them, on a box where nothing was wrong.
  The session now starts in the agent's own home, chosen after `sudo` so it does
  not depend on the operator reaching that directory either.
- `vm bootstrap` could never succeed on a Claude Code instance. Its no-sudo
  proof required `sudo -n -l -U claude` to exit 1, and sudo 1.9.15 exits 0 for
  that query whether the identity may run everything or nothing — the answer is
  in the output. The check now matches sudo's two sentences in the C locale and
  fails closed on anything else, including silence. Found by running the backend
  on a real guest; every unit and transcript test passed throughout.
- Every `torio project` command ran as the wrong agent on a non-Hermes
  instance. The project manager derives the guest identity, workspace, registry
  and interactive session from the backend in its options and falls back to the
  first backend Torio shipped when handed none — and the CLI handed it none.
  `project add` on a Claude Code box therefore demanded a `hermes` user the
  guest does not have, and `project agent` would have asked the fallback backend
  for a session it does not declare.
- `project show` reported `hermes_project_absent` on every project on a backend
  that keeps no registry — an issue naming a registration that could never have
  existed, on a healthy checkout. `project use` there failed with a registration
  error telling the operator to re-run `project add`, which cannot create what is
  missing; it now fails with `no_registry` naming the backend, where serve's
  `no_service` already pointed.
- `brain init` could never reach `initialized` on a backend with no project
  registry. An unregistered vault counted as drift regardless of whether there
  was anywhere to register it, so `init` reported drift it could not repair and
  then failed its own postcondition — on a vault that was healthy on disk.
  Registration is now a condition only where a registry is declared.
- `brain status` told an operator on a backend that declares no retrieval skill
  to run `torio brain init` to install one, while the JSON envelope beside it
  correctly reported `not_applicable`. It now says the same thing in both.
- Installing a retrieval skill for a backend with no skill category probed
  `/SKILL.md` and `test -f ""`. No shipped backend reached that path before; the
  Claude Code skill does.
- `brain import` staged and swapped through the first backend's home on every
  backend. The vault, the staging directory and the lock had already moved to
  the identity that owns them; the import's own four paths had not. On a Claude
  Code box the import therefore had root fabricate `/home/hermes`, staged
  private vault bytes there, and parked the previous Brain beside them —
  outside the boundary the owning identity keeps. All four are now derived.
- `backend status` showed no MCP servers on a Hermes box that had them. The
  check was declared and did run, but only inside `torio mcp status`, whose
  report the status renderer never sees — so "none configured" and "three, one
  of which bypasses the broker" were the same silence. Bootstrap now records it,
  and records what it found without failing: the command that treats a bypass as
  a failure is the one that verifies the boundary.
- `backend status` said it read the guest and changed nothing while running the
  same reconciling walk as `vm bootstrap`. On a drifted or fresh guest, asking
  for status would download the pinned binary, repoint a root-owned symlink and
  write managed settings. It now runs that walk with every repair turned off: a
  guest that needs one is reported, naming `torio vm bootstrap` as the remedy.
- `backend status` recovered the credential state by searching the check's prose
  for "present". The two answers are now constants shared by the backend that
  records one and the renderer that reads it, compared by equality, and anything
  else reads as **unknown** rather than as a credential the box does not hold.
- `torio brain --help` named the first backend's box and vault path. Help text is
  built before the instance is resolved and `--help` never reaches that
  resolution, so a Claude Code operator was told to run
  `limactl copy torio:/home/hermes/brain/` — the wrong box and the wrong
  directory, in a line that looks exactly right. The command now prints filled in
  by `brain status`, which knows what it just read.

### Known

- MCP inside a Claude Code box is a named hole, not a solved problem. The
  backend is a native MCP client, an operator's tokens then live under the agent
  identity, and invariant 9's "explicit, enumerable and verified" is not met for
  it. What is provided is legibility: the configured servers are enumerated by
  name, described everywhere as what is configured and never as what is
  permitted. The fix is the broker, which needs an accepted ADR first.

## 0.3.1 - 2026-08-07

The first release built from the public repository. 0.3.0's tag predates the
publication cleanup and cannot be rebuilt from it, so it carries no downloadable
assets; this release replaces it. The command surface is unchanged between the
two.

### Added

- The reference page lists all four global flags — `--json`, `--verbose`,
  `--timeout` and `--config` — with what each one does. `--verbose` and
  `--config` were real persistent flags documented only in
  [`docs/contracts/cli.md`](docs/contracts/cli.md), so they reached neither the
  site nor the runbook.
- `make validate` now derives the command surface from `internal/cli/` and fails
  when the binary exposes a command no source under `docs/content/` describes.
  That the two agreed was luck: a new subcommand could ship undocumented and
  nothing failed.

### Fixed

- The front page used `9119` for both ends of the SSH forward. Every
  operational block forwards `19119` on the Mac to the guest's `9119`, so a
  reader who started on the front page and continued into the tutorial was
  given two different local ports — the exact symptom troubleshooting explains
  as "you forwarded a different local port". The page now names `19119` as the
  host end and keeps `9119` as the guest bind.

### Changed

- `README.md` is now the project's front page: the flow as a diagram, the
  shortest true path from nothing to a working box, the boundary, the supported
  host matrix, and a roadmap. The full command surface, the global flags and the
  exit-code table live in the site reference rather than in two places at once.

### Internal

- [ADR-0008](docs/adr/0008-mcp-broker-daemon-deleted.md) deletes the fully
  dormant MCP broker daemon and relay — `internal/mcpbroker`,
  `cmd/torio-mcp-broker`, `cmd/torio-mcp-connect`, about 5,650 lines — that
  ADR-0004 had decided would stay in the repository, tested but unshipped.
  The shared policy-document parser (`ParseDocuments`, `Set`, `Grant`,
  `Digest`) moves into `internal/lima`, where the delivered `torio mcp
  install`/`status` verification already used it. ADR-0004 gains a second
  `Superseded in part by:` pointer for the one sentence this replaces; the
  custody boundary and every other "not delivered" item stand unchanged.
- The public repository is published under a rewritten commit history.
  Historical and internal records of that migration are not part of it.
- Nine source comments cited removed delivery evidence by address; each now
  carries its fact inline.
- Removed the `spikes/` tree and every coupling to it: the `make validate` step
  that ran the dogfood driver's structural assertions, the `spikes/**`
  paths-ignore entry in the platform-e2e trigger, two dead `.gitignore` rules,
  and the `CONTRIBUTING.md` paragraph describing the `v1-e2e` harness. Each
  spike had reached the ADR or contract it was run to settle, so the code was
  answering a question nobody had left. The rule that a spike lives in
  `spikes/` and never graduates into `internal/` unchanged stays in `AGENTS.md`
  §7 and `CONTRIBUTING.md`: it governs a spike someone starts, not files that
  happen to exist.
- Pre-publication cleanup audit: removed dead code, inlined or deleted stale
  comments, and trimmed duplicated or self-narrating documentation. No decision
  changed.

## 0.3.0 - 2026-08-06

Detailed notes: [`docs/releases/v0.3.0.md`](docs/releases/v0.3.0.md).

### Added

- Linux on x86_64 is a supported host. The matrix is `darwin/arm64` and
  `linux/amd64`: `torio vm init` creates a `vz`/`aarch64` instance on macOS and
  a `qemu`/`x86_64` one on Linux, both from the same pinned Ubuntu build, and
  `torio vm bootstrap` verifies the guest architecture against the host's
  profile rather than a literal. Intel Macs and arm64 Linux are deliberately
  absent — `vz` requires Apple Silicon, and nothing here has ever booted an
  arm64 Linux host
  ([ADR-0002](docs/adr/0002-lima-vm-is-the-trust-boundary.md)).
- A release carries one archive per supported host plus a single `SHA256SUMS`
  covering both, regenerated from the built set so a rebuilt archive cannot keep
  a stale line beside a fresh one. `scripts/install.sh` derives the asset name
  from `uname` instead of assuming Darwin/arm64, and refuses a platform it has
  no archive for.
- An unsupported host is refused once, up front, with the `unsupported_host`
  error code and the precondition exit code 3. The Lima adapter already failed
  closed on its own, but only inside the first command that needed an instance
  pin — after the operator had been told to install Lima and create a VM that
  could never verify.

### Fixed

- `torio project add` no longer fails closed on a working guest running Hermes
  Agent 0.19.1. That version exits non-zero from `hermes project show` for a
  project that does not exist, where 0.19.0 exited 0; Torio read the non-zero
  exit as a broken CLI, so adding the first project to a fresh VM could not
  succeed. Existence now comes from `hermes project list` output rather than
  from either exit code. `list` failing, or naming a slug `show` will not
  describe, still fails closed.

### Changed

- Closed the open question about the domain network allowlist. The
  destination-keyed egress allowlist is rejected and the `AGENTS.md` prohibition
  stands unamended; exfiltration remains unsolved and the documentation keeps
  saying so. Recorded in
  [`docs/adr/0006-destination-egress-allowlist-rejected.md`](docs/adr/0006-destination-egress-allowlist-rejected.md).
- Corrected the number of consolidated decision records from twenty to nineteen,
  the count actually held at `archive/pre-oss:docs/adr/`. The figure was wrong in
  `docs/adr/README.md`, in four places in ADR-0005, in the `0.2.0` entry below
  and in the `0.2.0` release note. `archive/pre-oss:docs/adr/0014-rename-to-torio.md`
  was claimed by both ADR-0001 and ADR-0005; the claim now belongs to ADR-0001
  alone, which carries the naming decision. ADR-0005 also said the tree was
  Polish in "seventeen of nineteen" ADRs; only `0027` was written in English, so
  the figure is eighteen.
- Added a `Superseded in part by:` header convention for ADRs, documented in
  [`docs/adr/README.md`](docs/adr/README.md) beside the immutability rule. ADR-0004
  carries the first one, pointing at ADR-0006 for the destination-allowlist
  question its "Blocked — egress control" section still presents as open. The
  header is a pointer; no superseded prose is rewritten. ADR-0004 is the only
  record in the tree that needs one.

### Internal

- Retired `spikes/003-linux-host-lima/` and its workflow. The spike's own header
  set the condition — both go once the verdict reaches the ADR it changes — and
  ADR-0002 now states the supported host matrix and the runner-nesting finding
  that made the exercise worth running. The workflow could not fire again in any
  case: its `pull_request` trigger was scoped to the spike's own paths.
- Removed `scripts/README.md`. It documented one of eleven scripts and
  advertised three checks (required files, a JSON Schema subset, examples) that
  ADR-0005 removed and that the validator's own docstring says are gone.
- Removed an unused expected-argv constant from `internal/lima/status_test.go`.
- `AGENTS.md` §7 and §10 now name `make validate` rather than
  `scripts/validate_artifacts.py`, which is one check inside it. Following the
  contract literally skipped the docs-drift and site-link checks that CI
  enforces.
- Corrected the header of `internal/config/trust_other.go`, which justified
  itself with a milestone label that no longer exists, named an unsupported host
  (arm64 Linux), cited the wrong ADR, and deferred to a file not in the tree.
- Replaced two drifted dependency counts in `CONTRIBUTING.md` and `e2e/go.mod`
  with the property they existed to state, so the next `go get` cannot stale
  them again.
- Deleted `runLive` and `runLiveGeneration2` from
  `spikes/002-remote-mcp-oauth-compatibility/live.go`. They orchestrated a full
  live OAuth flow against a real provider, and nothing called or tested them —
  the spike's "no live flow" claim held because the call site was missing, not
  because the code was. Their two exclusive helpers and the three `liveConfig`
  fields only they read went with them; every function left in the module is
  reached by a test. The spike README says what the harness proves and which of
  its pinned evidence conclusions that supersedes.
- `.gitignore` now covers the binary `go build` drops inside
  `spikes/002-remote-mcp-oauth-compatibility/`, by exact path rather than by a
  pattern that would hide real files added to a spike.

Nothing under `Changed` or `Internal` alters the behaviour of the binary.

### Not delivered

- The MCP broker daemon, relay, OAuth lifecycle, and upstream transport remain
  outside the release surface under ADR-0004.
- Exfiltration is unsolved, and after ADR-0006 it has no owner and no plan. No
  mechanism in this release partially mitigates it, and none may be described as
  doing so.

## 0.2.0 - 2026-08-05

### Fixed

- Guest commands now address the instance the operator selected: the guest
  command prefix captured the default instance before `TORIO_INSTANCE` was
  resolved, so `limactl start` and `stop` operated on the selected VM while
  every probe behind them went to the default one.
- Normalized release archive modes to `0755` for the CLI and `0644` for the
  license and release README, independent of source-file permissions.
