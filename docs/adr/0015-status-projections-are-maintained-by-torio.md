# ADR-0015: Torio maintains bounded projections of its status document

- Status: Accepted
- Date: 2026-08-09
- Supersedes in part: [ADR-0014](0014-status-is-a-poll-of-facts.md), only
  the decision that tmux and prompt projections ship as recipes rather than as
  maintained Torio renderers
- Applies to: `internal/cli`, `docs/contracts/cli.md`,
  `docs/runbooks/ambient-status.md`

## Context

ADR-0014 made the schema emitted by `torio status --json` the interface and
treated every ambient surface as configuration outside Torio. The first tmux and
prompt recipes then duplicated the schema in `jq`: field names, the precedence
of waiting over liveness, the distinction between unknown and not applicable,
and the fallback shown when polling fails. A schema change could leave those
recipes rendering a false quiet state without any test failing.

The renderer and the surface still have different owners. Torio can maintain a
pure projection of its own document without owning `~/.tmux.conf`, `~/.zshrc`,
the process that refreshes either one, or a cache of status. That is narrower
than a watcher and does not change ADR-0014's decision that status is always
derived from a fresh poll of facts.

## Decision

Torio maintains two bounded, one-line projections of the status document:
`torio status --format=tmux` and `torio status --format=prompt`.

- A projection is a pure renderer of the same report carried by `--json`. It
  performs no additional probe, persists no state and accepts no agent-authored
  prose.
- Waiting is evaluated before every quieter state. An unknown poll renders an
  explicit failure cell rather than an empty line.
- The tmux projection may contain tmux style sequences. The prompt projection
  contains no terminal escapes because the shell must account for every
  printable character itself.
- `torio status setup tmux|zsh` may print a tested integration snippet, but it
  writes no dotfile and does not own the process or shell that evaluates the
  snippet.
- A setup snippet must work in the surface's default configuration, must not
  replace unrelated theme or status settings, and must keep a guest poll out of
  a synchronous shell prompt.

The JSON document remains the interface. These projections are maintained
opinions for the two surfaces Torio explicitly supports; other consumers build
against the document.

## Consequences

- A schema or precedence change can be tested once against the renderers that
  ship with the binary rather than copied into untested `jq` programs.
- Torio owns compatibility of the emitted line, but does not acquire a daemon,
  a `--watch` mode, a status cache or authority to edit operator files.
- Adding another maintained surface is a product decision with a renderer, a
  real-surface test and documentation, not another recipe copied from the
  schema.
- A native per-session status line remains the backend's surface. Torio's line
  answers only the cross-instance question its document owns.

## Rejected alternatives

- **Keep the renderers as `jq` recipes.** This restores the ownership wording
  from ADR-0014 but duplicates schema and precedence in files no Go test reads.
- **Write directly to tmux or shell configuration.** The operator owns those
  files; printing a snippet is sufficient and keeps installation reviewable.
- **Add `--watch` or a Torio cache.** Neither is required to render one report,
  and both would introduce long-lived state that ADR-0014 deliberately deferred.
- **Put backend-native details in the cross-instance line.** Model, context,
  cost and session prose are neither shared facts nor safe additions to the
  terminal-facing document.
