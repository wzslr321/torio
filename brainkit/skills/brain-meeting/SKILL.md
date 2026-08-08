---
name: brain-meeting
description: Prepare for a meeting from the Second Brain, or write it up afterwards. Use when the user says "I have a call with X", "prep me for", "brief me before", "write up that meeting", or hands over meeting notes to file.
---

# Meetings

Two jobs with the same subject and opposite directions. Prep reads and writes
nothing. Debrief writes a note and everything that note owes.

Resolve the vault as `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7 describes. The
`meeting` schema is §2.3, `person` is §2.4, and the sections are §4.

## Prep — read only

Given who and what:

1. Read each attendee's `person` note: `## Facts`, the last few
   `## Interactions`, and every open `## Follow-ups` item.
2. Read the `project` note if the meeting has one — `## Now` and the most recent
   `## Decisions`.
3. Read the last one or two meetings with these people. Their `## Actions` are
   the highest-value lines in the vault right now: an action you owe and have
   not done is what you want to know before walking in, not after.

Brief in this order: **what you owe** (open follow-ups and unfinished actions),
**where things stand**, **who these people are**, **what is unresolved**. Cite
the note behind each block.

Write nothing during prep. Prep that files things is prep the user stops
trusting to be cheap.

## Debrief — write the note and its links

Given what happened, in whatever form the user hands it over:

1. **Write the meeting note** at `meetings/YYYY-MM-DD-<slug>.md`, with the §2.3
   frontmatter: `title`, `timestamp` of when the meeting *happened*, `attendees`
   as vault-relative paths to `person` notes, `project` if there is one, `tags`
   if they help. Sections `## Notes`, `## Decisions`, `## Actions`.
2. **Create any missing person note** before referencing it. A path in
   `attendees` that points at nothing is a broken graph; the standard says
   attendees are paths, so they must resolve.
3. **Update every attendee.** One line under `## Interactions`, newest first,
   linking this meeting and saying in a clause what came of it. This reverse
   index is written here, by whatever writes the meeting — nothing scans for it
   later.
4. **Update the project.** A decision from the meeting goes under the project's
   `## Decisions` with a link back here. Do not leave a decision recorded only
   in the meeting note; nobody reads meeting notes to find out where a project
   stands.
5. **Land the actions.** Every item under `## Actions` also becomes a line in
   `todo.md`, linking the meeting. An action that lives only in the meeting note
   has not been captured, it has been mentioned.

Then report: the note's path, which people were updated, which project, and how
many actions landed.

## Judgement calls

- **Attendees you cannot identify.** Ask for the surname rather than creating a
  note under a first name; `people/jane.md` and `people/jane-doe.md` becoming
  two people is a merge nobody ever gets around to.
- **A meeting spanning several projects.** One `project` in frontmatter, the
  rest linked from the body, and the decisions written to each project's note.
- **Notes about people.** §2.4 binds this note too. Record what was said and
  decided, not impressions of the people who said it.
- **Recordings and transcripts.** Summarise into the sections. Do not paste a
  transcript into the vault; a note nobody will reread is a note that only
  costs.

## Tools

- `Read` — person, project and previous meeting notes.
- `Glob` — find a person's note by name; find previous meetings by date-slug.
- `Grep` — find meetings by attendee path or `^type: meeting`.
- `Write` — the meeting note; a missing person note.
- `Edit` — `## Interactions`, the project's `## Decisions`, `todo.md`.
