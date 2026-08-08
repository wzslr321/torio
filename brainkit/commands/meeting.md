---
description: Prepare for a meeting, or write one up afterwards
argument-hint: "prep <who|what> | debrief <who|what>"
---

Prepare for or write up a meeting, using the `brain-meeting` skill.

`$ARGUMENTS` says which. If it starts with `prep`, prepare. If it starts with
`debrief` or `writeup`, write up. If it says neither, decide from the tense the
user used, and say which you chose before doing it.

## Prep

Read only — the attendees' `person` notes, the project's `## Now` and recent
`## Decisions`, and the last one or two meetings with these people.

Brief in this order: **what you owe them**, **where things stand**, **who they
are**, **what is unresolved**. Cite the note behind each block. Write nothing.

## Debrief

Take the user's account of the meeting and land all of it
(`${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §2.3):

1. The meeting note at `meetings/YYYY-MM-DD-<slug>.md`, with `attendees` as
   paths that resolve, and `## Notes` / `## Decisions` / `## Actions`.
2. A person note for any attendee who has none.
3. One `## Interactions` line on every attendee, linking this meeting.
4. Each decision copied to the project's `## Decisions`, linking back here.
5. Each action as a line in `todo.md`, linking the meeting.

Report the path, who was updated, which project, and how many actions landed.
Steps 3 to 5 are the point — a meeting note nobody reads again, with actions
that live only inside it, is a transcript with extra steps.
