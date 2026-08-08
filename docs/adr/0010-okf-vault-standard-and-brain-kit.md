# ADR-0010: The vault has a written standard, and the standard ships as a kit anyone can install without the VM

- Status: Accepted
- Date: 2026-08-08
- Applies to: `brainkit/`, `.claude-plugin/`, `README.md`

## Context

Torio's first line says "Your AI second brain". The control plane under that
sentence is real — a versioned Markdown vault on the guest's own filesystem,
outside every checkout, reachable only through a retrieval skill (invariant 13).
The brain itself is not. `torio brain init` writes six empty directories, a
`README.md` describing them, an `AGENTS.md` with seven house rules, and a
`todo.md`. One skill per backend reads it. Everything that makes a second brain
worth keeping — what a note *is*, when capture becomes a routed note, what a
meeting note owes the people in it, what gets reviewed weekly — was left for the
operator to invent, in their own vault, undocumented.

That has two costs. The first is drift: with no written shape, the vault's
actual shape is whatever the last agent guessed, and the next agent guesses
again. The second is that nothing else can read it. A vault whose conventions
live only in one skill's prose is legible to that skill and to nothing else — no
second tool, no exporter, no successor.

There is also an adoption question, and it is not cosmetic. Today the vault sits
behind the VM: to get the second brain you accept Lima, a pinned image, a guest
identity, bootstrap verification. That order is backwards for how people arrive.
The brain is the part someone wants on day one and can evaluate in ten minutes;
the box is what they want on day thirty, once the agent reading their notes is
also running their code. Shipping only the bundle asks for the day-thirty
decision first.

## Decision

### The vault format is written down, as a profile of OKF

`brainkit/STANDARD.md` is the one normative description of a Torio vault. It is
a *profile* of the Open Knowledge Format: Markdown files with YAML frontmatter,
`type` the only required field, `title`/`description`/`resource`/`tags`/
`timestamp` reserved with fixed meanings, relative Markdown links forming the
graph, `index.md` as a directory's curated entry point. Torio adds what a base
format deliberately leaves open — a taxonomy of eight note types, file-naming
rules, which links a note of each type owes, and the rituals that maintain them.

Reusing a published base rather than inventing a vocabulary is the whole point:
the frontmatter is not ours to design, only ours to constrain. A vault written
to this standard is readable by anything that reads OKF, and the parts that are
ours are visibly the parts that are ours.

Rejected: an Obsidian-flavoured format — wikilinks, Dataview queries, a
`.obsidian/` directory. Wikilinks are not Markdown; they resolve against one
application's index, so `scripts/validate_artifacts.py` cannot check them and a
plain reader cannot follow them. Binding a private, long-lived corpus to one
editor's resolution rules is the opposite of what a standard is for.

Rejected: a heavier ontology with required fields per type. Every required field
is a thing an agent can get wrong and a human will not fix. The standard
requires `type`, states the rest as conventions, and says which conventions the
skills rely on.

### The standard is content; content lives in a kit, not in the binary

ADR-0009 established that a backend declares its own retrieval skill, because
that document names the tools one agent has and the vault path one identity
owns, and there is no backend-neutral wording to share. That reasoning is about
*skills*. It does not reach the *standard*, which describes Markdown files —
files have no tools and no uid.

So the boundary is: **the kit is content, Torio is mechanics.** The kit says
what a note looks like and how a week is reviewed. Torio creates the vault,
owns its path, versions it, installs the skill and proves the install. Neither
half needs the other to be correct, and the kit's own README is written for a
reader who will never run `torio`.

### The kit ships as a plugin, from a marketplace in this repository

`.claude-plugin/marketplace.json` at the repository root publishes one plugin,
`brain-kit`, sourced from `brainkit/`. A reader with Claude Code and no VM runs
two commands and has the second brain. A reader who later runs `torio vm init`
gets the same vault shape inside the box, because both are written against the
same `STANDARD.md`.

Rejected: a separate repository for the kit. It would need its own issues,
releases, CI and README, and it would split the attention of a project that has
not yet earned one audience, let alone two. The kit is a folder with a hard
boundary, not a second product. If it ever outgrows the repository, moving a
self-contained directory out is a smaller operation than merging two histories
back together.

Rejected: publishing through a third-party skill index instead. That is a
distribution channel, not a home; a channel can be added later over the same
directory, and adding one does not require the source of truth to live there.

### This decision changes no Go

The kit lands as Markdown and two manifests. Nothing under `internal/`, `cmd/`
or `e2e/` is touched, and the acceptance criterion is literal: a diff against
`main` with no path under those directories.

That is affordable because of one clause in the standard. **A note without
frontmatter is valid to read; frontmatter is added when the note is next edited
in a meaningful way.** The vault Torio scaffolds today therefore already
conforms — it is a conforming vault with nothing written in it yet — and
aligning the Go templates becomes ordinary follow-up work rather than a
prerequisite. The clause is not a transitional courtesy either: a decade-old
notes directory someone imports is in exactly the same position, and a standard
that rejects it on arrival is a standard nobody adopts.

## Consequences

- Two documents now describe a Torio vault: `brainkit/STANDARD.md` and the
  templates under `internal/brain/templates/`. They agree today because the
  templates say less, not because anything checks. Aligning them — frontmatter
  in the scaffold, a per-directory `index.md`, and the question of whether the
  vault's root document is `README.md` or `index.md` — touches `brain status`
  checks and golden tests, so it is its own change.
- `brainkit/skills/brain-search/SKILL.md` and the per-backend skill templates
  are two renderings of one retrieval discipline, maintained by hand. That is
  accepted duplication with a known end: when `backend.BrainSkill` becomes a
  *set* of named skills rather than one payload, the backends install renderings
  of the kit's skills through the existing content-addressed channel. That is a
  contract change and gets its own record.
- `scripts/validate_artifacts.py` walks every `*.md` in the repository, so every
  relative link in the kit is checked on every run. Example links therefore
  point at real files in `brainkit/examples/vault/`, which is why that directory
  exists at all — it is a link target and a behavioural fixture before it is
  documentation.
- The kit carries its own version, starting at `0.1.0`, and it is not the
  binary's version. A vault standard and a control plane change for different
  reasons and must be allowed to move at different speeds.
- Someone can now hold the second brain without holding the VM, which means the
  first thing a new reader evaluates is the part with no security surface. The
  security claims in `SECURITY.md` are unchanged and unextended: a plugin
  installed into a Claude Code running on a workstation has no VM edge under it,
  and the kit's README says so rather than implying the box's guarantees travel
  with the Markdown.

## Rejected alternatives

Collected above, at the decisions they belong to: an Obsidian-flavoured vault
format; a per-type required-field ontology; a separate repository for the kit;
a third-party skill index as the kit's home. One more, which belongs to none of
them: teaching `torio brain init` to write the richer scaffold *first*, and
extracting the kit from it afterwards. It inverts the dependency — the standard
would be whatever the Go templates happened to emit — and it would put a
behaviour change into the vault-creation path, which is the one path in this
product that runs against a directory the operator cannot get back.
