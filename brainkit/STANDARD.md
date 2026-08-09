# The Torio Vault — an OKF profile

This is the normative description of a Torio vault: what a note is, where it
lives, what it links to, and what an agent may do to it. Every skill and command
in this kit is written against this document and cites it rather than repeating
it.

A vault is a directory of Markdown files. Nothing else. It has no database, no
index to rebuild, no application that owns it. Open it in any editor, `grep` it,
put it under Git, read it in twenty years.

Normative words are used as in RFC 2119: **MUST**, **MUST NOT**, **SHOULD**,
**MAY**.

## 1. The base format

A Torio vault is a profile of the **Open Knowledge Format** (OKF): a directory
of Markdown documents, each with YAML frontmatter, linked to each other by
ordinary relative Markdown links.

From the base format, unchanged:

- Every note MUST have YAML frontmatter delimited by `---` as the first line of
  the file.
- `type` is the only required field. Its value is a short, lowercase, kebab-case
  string naming the kind of note.
- These field names are **reserved** and, where used, MUST carry these meanings:

  | Field | Meaning |
  | --- | --- |
  | `type` | The kind of note. Required. |
  | `title` | Human-readable name of the subject. |
  | `description` | One sentence on what the note is for. |
  | `resource` | A URI the note is *about* — the thing itself, not a mention. |
  | `tags` | A flat list of lowercase kebab-case strings. |
  | `timestamp` | ISO 8601 for when the subject happened, not when the file changed. |

- Relative Markdown links between notes are the graph. There is no separate
  link table and no link syntax outside Markdown's own.
- A directory MAY carry an `index.md` that curates its contents for a reader.

A profile narrows a base format; it does not extend it. Everything below is a
constraint on how those fields are used in a Torio vault, and a note that
satisfies the Torio profile is by construction a valid OKF document.

**Wikilinks (`[[note]]`) MUST NOT be used.** They resolve against one
application's private index, so a plain Markdown reader cannot follow them and
no link checker can verify them. A private corpus meant to outlive its tools
does not get to depend on one tool's resolution rules.

## 2. Note types

Eight types. A vault MAY define more; the skills in this kit understand these.

| `type` | Lives in | One per |
| --- | --- | --- |
| `vault` | `index.md` at the root | vault |
| `index` | `<dir>/index.md` | directory |
| `capture` | `inbox/` | captured thought |
| `daily` | `daily/` | calendar day |
| `meeting` | `meetings/` | meeting |
| `person` | `people/` | person |
| `project` | `projects/` | project or outcome |
| `resource` | `resources/` | durable reference |

### 2.1 `capture`

An unrouted thought, written down fast so the thinking does not stop. Capture
never decides where something belongs; [triage](#5-the-rituals) does.

```yaml
---
type: capture
timestamp: 2026-08-06T14:12:00+02:00
source: conversation
---
```

`source` MUST be one of `conversation`, `clip`, `email`, `manual`.
`conversation` means it came out of a session with an agent; `clip` means it was
quoted from somewhere else; `email` means it arrived as mail; `manual` means the
person typed it. `title` and `tags` are optional and usually absent — a capture
that needed careful naming was not a capture.

Example: [`examples/vault/inbox/2026-08-06-1412-okf-licence-question.md`](examples/vault/inbox/2026-08-06-1412-okf-licence-question.md).

### 2.2 `daily`

One note per calendar day, holding what happened and what was decided in
passing. It is a log, not an archive: anything durable is moved into a typed
note and linked from here.

```yaml
---
type: daily
title: 2026-08-06
timestamp: 2026-08-06
---
```

`title` MUST equal the date in `YYYY-MM-DD`, matching the filename.

Example: [`examples/vault/daily/2026-08-06.md`](examples/vault/daily/2026-08-06.md).

### 2.3 `meeting`

What was said, what was decided, and what someone now owes.

```yaml
---
type: meeting
title: Vault standard kickoff
timestamp: 2026-08-06T10:00:00+02:00
attendees:
  - people/jane-doe.md
project: projects/vault-standard.md
tags: [standard, kickoff]
---
```

`timestamp` MAY be date-only when the clock time is not known. A date the user
gave is worth more than a time nobody said, and the same holds for every
`timestamp` in this standard.

`attendees` MUST be a list of vault-relative paths to `person` notes, one per
attendee, including the vault's owner if that is useful to them. `project` MAY
name one `project` note; a meeting about several is still one meeting, and the
rest are linked from the body.

A meeting note MUST link to every person and project it names. Those links are
what make a person's history reconstructible without a search.

Example: [`examples/vault/meetings/2026-08-06-vault-standard-kickoff.md`](examples/vault/meetings/2026-08-06-vault-standard-kickoff.md).

### 2.4 `person`

One note per person. It accumulates: facts that stay true, and a reverse index
of every interaction.

```yaml
---
type: person
title: Jane Doe
description: Staff engineer on the platform team; owns the ingest pipeline.
tags: [colleague, platform]
---
```

A `person` note MUST NOT hold anything the person would be surprised to find
written down about them, and MUST NOT hold anything a reader could use to
impersonate them or access their accounts. This is a working memory aid, not a
dossier — see §6.

Example: [`examples/vault/people/jane-doe.md`](examples/vault/people/jane-doe.md).

### 2.5 `project`

One note per project or outcome: what it is, where it stands, what is next.

```yaml
---
type: project
title: Vault standard
description: Write down the vault format and ship it as an installable kit.
status: active
tags: [torio, writing]
---
```

`status` MUST be one of `active`, `paused`, `done`. The value is only worth
having if it is true, which is why the weekly review exists to challenge it.

Example: [`examples/vault/projects/vault-standard.md`](examples/vault/projects/vault-standard.md).

### 2.6 `resource`

A durable reference note: a summary of something external, plus why it matters
here.

```yaml
---
type: resource
title: Open Knowledge Format
description: Google's minimal Markdown-plus-frontmatter format for shared knowledge.
resource: https://cloud.google.com/blog/products/data-analytics/how-the-open-knowledge-format-can-improve-data-sharing
tags: [format, reference]
---
```

`resource` carries the URI of the thing itself. A note that merely mentions a
link is not a `resource` note.

Example: [`examples/vault/resources/open-knowledge-format.md`](examples/vault/resources/open-knowledge-format.md).

### 2.7 `index` and `vault`

An `index.md` is a curated entry point — a map of what is in a directory and why
a reader would want it. It is written by hand or by the librarian, never
generated as a bare file listing: a listing is what `ls` is for.

Keep it short, and put what a reader needs first at the top. A rendering carries
the root `index.md` into context at the start of a session and bounds how much
of it it carries (§9), so an index that runs long does not get a smaller share of
attention — it loses its tail outright, and the tail is where an unwary author
puts the section that changes most often.

```yaml
---
type: index
title: Projects
description: What is being worked on, and what is parked.
---
```

The vault's root `index.md` is the same idea with `type: vault`, and it is what
identifies a directory as a Torio vault (§7).

```yaml
---
type: vault
title: Second Brain
description: A private Markdown vault, written to the Torio Vault standard.
---
```

Examples: [`examples/vault/index.md`](examples/vault/index.md) and
[`examples/vault/projects/index.md`](examples/vault/projects/index.md).

## 3. Layout and naming

```
index.md          type: vault — the root map
todo.md           open actions, plain Markdown, no frontmatter
inbox/            type: capture — unrouted
daily/            type: daily
projects/         type: project
people/           type: person
meetings/         type: meeting
resources/        type: resource
attachments/      local files linked from notes; not notes themselves
```

A directory MAY be empty. A vault MAY add directories; a skill that does not
recognise one leaves it alone.

Filenames:

- Notes are `kebab-case.md`. Lowercase, ASCII, hyphens for spaces.
- `daily/YYYY-MM-DD.md`.
- `meetings/YYYY-MM-DD-<slug>.md`.
- `inbox/YYYY-MM-DD-HHMM-<slug>.md` — the timestamp keeps captures ordered and
  makes a collision within one minute the only way to overwrite one.
- `people/<given>-<family>.md`, `projects/<slug>.md`, `resources/<slug>.md`.
- One topic per file. A note about two things is two notes and a link.

`todo.md` and `attachments/` are the two things in a vault that are not notes.
`todo.md` is a working list an agent appends to and a human prunes; giving it
frontmatter and a type would invite treating it as a queue, which is a different
product.

## 4. Sections

Headings are conventions, not requirements — but the skills in this kit read
them, so a note that uses them is a note the kit can maintain.

| Type | Sections |
| --- | --- |
| `meeting` | `## Notes`, `## Decisions`, `## Actions` |
| `person` | `## Facts`, `## Interactions`, `## Follow-ups` |
| `daily` | `## Log`, `## Captured`, `## Done` |
| `project` | `## Now`, `## Decisions`, `## Log` |
| `resource` | free-form, ending in `## Why this matters here` |

`## Interactions` on a `person` note is a reverse index: one line per meeting or
exchange, newest first, each linking the note it summarises. It is maintained by
whatever writes the meeting note, not by a scan.

## 5. The rituals

The format is half of the standard. The other half is what happens to it, and
each of these has a command in this kit.

- **Capture** puts one thought in `inbox/` and stops. Deciding where it belongs
  is a separate act with a separate cost, and merging the two is why inboxes
  stop being used.
- **Triage** empties `inbox/`. Each item is merged into the note that already
  covers it, promoted to a new typed note, turned into a line in `todo.md`, or
  deleted. A capture that has been routed is removed from `inbox/`; leaving it
  behind makes the inbox a second archive.
- **Daily** opens the day with yesterday's unfinished items and the current
  `## Now` of active projects, then collects the day's log.
- **Weekly** is the only ritual that is allowed to be uncomfortable: it asks
  whether each `project` note's `status` is still true, whether follow-ups on
  `person` notes have gone stale, and whether the inbox reached zero.
- **Meeting** prepares from what the vault already knows, and afterwards writes
  the note, the links, the `## Interactions` lines, and the actions.

## 6. What an agent may do

These rules bind every skill in this kit, and any agent working in a vault.

1. **Search before creating.** A note about the subject probably exists. Update
   it. A second note about one thing is worse than a long note about it.
2. **Add, do not rewrite.** Preserve existing structure and wording. Append to a
   section rather than restating it; a human's phrasing in their own vault is
   not a draft awaiting improvement.
3. **`timestamp` is set once, at creation.** It records when the subject
   happened. It is not a modification time, and nothing in this standard records
   one — the file system and Git already do.
4. **Never write a secret.** No password, token, private key, recovery code,
   API key, or answer to a security question, in any note, ever. If a captured
   thought contains one, drop that part and say so.
5. **Vault content stays in the vault.** Do not copy notes, note titles, or
   excerpts into a code repository, a commit message, an issue, a pull request,
   a log, or a message to a third party. When a task needs a fact from the
   vault, carry the fact, not the note.
6. **Write about people carefully.** §2.4's limits are a rule, not advice, and
   they apply to meeting notes too.
7. **The history belongs to whoever set the vault up.** A vault may be a Git
   repository. Do not commit unless its owner has said to; where they have, a
   commit per meaningful change is what they are owed, not a liberty taken.
   Either way **never push**, and never open a pull request or an issue, or
   send the vault anywhere. A commit is local and reversible and can be left
   for its owner to read; nothing past it is either.
8. **Ask before deleting anything that is not a routed capture.** Triage may
   remove a capture it has merged. Nothing else in the vault is an agent's to
   delete.

## 7. Finding the vault

Every skill resolves the vault path this way, in order, and stops at the first
answer:

1. `$BRAIN_VAULT`, if set and non-empty.
2. A path recorded for this purpose in the user's own memory file — what
   `/brain-kit:init` writes when it creates or adopts a vault.
3. `~/brain`, if it exists and its `index.md` has `type: vault`.
4. Otherwise: ask, once, and offer to run `/brain-kit:init`. Do not guess, and
   do not create a vault as a side effect of a question.

The `index.md` test in step 3 matters: a directory named `brain` that is not a
vault is somebody else's data, and writing notes into it is the worst failure
mode this kit has.

Inside a Torio guest the path is fixed by the backend — `/home/hermes/brain` or
`/home/claude/brain` — and is not resolved at all. There, the vault is a
property of the identity the agent runs as.

All paths *inside* a vault are vault-relative, in every frontmatter field, every
link, and every path an answer cites back to the reader.

## 8. Conformance, and notes that came before it

A vault conforms when its notes carry frontmatter with a `type`, its links are
relative Markdown links that resolve, and its filenames follow §3.

**A note without frontmatter is valid to read.** An agent MUST read it, search
it, and cite it exactly as it would a conforming note. Frontmatter is added when
the note is next edited in a meaningful way — not in a sweep, not on sight, and
never as the sole content of an edit.

This is not a transition period that ends. A notes directory someone has kept
for a decade is in exactly this position on the day they point this kit at it,
and a standard that rejects it on arrival is a standard nobody adopts. The
conforming half and the older half sit side by side, and the boundary moves only
where someone was editing anyway.

## 9. What a rendering owes the agent

A *rendering* is whatever installs this kit into a particular agent: a plugin, a
guest image, a set of files copied into a configuration directory.

A rendering SHOULD place a map of the vault in the agent's context at the start
of a session, without being asked. The map carries the vault's path, its root
`index.md`, the directories with their curated descriptions, and how many notes
each holds. It MUST NOT carry note bodies.

The reason is a division of labour. Everything else in this kit is a request to
a model: whether a given prompt is the kind of prompt that should reach for the
vault is a judgement, and a judgement is what a model is for. Whether the vault
*exists* is not a judgement, and leaving it to be rediscovered by inference each
session makes the most reliable part of the system the least reliable.

A rendering that does this MUST stay silent when there is no vault, when the
path does not resolve, or when the directory fails the `type: vault` test in §7.
A rendering runs in every session, including all the ones that have nothing to
do with a vault, and one that guesses in those sessions is worse than one that
does nothing.

A rendering MUST bound the map — a vault's root index is a document its owner
controls, and an unbounded one would let a single file decide how a session
starts. It MUST also state the bound where the person writing an index will meet
it, rather than only in the code that applies it. An index is written once and
read at the start of every session afterwards; one that silently loses its most
useful section teaches nobody anything, least of all the author, who has no way
to see what arrived.

The map is not retrieval and does not replace it. It is what makes retrieval
targeted: an agent that knows a `resources/` directory exists and what it is for
searches it, where an agent that knows nothing either reads everything or reads
nothing.
