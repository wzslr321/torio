---
name: brain-people
description: Read and maintain person notes in the Second Brain. Use when the user asks "who is X", "what do I know about X", "when did I last talk to X", "remind me what I owe X", or tells you something about a person worth keeping.
---

# People

One note per person, at `people/<given>-<family>.md`. It holds what stays true
about them, a reverse index of every interaction, and what is outstanding
between you.

Resolve the vault as `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7 describes. The
`person` schema is §2.4 and the sections are §4.

## Answering about a person

1. Read their note.
2. Read the meetings it links under `## Interactions` — the last two or three,
   newest first, not all of them.
3. Answer from both, citing paths. Say what is open before what is historical:
   "you owe them the type table, due Friday" is the useful half of "who is
   Jane".

If no note exists, say so plainly and offer to create one. Do not assemble an
answer by grepping the vault for a name and calling that a profile — a person's
note is curated, and a search result is not.

"When did I last talk to X" is the first `## Interactions` line. One read.

## Writing about a person

Creating: full frontmatter — `title` as their display name, `description` as one
sentence on who they are in your working life, `tags` for how you would group
them. Sections `## Facts`, `## Interactions`, `## Follow-ups`.

Updating:

- A durable fact goes under `## Facts` as its own line. Something true today and
  false next quarter is not a fact; it is context for a meeting note.
- An interaction goes under `## Interactions`, newest first, linking its note.
  `brain-meeting` writes these after a meeting; write one here when the exchange
  had no meeting note — a corridor conversation, a long thread.
- Something you owe, or they owe, goes under `## Follow-ups` with the date it
  was promised and the date it is due. Mark it done rather than deleting it;
  what you promised and when is worth more than a tidy list.

## What does not go in a person note

§2.4 is a rule and this is where it is tested.

- Nothing they would be surprised to find written down about them.
- No credentials, security answers, or anything usable to impersonate them or
  reach their accounts.
- No home address, medical detail, or family circumstance unless *they* asked
  you to remember it and it serves them — a birthday they mentioned so you would
  not forget it is different in kind from a fact about their health you
  inferred.
- No appraisal of them as a person. "Prefers written proposals" is how to work
  with someone. A verdict on their competence is a thing you will regret having
  written, in a file that outlives the mood.

If the user dictates something that fails these, write the rest and say what you
left out. Do not argue; say it once and move on.

## Merging duplicates

Two notes for one person happen — a first name and a full name. When you find
them, tell the user and propose the merge; do not perform it silently. If they
agree: keep the fuller filename, append the other's sections into it, and update
every note whose `attendees` or links pointed at the file being removed. A merge
that leaves dangling paths is worse than the duplicate.

## Tools

- `Glob` — find a person's note by name fragment.
- `Grep` — find every note naming them; find their note by `^title:`.
- `Read` — the note and the meetings it links.
- `Write` — create a person note.
- `Edit` — add a fact, an interaction, a follow-up.
