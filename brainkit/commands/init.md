---
description: Create a Second Brain vault, or bring an existing directory up to the standard
argument-hint: "[path]"
---

Create or adopt a Second Brain vault at `$1`, defaulting to `~/brain` when no
path is given.

Read `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` first. It is what you are creating.

## Decide which case you are in

Look at the target directory before writing anything.

- **Missing or empty** — create the vault.
- **Has an `index.md` with `type: vault`** — already a vault. Fill gaps only.
- **Has Markdown in it but no `type: vault`** — somebody's existing notes.
  Adopt: report what is there, and ask before touching it.
- **Has anything else in it** — stop. Report what you found and ask. A directory
  that is not notes is somebody else's data, and writing a vault into it is the
  worst thing this command can do.

## Create

Write, under the vault root:

- `index.md` with `type: vault` frontmatter and a short map of the directories,
  modelled on `${CLAUDE_PLUGIN_ROOT}/examples/vault/index.md`.
- The directories from §3: `inbox/`, `daily/`, `projects/`, `people/`,
  `meetings/`, `resources/`, `attachments/`.
- `projects/index.md` and `people/index.md`, each `type: index`, each honest
  about being empty.
- `todo.md` with `## Now`, `## Waiting`, `## Someday` and nothing under them.

Do not copy the sample notes in. An example vault with Jane Doe in it is not a
starting point, it is somebody else's furniture.

## Adopt

For a directory of existing notes:

1. Report what is there — how many files, what the top-level directories are,
   whether frontmatter already exists anywhere.
2. Ask before writing. Say exactly what you would add: the missing directories,
   `index.md`, `todo.md`.
3. Add only those. **Do not touch a single existing note.** §8 of the standard
   is explicit: a note without frontmatter is valid to read, and frontmatter
   arrives when the note is next edited for a real reason. A sweep is not a
   reason.
4. If their directories are named differently — `notes/` where the standard says
   `resources/` — say so and leave them. Renaming someone's directories to match
   a document they have just met is not adoption.

## Fill gaps

On an existing vault, create only what is missing and say what you created. If
nothing was missing, say that and write nothing.

## Record where it is

Once the vault exists, offer to record its path so every session finds it
without asking — a line naming the vault path, appended to the user's own memory
file (`~/.claude/CLAUDE.md`).

Ask first, and say what you are appending. If they decline, tell them
`BRAIN_VAULT` in their environment does the same job, and that `~/brain` is
found automatically.

## Finish

Report: which case you were in, what you created, whether the path was recorded,
and the two things to do next — `/brain-kit:daily` to open the day,
`/brain-kit:triage` once the inbox has something in it.
