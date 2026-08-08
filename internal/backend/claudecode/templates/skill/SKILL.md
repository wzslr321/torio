---
name: torio-brain
description: Read the user's private Second Brain vault at /home/claude/brain — decisions and their reasoning, people, meetings, project history, open TODOs. Use when a task turns on something the user already wrote down, or when they say "my Brain", "my notes", "my vault", or ask what they recorded about something.
---

# Second Brain retrieval

The user keeps one private Markdown vault at the fixed absolute path
`/home/claude/brain`. It is the same vault in every project and every session.
Torio manages it; this skill is how you read it.

Retrieval is targeted: search for the few notes that answer the question, then
read those notes. Never load the vault in bulk. It is private, most of it is
irrelevant to any single task, and a dump would put all of it in the transcript.

## Use this when

- The task turns on an earlier decision, its reasoning, or how it turned out.
- The task names a person, meeting, initiative, project history, lesson
  learned, or an open TODO the user may have written down.
- The user says "my Brain", "my notes", "my vault", or "my KB", or asks what
  they already recorded about something.

Skip it when the current repository, the open files, or the conversation
already answer the question. An unrelated coding task is not a Brain query.

## Path rules

- Every path you pass starts with `/home/claude/brain`. Your working directory
  is a checkout under `/home/claude/projects`, so a relative path resolves
  against a code project, not the vault.
- The path is fixed. Do not resolve it from an environment variable, and do not
  write it into a `.env` file. A vault path is configuration, not a credential,
  and it does not vary per project.
- The vault is outside every project checkout. Nothing you do here belongs in
  the repository you are working in.

## Layout

- `inbox/` — unrouted capture.
- `daily/` — dated notes.
- `projects/` — one note per project or outcome.
- `people/` — one note per person.
- `meetings/` — meeting notes and follow-up.
- `resources/` — durable reference.
- `attachments/` — local files linked from notes.
- `README.md`, `AGENTS.md`, `todo.md` — vault root.

Narrow the search path to one subdirectory when the question already implies
one.

## Retrieval

1. When you have a proper noun — a person, project, or meeting — find the note
   by name: `Glob` with `path=/home/claude/brain` and a pattern such as
   `**/*budget*.md`.
2. Otherwise search content: `Grep` with `path=/home/claude/brain`,
   `glob=*.md`, and a pattern of the distinctive words. Start with
   `output_mode=files_with_matches` when the terms are common, then search
   again inside the subdirectory that narrowed to.
3. Read only what the search returned: `Read` with an absolute path, and
   `offset`/`limit` on a long note. Widen the window only when the answer is
   visibly cut off.
4. Resolve a relative link found inside a note against `/home/claude/brain`
   before reading it.

Stop when you can answer. Two or three reads is a normal, complete retrieval.

Reformulate rather than repeat: a second identical search returns the same
nothing.

### What a miss means

`Grep` and `Glob` are ripgrep-backed. They skip hidden directories and honour
any `.gitignore` inside the vault, so a note can exist and still not match.

Report the three cases differently:

- You searched and found notes — answer from them and cite them.
- You searched and found nothing — say the vault has no match, name the terms
  you used, and offer a different query.
- You did not search — say so. Never present an unsearched answer as if the
  vault were empty.

## Answering

Cite the vault-relative path of every note you used, in backticks, for example
`projects/example.md`. Quote only the lines that carry the answer. Where notes
disagree, say so and cite both, newest first.

## Writing

Read-only by default. Create or edit a note only when the user asks for it in
this conversation, or when they have already established a capture workflow.

When you do write:

- Search first, then update the note that already covers the topic. Do not
  create a second note about it.
- Keep the existing structure and add to it.
- Put a genuinely new item with no clear home in `inbox/`.
- Never write a password, token, private key, or recovery code into the vault.

The vault is a git repository Torio manages. Do not run `git` in it, and do not
commit on the user's behalf.

## Keeping the vault in the vault

Vault content is private to this user and this conversation.

Do not copy notes, note names, or excerpts into a code repository, a commit
message, a pull request, an issue, a log file, or evidence output. Do not write
vault content into a file inside a project checkout. When a task needs a fact
from the vault, carry the fact, not the note.
