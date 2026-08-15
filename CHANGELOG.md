# Changelog

## Unreleased

### Added

- **Codex CLI as the third backend.** `torio vm init --backend codex` builds a
  box that runs Codex as its own guest identity, with the same custody the other
  process backend has: no sudo, an exact group set, and a pinned binary the agent
  cannot rewrite. The archive is proven against a digest committed in this
  repository rather than one fetched beside it, because the release publishes
  checksums only for a different set of artifacts. Sessions, the Second Brain
  retrieval skill, the waiting marker `torio status` reads, and the MCP relay all
  work as they do elsewhere; what differs is where the controls live, which is a
  root-owned system configuration layer and a root-owned MCP allowlist under
  `/etc/codex` ([ADR-0022](docs/adr/0022-codex-backend.md)).
- Signing a Codex box in asks for a device code, so a box with no browser needs
  no forwarded port to receive a credential. An API key remains available through
  `codex login --with-api-key`, which reads the key from standard input.
- **A dev channel.** Every `main` commit whose `ci` run went green is published
  as a prerelease on the `dev` tag, and `scripts/install.sh --channel dev`
  installs it beside a stable install, as `torio-dev`. The two get separate
  directories because the guest payload names are fixed and cannot be shared.
  Checksums are verified as they are for a release; the release gates that boot
  a guest and install the macOS archive have not run against a dev build, and
  the installer, the release notes and the docs each say so. A prerelease is not
  named by `releases/latest`, so installing without `--channel` is unchanged.
- **`make local`.** Builds the working tree for this host and installs it as
  `torio-local` through the same installer, so a branch can be tried on a real
  machine without publishing anything. The version it reports carries the
  branch, the commit and whether the tree was dirty. A release, a dev build and
  a working-tree build can be installed at the same time; each keeps its own
  directory and its own name.
- **One Second Brain, with the host as its hub.** The canonical vault is now a
  Git worktree on the host, under
  `${XDG_DATA_HOME:-~/.local/share}/torio/brain/vault`, and each backend's guest
  keeps a replica of it. `torio brain sync`, or `y` on the hub's Brain tab,
  reconciles the bound guest with the host both ways by carrying Git bundles
  over the same one-shot transport `brain import` uses. Unsaved work in a guest
  vault is committed first, because an agent writes notes and never commits
  them. Neither vault gains a network remote, and no host mount is introduced. A
  merge that cannot be made automatically stops that direction, leaves it as it
  was, and names the host vault, where it is resolved with ordinary Git.
  `brain status` now reports how far the replica is from the hub
  ([ADR-0025](docs/adr/0025-one-second-brain-with-the-host-as-its-hub.md)).
- **A rebind reconciles the Second Brain on both sides of the move.** Pressing
  `b` and picking another backend syncs the Brain of the box being left before
  the binding changes and the Brain of the box arrived at right after, so the
  notes the operator just wrote are on the box their attention lands on. The
  hub's note reports what each side carried, in counts. Neither sync can fail
  the rebind: a box that cannot sync — stopped, or its vault uninitialized — is
  reported in that note and the move lands anyway, and a merge conflict names
  the host vault where it is resolved
  ([ADR-0026](docs/adr/0026-a-rebind-reconciles-the-second-brain-it-crosses.md)).
- **Opening a session materializes the checkout it needs.** The registry is
  shared by every instance and the checkouts are not, so a project attached
  under one backend is registered and absent under the next. Pressing enter on
  it in the hub, or running `torio project agent`, `enter` or `shell`, now
  clones it from the remote on record and then opens the session, instead of
  reporting drift and naming a command to go and run. Only a checkout that is
  simply not there is made this way: a checkout that exists and disagrees with
  the record is a working tree, and it is refused as before. The clone reaches a
  remote, so it still stops with the deploy key to authorize where the guest may
  not read one
  ([ADR-0024](docs/adr/0024-a-session-materializes-the-checkout-it-needs.md)).
- **`torio project set-remote <id> <remote>`.** Corrects the remote of a project
  already on record, without removing the entry, the checkouts other guests
  hold, or the deploy keys those guests had authorized. The registry is shared,
  so the correction applies to every backend. The checkout on the selected
  backend's guest is repointed when its origin still holds the remote being
  replaced; any other origin is reported and left alone. The hub offers the same
  correction on `e`, prefilled with what the record holds
  ([ADR-0023](docs/adr/0023-recorded-remotes-are-resolvable-from-a-guest.md)).

- **More of the surface reaches the hub.** The dashboard stops the bound box on
  `x`, asking first, because stopping a box takes the agent sessions it is
  carrying with it. The project screen opens a detail panel on `v`, which is
  `project show`: what the guest holds and the markers naming what drifted,
  without leaving the hub to find out why a session refused.

### Fixed

- **A host the guest cannot resolve is named as one.** A remote whose host only
  the operator's machine knows, which is what a host-local SSH alias is, used to
  fail as though an authorization were missing, and offered a deploy key to
  authorize. Authorizing it changed nothing: the guest had never reached the
  forge to present it. The run now reports the host that did not resolve and
  names the command that corrects the record.
- **The hub preflights a session before it opens one.** Opening an agent session
  or a shell from the project screen now runs the same checks
  `torio project agent` and `torio project shell` run, so a checkout that is not
  on this backend's guest is named as such, with the command that reconciles it.
  It previously reached the guest helper with a path nothing had verified and
  came back as a bare exit status, which the repaint had already erased the
  reason for.
- A session that ends non-zero keeps the end of what it wrote on screen, under a
  banner that stays until it is dismissed.
- The hub's add form accepts the id alone, as it always said it did. The remote
  and the display name come from the shared registry, which is what materializes
  an already-registered project in another backend's guest.
- The project screen stops offering `u use` on a backend that keeps no registry.
  It had no registry to select in and the key always failed.
- Drift and `project show` name `torio project add <id>`, the form that
  reconciles a registered project from the remote on record, rather than a bare
  verb or a form that asks for a remote Torio already holds.

## 0.3.4 - 2026-08-10

### Added

- **An interactive hub.** Bare `torio` on a terminal opens setup, cross-box
  status, projects, the Second Brain, and the guest service. `torio ui` opens it
  explicitly. Non-terminal and JSON behavior remains unchanged
  ([ADR-0019](docs/adr/0019-bare-torio-opens-the-hub.md)).
- **Private SSH repository setup.** `torio project add` generates a guest-held
  deploy key and prints its public half. Add it to that repository with write
  access off, then rerun the command. Torio neither reads the private half nor
  verifies the forge setting
  ([ADR-0018](docs/adr/0018-guest-held-deploy-key-for-read-access.md)).

### Fixed

- TUI operations and interactive sessions stop when the hub context is canceled.
- `torio status` pads columns instead of relying on terminal tab stops.
- User documentation now matches deploy-key setup, TUI refresh behavior,
  credential custody, editor support, and JSON availability.

### Internal

- Removed test-only production wrappers, redundant TUI setup, and the static
  screenshot/video pipeline. The recorded hub GIF and its VHS source remain.

## 0.3.3 - 2026-08-09

**Migration first: a Claude Code box bootstrapped before this release reports
settings drift and refuses.** Bootstrap now installs the waiting-marker hooks
into the managed settings, and drift is reported, never repaired in place.
Remove `/etc/claude-code/managed-settings.json` on the guest and run
`torio vm bootstrap --backend claude-code` again; a Hermes box needs nothing.

### Added

- **`torio status`, one row per box.** Running several agents, there was no way
  to ask which of them needs you. This polls every box Torio owns and reports
  the box state, the backend, what it has running, whether anything there is
  waiting on a human, and when it last provably did work. Every field is a
  proven value, `?` for a question that could not be answered, or `—` for one
  that backend does not answer at all — never a zero standing in for a silence.
  It exits 0 whenever the poll completes, so a status bar can call it on a
  timer; only failing to list the boxes at all is an error
  ([ADR-0017](docs/adr/0017-status-is-a-poll-of-facts.md),
  [contract](docs/contracts/status.md)).
- **The status line, and the one command that installs it.**
  `torio status --format tmux|prompt` collapses the report onto one line for a
  status bar or a shell prompt, and `torio status setup tmux|zsh` prints the
  configuration that puts it there. `setup` prints and nothing else: a dotfile
  belongs to you, and the snippet names the file it goes in so the placing is
  your step. The snippet calls the binary that printed it rather than `torio`,
  because an older one earlier on `PATH` exits 2 and every such surface renders
  that as an empty line. A poll that failed prints `torio: ?` rather than
  nothing, for the same reason.
- **A fourth declarable capability.** A backend declares the name its sessions
  run under, the files whose modification time proves work, and whether its
  hooks write the waiting marker. Each is separately declarable and each
  omission is an answer rather than a gap. Claude Code declares its sessions and
  its marker; Hermes declares its work, and says unknown about a waiting
  question it cannot answer from disk.
- **Waiting-marker hooks on a Claude Code box.** `torio vm bootstrap` installs a
  root-owned helper and names it in the managed settings, so the agent cannot
  silently retune the hook integration between sessions. **A box
  bootstrapped before this reports settings drift and refuses**: drift is
  reported, never repaired in place, so remove
  `/etc/claude-code/managed-settings.json` and run bootstrap again.
  The helper keys an atomically rewritten fixed document by Claude's validated
  `session_id`, so clearing one session no longer clears another session still
  waiting on the same box. The marker remains agent-owned operational evidence,
  not a security boundary.
- **Mediated agent forwarding: a shell that can push forwards one key, and
  every signature stops at a person**
  ([ADR-0015](docs/adr/0015-mediated-agent-forwarding.md)). Config schema `"4"`
  adds `operator_key` — a fingerprint or comment naming the one identity a
  session may use. With it set, `project shell` points the guest's
  `SSH_AUTH_SOCK` at an agent Torio serves in front of the operator's own: it
  lists the pinned key alone, asks the operator on the host before each
  signature — a dialog naming the project, where origin actually pushes, the
  branch and how far ahead it is, with Deny as the default, the cancel and the
  timeout — and refuses every other request in the protocol without writing it
  to the real agent. Each decision is recorded to `agent-audit.jsonl` beside
  the config document *before* it takes effect; a decision that cannot be
  recorded is not taken. With no pin the session forwards the operator's agent
  whole, exactly as before — a document with no `operator_key` was written by
  an operator who has not chosen a key, and choosing one for them is choosing
  which key a guest may use.
- **`torio project agent <id> --push-grant`: a granted session that may ask to
  push, one signature at a time**
  ([ADR-0016](docs/adr/0016-session-scoped-push-grant.md)). Refused outright
  without a pinned key. The mediated socket is remote-forwarded into the guest
  on a single-use random path that lives exactly as long as the session, and
  reaches the backend identity through a helper that validates the socket
  before widening it to the shared group — the ordinary session helper remains
  provably free of `SSH_AUTH_SOCK`. A preflight refuses an origin the grant
  cannot serve, each with its remedy: an HTTPS push URL never consults an SSH
  agent, and a host key absent from the agent identity's `known_hosts` stops a
  push before it reaches the key — as `Host key verification failed`, which
  reads like a problem with the key just pinned and is not one. An operator
  shell is told the same things and opens anyway: a shell is opened to read and
  commit as often as to push.

### Fixed

- **`project shell` and `project enter` were unusable on any backend but the
  first.** Both guest helpers named `/home/hermes/projects` outright, so on a
  Claude Code box the host derived the right path and the guest refused it —
  `project path is not a project directly under /home/hermes/projects`, exit 64,
  on the one command that carries write capability. The two shared helpers now
  take the declared backend's workspace, substituted on both install paths, and
  a test refuses a workspace written into either of them so the third backend
  does not rediscover this.
- **A corrected session helper could not reach a box that already existed.** The
  Lima template is rendered once, at `vm init`, and bootstrap verified the
  push-capable helper without ever installing it, so the only route to a new
  version was recreating the VM. Bootstrap now installs it when the path is
  absent, as it already did for the other session helpers. Drift is still
  reported and never repaired: a helper that is present and wrong is left alone.

### Changed

- **The vault standard stopped forbidding what vault owners actually ask for.**
  `STANDARD.md` §6.7 said "Do not commit… an agent writes files and stops",
  which read as an absolute and was one: an owner who had explicitly asked for a
  commit per meaningful change got an agent citing the standard back at them.
  The rule now states the fact it was built on — the history belongs to whoever
  set the vault up — makes committing the owner's call, and puts the hard line
  where it belongs. Push, pull requests, issues and anything else that sends the
  vault outward are refused regardless of what anyone asked for, because a commit
  is local and reversible and nothing past it is. `brain-search` and the
  `brain-librarian` subagent are aligned; the librarian still commits nothing,
  now for its own stated reason rather than a blanket ban — it runs a bulk pass
  whose result nobody has read yet.
- **The session-start map's budget is written down where an index author will
  meet it.** The hook carries the first 25 lines of the root `index.md` below
  its frontmatter and drops the rest in silence, and nothing said so — not
  `STANDARD.md`, not `/brain-kit:init`, not the kit's README. An index long
  enough to lose its tail therefore lost it invisibly, and the tail is where a
  `## Now` section tends to sit. §2.7 now tells index authors that the opening
  lines are what gets carried; §9 requires a rendering to bound the map **and**
  to state its bound somewhere the author will find it; the README and the
  `init` command name the 25 lines outright.
- The Brain Kit is `0.2.0`, and the bump is a breaking one in the `0.x` sense.
  §9 gained a requirement renderings must meet, so a rendering that documents no
  bound stops conforming, and §2.7 drops frontmatter from directory indexes, so
  a `0.1.0` vault whose `projects/index.md` carries `type: index` no longer
  conforms either. Removing those four lines is the whole migration, and OKF's
  permissive conformance means nothing rejects such a vault meanwhile.
- **The vault standard adopts `log.md` and stops overclaiming OKF conformance**
  ([ADR-0014](docs/adr/0014-okf-profile-divergence-and-log-files.md)). OKF
  reserves two filenames and we had adopted one: `log.md` — change history
  scoped to a directory — was missing entirely, which is why ageing content had
  no answer in the format we chose precisely for having one. Meanwhile every
  `index.md` carried a `type`, where the base format permits frontmatter in the
  root index alone. Directory indexes lose their frontmatter and `index` stops
  being a note type; the root keeps `type: vault`, because it is what stops this
  kit writing into a directory that merely happens to be called `brain`, and it
  now declares `okf_version` so a future major bump cannot rename a reserved
  filename underneath us. §1 names that one divergence instead of claiming there
  is none.
- `docs/adr/README.md` lists ADR-0012 and ADR-0013, which shipped in 0.3.2
  without reaching the index.
- **The tree is linted, and the gate is committed.** `golangci-lint` runs the
  standard set on a committed `.golangci.yml`, locally as `make lint` and in
  CI; every exception the config makes is written down where it is made. The
  first run's findings are fixed: the hermes-specific spellings the backend
  generalisation left behind are deleted along with the serve adapter's
  caller-less accessors, error-path closes discard their results explicitly,
  and duplicated struct copies became conversions.
- The site caught up with the product: the home page states the
  one-VM-per-agent rule and hands the several-boxes question to `torio status`,
  and mediated forwarding with the push grant reached the push how-to, the
  explanation page and the README. The per-release notes directory is gone;
  the CHANGELOG and generated release notes are the record, and CONTRIBUTING
  now states the release procedure that previously existed only as workflow
  YAML.

## 0.3.2 - 2026-08-09

### Added

- **The MCP broker now carries traffic through the custody boundary.** Release
  archives ship Linux guest broker and relay payloads for each supported host.
  `torio mcp install` atomically installs them and the systemd unit, verifies
  root-owned exact policy, and wires either backend to a credential-free stdio
  relay. The broker uses the official MCP Go SDK v1.7.0 for Streamable HTTP,
  validates every granted tool against upstream discovery, exposes only the
  intersection, obtains caller uid from the Unix socket, and audits decisions
  without arguments or results
  ([ADR-0012](docs/adr/0012-mcp-broker-transport-and-oauth.md)).
- **`torio mcp login <service>`.** OAuth is an explicit interactive operator
  action using discovery, dynamic client registration and PKCE S256 through a
  loopback-only SSH callback forward with no SSH agent. Tokens and refresh state
  are crash-safely stored as `torio-mcp`, never under the agent uid or on the
  host. The unit starts only after every policy service has logged in.
- Claude Code now uses root-owned managed MCP configuration with
  `allowManagedMcpServersOnly`; native entries are removed from its agent-owned
  file and rejected by status. Hermes' agent-owned declaration is reconciled
  and checked as drift, while authorization remains the root-owned broker policy
  ([ADR-0013](docs/adr/0013-mcp-managed-client-config-and-activation.md)).

- **What the brain does on its own is measured, not asserted.** The kit's claim
  was always behavioural — that an agent reaches for the vault when a task turns
  on something written there, leaves it alone when it does not, cites the note it
  used, and records a correction so the next session does not need it. Nothing
  checked any of that, and reading the skills would not have caught either of the
  two defects the first manual pass found. `brainkit/evals/` now holds
  backend-neutral scenarios across four families — linkage, precision, retrieval
  and self-update — run by `make brain-evals` against a fixture vault, asserted
  mechanically against the vault diff and the answer, and reported with per-run
  cost and the list of skills that were loaded. Assertions a runner cannot
  observe are reported skipped, never passed. There is no CI gate yet, on
  purpose: the cadence decision waits for a measured cost
  ([ADR-0011](docs/adr/0011-measured-brain-behaviour.md)).
- **A map of the vault reaches the agent before the first prompt.** Whether a
  question turns on something you wrote down is a judgement worth leaving to a
  model; whether the vault exists is not. The kit now ships a session-start hook
  that puts the vault's path, its root index, and its directories with their
  counts into context — never note bodies — and stays silent when there is no
  vault or the path fails the `type: vault` test. `STANDARD.md` §9 states the
  requirement in backend-neutral terms, so a rendering other than the plugin owes
  the same thing.
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

- The Hermes `VerifyIsolation` comment claimed the backend held no authority
  beyond its own work while the implementation proved only absence from the
  Docker group. The contract now states exactly the fact it proves, without
  broadening a security claim beyond its evidence ([#13](https://github.com/wzslr321/torio/issues/13)).

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
