# AGENTS.md — normative instructions for implementers and LLMs

This file is the governing work contract for the Torio repository. If another
document or prompt contradicts it, **stop and report the conflict**. Do not
resolve a contradiction by guessing.

## Product status

The delivered product is the `torio` binary: VM lifecycle, a loopback Hermes
backend, a Second Brain, a multi-project registry, operator-carried push, and the
MCP custody boundary. The operator-facing surface is [`README.md`](README.md) and
one runbook, [`docs/runbooks/first-run.md`](docs/runbooks/first-run.md). Both
describe the delivered binary; a disagreement between them and the code is a
defect to fix, not a state to accept
([ADR-0005](docs/adr/0005-repository-and-documentation-governance.md)).

**Internal milestone labels do not appear on the user surface.** README, `site/`,
`docs/runbooks/` and operator-visible CLI strings do not say "V0" or "V1"; the
operator reads the version from `torio version`.

> Runbooks and the pages under `site/` are **generated** by
> `scripts/build_docs.py` from sources in `docs/content/`, and they share
> sections so they cannot drift apart. Do not edit a generated file — change the
> source and run `make docs`. `make validate` fails when output differs from
> source.

Earlier exploration — a worker platform, admission control, per-task isolation, a
fresh verifier, a staged roadmap — is **not in the working tree**. It is under the
tags `archive/pre-v1` and `archive/pre-oss`. Do not reactivate it and do not treat
it as the next task.

## 1. Mission

A thin, trusted control plane over Hermes Agent, Lima and Git. Not a new agent
framework, not a task queue, not a general worktree manager.

## 2. Normative words

- **MUST** — unconditional requirement.
- **MUST NOT** — unconditional prohibition.
- **SHOULD** — the default decision; departing from it requires an ADR.
- **MAY** — an option.

## 3. Sources of truth

In order of authority:

1. `AGENTS.md`.
2. Accepted ADRs in `docs/adr/`.
3. Contracts in `docs/contracts/`.
4. [`docs/03-architecture.md`](docs/03-architecture.md).

Current Hermes Agent documentation and source are the truth about Hermes. Do not
use model memory to guess its commands, ports, options, paths or lifecycle.

## 4. Fixed boundaries

Scope and rationale: [ADR-0003](docs/adr/0003-ownership-split-and-operator-carried-write.md)
and [`docs/03-architecture.md`](docs/03-architecture.md).

### Hermes Agent owns

- model execution;
- profiles, sessions and memory;
- the agent-side project registry;
- Kanban, dispatch and retry.

### Torio owns

- Lima lifecycle, provisioning and guest verification;
- the non-secret declaration of attached projects (`config.json`);
- derivation of workspace and vault paths;
- the short-lived operator session that is the only carrier of write capability
  against an origin;
- the MCP custody boundary.

### Torio MUST NOT implement

- an alternative agent loop or a second Kanban;
- its own dispatcher, queue or retry engine;
- autonomous merge, push or release;
- per-task workers or a verifier platform;
- a Vault-class secret manager, or a domain network allowlist.

> **Open question — the domain network allowlist.** The last item conflicts with
> the destination-allowlist half of
> [ADR-0004](docs/adr/0004-mcp-credential-custody-and-egress.md), which is the
> only part of the egress work that addresses data exfiltration at all. The
> conflict was raised rather than resolved, and it stays open: either this
> prohibition is amended, with the reasoning recorded in a new ADR, or the
> allowlist is rejected and the documentation keeps saying plainly that
> exfiltration is unsolved. **Until one is chosen, nothing is built on it.**
> Choosing is the maintainer's call, not the implementer's.

## 5. Security invariants

Every implementation MUST preserve:

1. Repositories, the Brain and state live on the VM's native filesystem, never on
   a broad macOS mount.
2. The Hermes profile is not a sandbox; the boundary is the edge of the VM.
3. The `hermes` service identity MUST NOT be in the `docker` group or reach
   `docker.sock`.
4. `/home/hermes/.hermes` (profile) and `/home/hermes/brain` (vault) are
   distinguished in code and in documentation.
5. A workspace path is derived from the project id, never supplied by the user.
6. A Git remote MUST NOT contain a password, token, query or fragment.
7. The persistent `hermes` identity has read-only access to an origin;
   `ssh.forwardAgent` is disabled globally.
8. Write capability **against a Git remote** comes only from a `torio project
   shell` session and ends with it.
9. Write capability granted by an MCP server is a **separate channel**: it does
   not pass through `project shell`, does not end with a session, and its scope
   MUST be explicit, enumerable and verified
   ([ADR-0004](docs/adr/0004-mcp-credential-custody-and-egress.md)).
10. The guest-side operator-session helper is `root:root 0755`; drift is
    reported, never repaired.
11. Push, merge and release are separate, human-only operations outside the CLI.
12. Brain transport is one-shot and bounded; payload content never reaches
    stdout, logs or evidence.
13. The Brain is not injected into a prompt — cross-project access goes through a
    retrieval skill.

A control MUST NOT live in a file the agent can write. Hermes hooks and
`mcp_servers.<n>.tools.include` are both that trap. Where a check has to read
such a file anyway, its own comment MUST say that it is a drift detector and not
a boundary.

## 6. Implementation rules

- Control plane language: Go 1.26.x, toolchain pinned in the repository.
- Use `log/slog`, `context.Context`, explicit timeouts and
  `os/exec.CommandContext`.
- Do not run a command through `sh -c` when the arguments can be passed directly.
- Every external command has a typed adapter, a timeout, a captured exit code and
  redacted logs.
- Do not import Hermes' private Python modules. Use its verified CLI, API or
  plugin contract.
- Do not write to `~/.hermes/kanban.db` directly.
- A mutating operation is idempotent or requires an idempotency key.
- Every state and artefact write is crash-safe: temp file → fsync → atomic
  rename.
- Machine output is a stable JSON envelope; human logs go to stderr.
- Secrets and credential examples are written only as `[REDACTED]`.

## 7. TDD and workflow

For every behaviour change:

1. Write one failing test.
2. Run it and confirm it fails the way you expect.
3. Implement the minimum.
4. Run the test and the whole relevant package.
5. Refactor with the tests green.
6. Run `python3 scripts/validate_artifacts.py`, then `go test ./...`.
7. Make a small commit.

No production code before a failing test. A spike may create throwaway code in
`spikes/` only, and spike code never graduates into `internal/` unchanged — it is
rewritten test-first or removed.

## 8. Evidence requirement

Do not claim "it works" on the strength of documentation. Record the actual
command, runtime versions, the exit code, relevant redacted output, the date, the
conclusion, and the effect on an ADR or contract.

## 9. Rules for an LLM

- Read the relevant contracts before writing anything.
- Change one vertical behaviour slice per task.
- Do not widen scope "for the future".
- Do not build compatibility with unverified Hermes versions.
- Do not add a mechanism merely because a library offers it.
- If a security requirement is technically unachievable, fail closed and record
  the problem.
- Never replace enforcement with a prompt instruction.
- Never edit an ADR silently. A new decision requires a new ADR superseding the
  old one.

## 10. Definition of done

A task is done only when acceptance criteria are met, a test failed and then
passed, regressions are green, output and logs carry no secrets, contracts and
documentation are updated, `scripts/validate_artifacts.py` passes, and a reviewer
can reproduce the result from the recorded commands.
