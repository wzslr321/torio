---
description: Open today's note — carry over what is unfinished and brief the day
---

Open the day in the Second Brain.

Use the `brain-daily` skill, which owns the note's shape and the carry-over
rules. This command is the ritual around it.

1. Resolve the vault (`${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7).
2. Create or open `daily/YYYY-MM-DD.md` for today.
3. Carry over what was still open in the most recent daily note.
4. Pull the `## Now` line of every project with `status: active`.
5. Count `inbox/`.

Then brief the user in five lines or fewer:

- what carried over,
- what the active projects say is next,
- anything in `todo.md` under `## Now` that is due today or overdue,
- how deep the inbox is.

Do not triage the inbox, do not write to any note but today's, and do not
summarise the briefing back to yourself before giving it. If `$ARGUMENTS` names
a date, open that day instead of today and skip the carry-over — an old day is
being read, not started.
