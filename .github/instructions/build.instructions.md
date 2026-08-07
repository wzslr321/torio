---
applyTo: "scripts/**,.github/workflows/**,Makefile"
---

# Build, validation and CI

`make validate` is the gate: docs regeneration check, site link check,
`scripts/validate_artifacts.py`, and the unit tests under `scripts/`.

## Python under `scripts/`

- Every script here has unit tests in `scripts/test_*.py`, discovered by `make
  validate`. A behaviour change with no test change is a finding.
- These scripts encode repository rules that prose failed to hold: link
  resolution, Go comments citing documents that exist, no version label on an
  operator surface, no pasteable credential, no committed secret, and full CLI
  command coverage. Weakening a check, narrowing its glob, or adding a path to
  an exemption list is a boundary change and needs a reason in the pull request.
- Standard library only. No new dependency for a repository script.

## Workflows

- Third-party actions are pinned to a commit SHA with the version in a trailing
  comment. A tag or branch ref is a finding.
- `permissions:` is declared and least-privilege. A job that gains `write` needs
  a reason in the diff.
- No `pull_request_target` that checks out the pull request head.
- No secret in an `echo`, a `run` step's output, or an uploaded artefact.
- The Go version comes from `go.mod` via `go-version-file`. A hardcoded version
  string is drift waiting to happen.

## The platform suite

`platform-e2e.yml` drives a real VM. Two properties are load-bearing and a diff
that removes either is a finding:

- cleanup runs even when the job is cancelled, retries the delete, and verifies
  the instance is gone;
- diagnostics are collected before the instance is deleted. `limactl delete`
  takes the host-agent and serial console logs with it, and those are the only
  trace a VM that refused to start leaves behind.
