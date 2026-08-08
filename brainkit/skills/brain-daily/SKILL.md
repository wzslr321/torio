---
name: brain-daily
description: Open and maintain today's note in the Second Brain. Use when the user says "start my day", "what's on today", "log this to today", "add to today's note", or asks what they did on a given day.
---

# Daily notes

One note per calendar day, at `daily/YYYY-MM-DD.md`. It is a log, not an
archive: anything durable moves into a typed note and is linked from here.

Resolve the vault as `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7 describes. The
`daily` schema is §2.2 and the section conventions are §4.

## Opening the day

When the user starts their day, or asks for today's note and it does not exist:

1. Create `daily/YYYY-MM-DD.md` with the `daily` frontmatter — `title` and
   `timestamp` both the date — and the three sections `## Log`, `## Captured`,
   `## Done`.
2. **Carry over.** Read yesterday's note, or the most recent one if yesterday
   has none. Anything under `## Log` that was still open goes into today's
   `## Log` with a note of where it came from. Do not carry over what was done.
3. **Pull the current work.** Grep `projects/` for `^status: active` and read
   the `## Now` section of each. Put one line per project at the top of
   `## Log`, linking the project note.
4. **Show the inbox depth.** Count `inbox/` and say the number. Do not triage —
   that is a separate ritual with a separate skill.

Then show the user the note. Opening the day is a briefing, not a filing task;
finish by telling them what is waiting, in four or five lines, not by narrating
what you wrote.

## During the day

"Log this to today" appends one line under `## Log`, with a link if the thing
has a note. "That's done" moves the item to `## Done`, keeping its wording.

Append. Do not rewrite the day's earlier entries, do not reorder them, and do
not summarise them into something tidier — a log's value is that it says what
was actually thought at the time.

If today's note does not exist yet when the user logs something, create it with
frontmatter and the three sections first, then append. Do not run the full
opening ritual for a one-line log.

## Reading a day back

"What did I do on Tuesday" is a read of one file. Resolve the date, read the
note, answer from it. If it does not exist, say the day has no note rather than
reconstructing one from other notes.

## Rules

- **`timestamp` is the date, set once.** Never update it; a daily note is about
  its day even when it is edited later.
- **Nothing durable lives only here.** A decision worth keeping goes in the
  project or resource note, and the daily note links it. A daily note is a
  place things pass through.
- Everything in §6 of the standard applies — including that captures belong in
  `inbox/`, not in today's `## Log`, when the user says "remember this".

## Tools

- `Read` — today's note, yesterday's, the active projects.
- `Glob` — find the most recent daily note; count `inbox/`.
- `Grep` — find active projects by `^status: active`.
- `Write` — create today's note.
- `Edit` — append a line, move an item to `## Done`.
