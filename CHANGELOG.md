# Changelog

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
