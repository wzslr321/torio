# ADR-0014: The OKF profile adopts `log.md`, and its one divergence is named

- Status: Accepted
- Date: 2026-08-09
- Supersedes: the "readable by anything that reads OKF" claim in
  [ADR-0010](0010-okf-vault-standard-and-brain-kit.md)
- Applies to: `brainkit/STANDARD.md`, `brainkit/commands/init.md`,
  `brainkit/examples/vault/`

## Context

OKF reserves two filenames. We adopted one.

- **§9 `log.md`** — "MAY appear at any level of the hierarchy to record the
  history of changes to that scope". `STANDARD.md` never mentions it, so the
  base format's own answer to ageing content was unavailable to us.
- **§8 `index.md`** — "Index files contain no frontmatter, with one exception: a
  bundle-root `index.md` MAY carry an `okf_version` key". Our §2.7 gives every
  `index.md` a `type`, which contradicts that.

ADR-0010 justified profiling OKF on the promise that a conforming vault is
"readable by anything that reads OKF". For index files that promise is not kept.

## Decision

1. **Adopt `log.md`** for change history scoped to a directory or the vault.
   §4's per-note `## Log` stays: scope is the axis between them, not preference.
   Structural changes go in `log.md`; what happened to one project stays in its
   note.
2. **Directory `index.md` loses its frontmatter.** `type: index` is dropped and
   `index` stops being a note type.
3. **Root `index.md` keeps `type: vault`.** §8 permits frontmatter in exactly
   that one file, and §7's vault test — the check that stops this kit writing
   into a directory that merely happens to be called `brain` — depends on it.
   It also declares `okf_version: "0.2"`.
4. **§1 stops claiming conformance it does not have.** It names the divergence
   in (3) and says why.

## Consequences

- Divergence from OKF is now one sentence about one file, down from every index
  in the tree, and it is written down rather than discovered.
- `okf_version` pins which spec we profile. OKF's major bumps may rename
  reserved filenames, so an unversioned profile was exposed to that.
- Breaking for any vault whose directory `index.md` carries frontmatter, which
  is what the `init` create path emitted. Migration is deleting those lines, and
  OKF's permissive conformance means nothing rejects the vault meanwhile. The
  kit goes to `0.2.0`, the breaking slot in `0.x`.
- The eval fixtures still carry `type: index`, deliberately: they are the
  instrument's inputs, and the committed 2026-08-08 reports were measured
  against them. Changing them is a separate decision about the baseline.

## Rejected alternatives

**Keep `type: index` and document both divergences.** A wider break for no gain:
directory indexes have no §8 exception to shelter under, and dropping their
frontmatter costs nothing we use.

**Drop `type: vault` to conform fully.** It is the vault's identifying mark and
the hook's safety property. Conformance is not worth trading for the kit's worst
failure mode.

**Autonomous supersession detection, as in [`ctx`](https://github.com/GottZ/ctx).**
It needs a daemon and a database, which §1 of the standard refuses. The portable
half — recording supersession as a relation between files — needs neither and
stays available.
