---
description: The weekly review — what happened, what is stale, what is not true any more
---

Run the weekly review of the Second Brain. This is the ritual that is allowed to
be uncomfortable; the rest of the kit maintains the vault, and this one
challenges it.

Resolve the vault (`${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7). Cover the last seven
days unless `$ARGUMENTS` says otherwise.

## Read

1. Every `daily/` note in the window. `## Done` is what actually happened;
   `## Log` is what was thought about.
2. Every `meetings/` note in the window, for decisions and unfinished
   `## Actions`.
3. Every `projects/` note with `status: active` — is `## Now` still what is
   next, and did anything move?
4. Every `person` note's `## Follow-ups`, for items past their due date.
5. `inbox/` — how deep, and how old is the oldest item.

## Report

- **Done.** What actually shipped, from the daily notes' `## Done`.
- **Decided.** Decisions made this week, with the note behind each.
- **Slipping.** Actions from meetings that never reached `todo.md`, and
  `todo.md` items now overdue.
- **Stale follow-ups.** What you owe people, and for how long.
- **Status that is no longer true.** Any `active` project with no activity in
  the window: name it and ask whether it is `paused` or `done`. This is the
  question of the whole review, and it is worth asking bluntly — a vault full of
  projects that are all "active" is a vault whose `status` field means nothing.
- **Inbox.** Depth, and the age of the oldest item.

## Then

Offer, in this order, and do each only if the user says yes:

1. `/brain-kit:triage`, if the inbox is not empty.
2. The `status` changes you proposed.
3. The `brain-librarian` subagent, for indexes and broken links.

Write nothing during the review itself. A review that files things as it goes is
a review the user stops running honestly.
