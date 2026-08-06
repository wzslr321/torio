# ADR-0007: Publication by rewritten history to a new repository

- Status: Accepted
- Date: 2026-08-06
- Supersedes: the tag-form citation rule in
  [ADR-0005](0005-repository-and-documentation-governance.md) §4 — a citation to
  removed delivery evidence is rewritten to an address under `archive/pre-oss`,
  "the address changes, the text does not". This record purges the evidence from
  every commit, so those addresses no longer resolve and the mechanism is
  retired. ADR-0005 §6 and its list of governed paths are corrected below as
  errata, which is a correction of measurement and not a supersession. Nothing
  else in ADR-0005 changes: the language rule, the consolidation of the decision
  record, and the `archive/pre-oss` tag itself all stand.
- Applies to: every commit, tag and reference in the published object graph

## Context

[ADR-0005](0005-repository-and-documentation-governance.md) prepared the working
tree for publication — English throughout, five ADRs instead of nineteen,
delivery evidence cut to a tag. It settled what the tip of the tree says. It did
not settle what the history says, and a clone hands the reader the history.

This repository was built inside an employer's context, on one laptop, in fifteen
days — 2026-07-23 to 2026-08-06 — as a stream of small pull requests. Three
classes of material are in the object graph and not in the working tree.

**Employer material.** The employer's organization name, and the name of the
private repository the first product slice targeted. The repository name is the
wider carrier by a large margin: it occurs in 113 blobs across 20 file paths, and
two of those paths carry it in the name itself — a runbook under
`docs/runbooks/` and its source under `docs/content/runbooks/`, both named after
it. The two strings do not travel together. At `archive/pre-oss` the only carrier
of the organization name is a single Polish planning document, while the
`README.md` at that tag carries 45 occurrences of the repository name and does
not mention the organization at all. A search for the organization name alone
would have found one file and missed almost everything.

**Commit messages.** Two messages reachable from `main` carry material that no
path filter and no content filter will ever touch, because neither reads a commit
message. In the archive, `331757ff` carries the employer repository URL twice
plus a guest workspace path, and `9273d2d3` carries the author's host username.
Both are ancestors of every release tag from `v0.1.0` to `v0.3.0`. This needed a
separate pass over messages, and it is worth stating plainly, because assuming
that a tree filter covers a message is the failure mode a reader would otherwise
repeat.

**The author's machine.** The host username in 36 blobs across 32 paths,
including `internal/lima/template_test.go` at older commits where it was a source
constant, and a file named `.sanitized` that still contained it. Alongside it, in
run transcripts, a machine fingerprint: timezone, macOS build version, uid, and a
per-account temporary-directory token.

This record describes those strings rather than quoting them, for two reasons.
The rewrite is a literal text replacement across every blob, so an ADR that named
them would be silently mangled by the operation it documents. And if it were
somehow exempted, publishing it would restore exactly what the pass removed.
ADR-0005 §4 already writes "the author's host username" without naming it. The
counts are the part a reader can check, and they are all here.

None of that is the load-bearing reason for a new repository. This is: GitHub
creates a `refs/pull/N/head` reference for every pull request ever opened against
a repository. It keeps that reference after the source branch is deleted, keeps
it after a force-push to `main`, and serves it on a public repository. The
reference set is not something a history rewrite reaches — it is not `main`, it
is not a tag, and it does not follow the commits it points at when they are
rewritten. So an in-place rewrite would produce a clean `main` and leave every
pre-rewrite commit — the employer material, the username, the fingerprint —
readable at a stable URL. A rewrite whose input is still fetchable has not
removed anything.

The count only illustrates the size of that surface, and it is a floor rather
than a figure: at least 106 references as of 2026-08-06 — from those fifteen days
— plus one for every pull request opened since, including the ones that carry
this decision.

## Decision

**The rewritten history is pushed to a new repository. The repository that
carries the pull-request references is renamed, kept private, and never
published.**

1. `wzslr321/torio` is a new, empty repository and receives the rewritten
   history: every commit and every tag, in one push. It has no pull requests, so
   it has no `refs/pull/N/head` to retain.

2. The original repository is renamed `wzslr321/torio-archive` and stays private.
   It keeps the unrewritten commits, every `refs/pull/N/head` reference and the
   review threads attached to them. That material is not deleted; it is put
   somewhere it is not served.

3. **Purged from every commit:** the employer's organization name and the private
   repository name, in content and in path names; the author's host username and
   host filesystem paths; the machine fingerprint; and four trees in full —
   `spikes/`, `docs/spike-results/`, `docs/v1-evidence/` and `.hermes/plans/`.

4. **Commit messages are a separate pass.** The two messages named above are
   rewritten. A filter that walks trees does not see them.

5. **Author identity is kept.** Every commit and every tag keeps
   `wiktor.zajac888@gmail.com`. The project is published under its author's own
   name; the alternative buys nothing, because the account doing the publishing
   is the same person.

6. **Model co-author trailers are kept.** They record how this was built. A
   reader deciding whether to trust a tool that provisions a VM and a system
   identity is better served by that record than by its removal.

### What was checked and found benign

A reader auditing this should be able to see the shape of the coverage rather
than take an assurance. These were searched for across the object graph, and none
of them adds anything to purge:

- **Credentials.** Every token-shaped string in the object graph traced to a
  canary — a synthetic value planted to prove the production redactor and the
  secret check actually fire.
- **SSH key material.** None, in any commit.
- **External ticket keys, coworker names.** None.
- **Public IPs, geolocation.** None beyond the timezone already named above.
- **An Atlassian identifier** that appears in configuration and in transcripts.
  A public metadata endpoint returns it for the shared service, so it is not
  tenant-scoped and identifies nobody.

## Errata to ADR-0005

An accepted ADR is an immutable record, so the corrections live here rather than
in it.

1. **§6's counts do not reproduce.** ADR-0005 states that fifty-eight files
   carried an AI-Provenance header and thirty still named the earlier harness.
   Measured at the commit immediately before the consolidation (`d329468^` in the
   archive), 57 files carried a header and 34 named the earlier harness. Counting
   only the files that survived the consolidation, 29 and 24. No denominator
   produces 58 or 30. Both measurements are given with their reference point so
   that a reader can check either, rather than guessing which the author meant.
   The decision in §6 — that the headers are removed — is unaffected.

2. **§4's tag-form citation rule is retired**, per the header of this record.
   The mechanism assumed the evidence stayed recoverable at `archive/pre-oss`,
   and it no longer does.

3. **The list of governed paths in the ADR-0005 header names `spikes/`**, which
   the working tree no longer has and this record purges from history. The other
   paths it names are unaffected.

## Consequences

- **ADR-0005 §4's mechanism is gone and the nine comments using it have to be
  rewritten.** Nine source comments cite the evidence tree in tag form to justify
  an implementation choice. A separate change rewrites each one to carry its fact
  inline. That is a real loss: an inline fact cannot rot into a broken address,
  but it also cannot be re-read in its original context by someone who doubts it.

- **Citations that address an ADR at a tag still resolve.** `docs/adr/` is not
  purged, so `git show archive/pre-oss:docs/adr/…` — the form ADR-0001 through
  ADR-0005 use in their headers, and the form the nineteen originals are
  recovered by — works unchanged.

- **Every commit hash in the published repository is new**, including the release
  tags. Rewriting a commit message rewrites the commit and every descendant, and
  the two rewritten messages are ancestors of `v0.1.0` through `v0.3.0`. Any
  address into this project written before publication resolves only in the
  private archive.

- **The archive becomes the only record of how the work was actually reviewed**,
  and it is private. The pull requests carry the argument behind many of these
  decisions. What survives publicly is what the ADRs and the changelog say, which
  is the reason both are written to stand on their own.

- **`git log` on the published repository still names one person and every model
  that co-wrote a commit.** Someone who wants to know how much of this was written
  by a model can count it, which is the intended effect of keeping both.

- **The purge has to be verified, not assumed.** The measurements in the context
  section are the checklist: a filter that leaves the organization name and misses
  the repository name would look successful against a grep for the employer.

## Rejected

- **Make the existing repository public as it stands.** Nothing to build and the
  history stays honest. It also publishes an employer's private repository name in
  113 blobs, the author's host username in 36, a machine fingerprint, and two
  commit messages naming a repository the author has no standing to disclose.
  Publication is not a reason to publish everything.

- **Rewrite in place and force-push.** The obvious move, and the one this record
  exists to reject. Every `refs/pull/N/head` reference survives the force-push and
  is served on the public repository, so the clean `main` sits next to a complete
  copy of what it removed, at stable URLs. This is the load-bearing reason for a
  new repository and not a secondary concern.

- **Squash to a single initial commit.** Removes everything in one move, needs no
  filter, no measurement and no commit-message pass. It also removes the thing
  worth publishing: a reader can no longer see when a decision was made or what it
  replaced, ADR-0005's account of the consolidation becomes unverifiable, and the
  co-author trailers reduce to one line. It also does not solve the problem:
  squashing in place retains the pull-request references exactly as a rewrite in
  place does.

- **Ask GitHub support to purge the pull-request references.** This is a
  supported request and it would make an in-place rewrite sound. It makes
  publication depend on a third party's manual action, on a timescale nobody
  controls, with no way to verify completeness from outside and no way to withdraw
  the exposure if it was incomplete. An empty repository requires nobody's
  cooperation and its emptiness is checkable in one request.

- **Keep the evidence tree and purge only the employer material.** The cheapest
  option, and it would preserve ADR-0005 §4's citation mechanism and the nine
  comments that use it. The evidence is run transcripts from one laptop: the host
  username in 36 blobs across 32 paths, plus the timezone, the macOS build, the
  uid and a per-account temporary-directory token. Redacting them in place
  destroys the fidelity that made them evidence, which ADR-0005 rejected on its
  own terms; keeping them unredacted publishes the machine. The tag that was
  supposed to hold them byte-for-byte is itself inside the history being
  published, so "moved to a tag" stopped being a place to put things.
