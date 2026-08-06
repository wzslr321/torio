# Changelog

## Unreleased

### Changed

- Closed the open question about the domain network allowlist. The
  destination-keyed egress allowlist is rejected and the `AGENTS.md` prohibition
  stands unamended; exfiltration remains unsolved and the documentation keeps
  saying so. Recorded in
  [`docs/adr/0006-destination-egress-allowlist-rejected.md`](docs/adr/0006-destination-egress-allowlist-rejected.md).

### Fixed

- `torio project add` no longer fails closed on a working guest running Hermes
  Agent 0.19.1. That version exits non-zero from `hermes project show` for a
  project that does not exist, where 0.19.0 exited 0; Torio read the non-zero
  exit as a broken CLI, so adding the first project to a fresh VM could not
  succeed. Existence now comes from `hermes project list` output rather than
  from either exit code. `list` failing, or naming a slug `show` will not
  describe, still fails closed.

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

No behaviour of the binary changes.

## 0.2.0 - 2026-08-05

Detailed notes: [`docs/releases/v0.2.0.md`](docs/releases/v0.2.0.md).

### Added

- `torio mcp status` and `torio mcp install` report the grant they verified:
  each service in the policy directory, its upstream endpoint, and its tool and
  write-tool counts, as a `policy` object under `--json` and as a listing in
  human output. Services are enumerated from the documents, so a second or third
  provider needs no CLI change.

### Fixed

- Guest commands now address the instance the operator selected. `TORIO_INSTANCE`
  is resolved after package initialization, but the guest command prefix had
  captured the default instance before that, so `limactl start` and `stop`
  operated on the selected VM while every probe behind it went to the default
  one — and the report printed the selected name over evidence from a machine it
  never touched.
- Normalized release archive modes to `0755` for the CLI and `0644` for the
  license and release README, independent of source-file permissions.

### Changed

- Preserved `changed` and `restart_required` in `torio mcp install` failures
  after partial guest provisioning, with actionable redacted remediation.
- Documented the released MCP custody boundary in the README and generated
  command reference.
- Prepared the repository for open source: five English decision records in
  place of twenty, a stated threat model, and roughly 330 files of run
  transcripts, spike results and internal plans out of the working tree. The
  prior tree is readable at `git show archive/pre-oss:<path>`.

### Internal

- Two end-to-end levels behind build tags, in a module of their own: a
  compiled-CLI suite against an in-process `limactl` fake that gates every pull
  request, and a real macOS arm64 journey against real Lima that gates every
  release.
- A throwaway harness pinning remote MCP OAuth compatibility for the Atlassian
  and Linear profiles against an in-process fake provider. It implements no live
  flow and can reach no real provider.

### Not delivered

- The MCP broker daemon, relay, OAuth lifecycle, and upstream transport remain
  outside the release surface under ADR-0004.
- The Atlassian result remains `PARTIAL`: the public probe stops before browser
  authorization, token issuance, content access, or write.
