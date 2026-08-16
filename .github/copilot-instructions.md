# Copilot code review instructions

Torio is a Go control plane over coding agents, Lima and Git. It provisions a VM,
a system identity and a credential boundary, so a change that looks small can
move a boundary.

[`AGENTS.md`](../AGENTS.md) is the governing contract and wins over this file.
Copilot reads it too. This file does not restate it; it says how to review
against it.

## Review in this order

1. **Boundary.** Does the change give the agent identity a capability it did not
   have, widen what a credential reaches, or move enforcement into a file the
   agent can write? Those are the findings worth interrupting a merge for.
2. **Fail-closed.** A check that cannot run must be an error, not a pass. A
   refusal must stay a refusal when the input is malformed, empty or absent.
3. **Contract drift.** CLI surface, JSON envelopes, exit codes and config keys
   are documented in `docs/contracts/`. Code and contract disagreeing is a
   defect in one of them, never an acceptable state.
4. **Correctness.** Error paths, context cancellation, partial writes, ordering
   of mutations, idempotence of anything that mutates state.
5. **Scope.** One behaviour change per branch. A refactor mixed with new
   functionality is a review finding on its own.

## Always flag

- A secret, token, private key, real hostname or Brain content reaching stdout,
  a log line, an error message, a test fixture or documentation.
- A new external process that does not go through the project's command adapter,
  or that runs without a timeout.
- A state or artefact write that is not crash-safe.
- A behaviour change with no test that would have failed before it.
- A claim in prose that the diff does not support, especially "verified",
  "proven" or "enforced" where the code only configures something.
- A capability granted to the agent identity that the PR does not name.

## Do not flag

- Formatting, import order or naming style. `gofmt`, `go vet` and `make
  validate` gate every pull request, so a comment about them is noise.
- Missing doc comments on unexported helpers.
- Generated files as if a human wrote them. See the path instructions.
- Proposals to add a dispatcher, queue, retry engine, worker pool, second Kanban
  or secret manager. `AGENTS.md` forbids all of them; suggesting one is a
  finding against the reviewer, not the author.
- Future-proofing, extension points and abstractions for cases the diff does not
  have.
- Dependency additions framed as "you could use library X" without showing why
  the existing module graph or standard library is insufficient.

## How to write the comment

- Name the invariant, contract or file the change conflicts with. A finding a
  reader cannot check is not usable.
- Give the failing input or sequence when you claim a bug. If you cannot
  construct one, say the finding is a suspicion.
- One comment per distinct problem. Do not split one problem across a file.
- If enforcement is not visible in the diff, ask where the test that proves it
  lives, rather than assuming either answer.
