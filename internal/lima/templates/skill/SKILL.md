---
name: torio-brain
description: "Search your Second Brain notes: decisions, people, TODOs"
version: 1.0.0
author: Torio
license: MIT
platforms: [linux]
metadata:
  hermes:
    tags: [second-brain, notes, markdown, retrieval, torio]
    category: productivity
---

# Second Brain retrieval

The user keeps one private Markdown vault at the fixed absolute path
`/home/hermes/brain`. It is the same vault in every project and every session.
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

- Every path you pass starts with `/home/hermes/brain`. Relative paths resolve
  against the session's terminal working directory, which is normally a code
  project, not the vault.
- The path is fixed. Do not resolve it from an environment variable, and do not
  write it into a `.env` file. A vault path is configuration, not a credential,
  and it does not vary per project.

## Layout

- `inbox/` — unrouted capture.
- `daily/` — dated notes.
- `projects/` — one note per project or outcome.
- `people/` — one note per person.
- `meetings/` — meeting notes and follow-up.
- `resources/` — durable reference.
- `attachments/` — local files linked from notes.
- `README.md`, `AGENTS.md`, `todo.md` — vault root.

Narrow `path` to one subdirectory when the question already implies one.

## Retrieval

1. When you have a proper noun — a person, project, or meeting — find the note
   by name: `search_files` with `target=files`, `path=/home/hermes/brain`, and
   `pattern` a glob such as `*budget*.md`.
2. Otherwise search content: `search_files` with `target=content`,
   `path=/home/hermes/brain`, `file_glob=*.md`, and `pattern` a regex of the
   distinctive words. Use `output_mode=files_only` first when the terms are
   common, then search again inside the narrowed subdirectory.
3. Read only what the search returned: `read_file` with an absolute `path`, and
   `offset`/`limit` on a long note. Widen the window only when the answer is
   visibly cut off.
4. Resolve a relative link found inside a note against `/home/hermes/brain`
   before reading it.

Stop when you can answer. Two or three reads is a normal, complete retrieval.

Reformulate instead of repeating: the runtime blocks a fourth identical search
and a third identical read.

### What a miss means

`search_files` is ripgrep-backed. It skips hidden directories and honours any
`.gitignore` inside the vault, so a note can exist and still not match.

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

## Keeping the vault in the vault

Vault content is private to this user and this conversation.

Do not copy notes, note names, or excerpts into a code repository, a commit
message, a pull request, an issue, a log file, or evidence output. Do not write
vault content into a file inside a project checkout. When a task needs a fact
from the vault, carry the fact, not the note.
