---
applyTo: "**/*.go"
---

# Go control plane

Go 1.26.x, toolchain pinned by `go.mod`. Packages under `internal/` hold the
credential boundary; `e2e/` is a separate module that must not import them.

## Processes

- Every external command goes through `internal/execx` (`execx.Runner`,
  `execx.Command`, `execx.InteractiveRunner`). A bare `exec.Command` or
  `exec.CommandContext` outside that package is a finding.
- No `sh -c` when the arguments can be passed directly.
- Every command carries a context with an explicit timeout, a captured exit code
  and redacted logging. A command whose timeout comes only from an ambient
  context deadline is worth a question.

## Context and logging

- `context.Context` is the first parameter and is threaded, not recreated.
  `context.Background()` below the command entry points is a finding.
- Logging is `log/slog` to stderr. Machine output goes to stdout as the CLI
  envelope, never through the logger.
- Anything that can carry a credential passes through `internal/redact`
  (`redact.String`, `redact.Slice`) before it reaches a log, an error string or
  an envelope. Git remotes, environment slices, argv and file contents all
  qualify.

## State

- Writes to config, state or artefacts are crash-safe: temp file, fsync, atomic
  rename. A direct `os.WriteFile` over a live file is a finding. Follow
  `internal/config` for the shape.
- A file that can hold config, a path registry or a token is created with a
  restrictive mode. Widening a mode needs a reason in the diff.
- A mutating operation is idempotent, or takes an idempotency key. Re-running it
  must not double an effect or leave a half-applied state.

## Boundary

- A Git remote must be rejected if it carries a password, token, query or
  fragment. Sanitizing it and continuing is the wrong answer.
- A workspace path is derived from the project id. A caller-supplied path that
  reaches the filesystem is a finding.
- A check that reads a file the agent can write is a drift detector, not a
  boundary, and its own comment must say so. Flag one that reads as enforcement.
- When a security check cannot complete, the operation fails. Logging and
  continuing is a finding.

## Dependencies

A new direct dependency in `go.mod` needs the pull request to say why the
standard library and current module graph do not cover the case.
