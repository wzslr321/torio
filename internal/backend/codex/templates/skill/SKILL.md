---
name: torio-brain
description: Read the user's private Second Brain vault at /home/codex/brain, which holds decisions and their reasoning, people, meetings, project history, and open TODOs. Use when a task turns on something the user already wrote down, or when they say "my Brain", "my notes", "my vault", or ask what they recorded about something.
---

# Second Brain retrieval

The user keeps one private Markdown vault at the fixed absolute path
`/home/codex/brain`. It is the same vault in every project and every session on this
machine, and this machine's copy of one vault the operator keeps: what they
reconcile with `torio brain sync` is written here too.
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

- Every path you pass starts with `/home/codex/brain`. Your working directory
  is a checkout under `/home/codex/projects`, so a relative path resolves
  against a code project, not the vault.
- The path is fixed. Do not resolve it from an environment variable, and do not
  write it into a `.env` file. A vault path is configuration, not a credential,
  and it does not vary per project.
- The vault is outside every project checkout. Nothing you do here belongs in
  the repository you are working in.

## Layout

- `inbox/`, unrouted capture.
- `daily/`, dated notes.
- `projects/`, one note per project or outcome.
- `people/`, one note per person.
- `meetings/`, meeting notes and follow-up.
- `resources/`, durable reference.
- `attachments/`, local files linked from notes.
- `README.md`, `AGENTS.md`, `todo.md` at the vault root.

Narrow the search path to one subdirectory when the question already implies
one.

## Retrieval

Search with the tools this guest has. It ships coreutils and findutils and
nothing else, so use `find` and `grep` rather than a faster searcher you may be
used to elsewhere.

1. When you have a proper noun, find the note by name:
   `find /home/codex/brain -iname '*budget*.md'`.
2. Otherwise search content for the distinctive words:
   `grep -ril --include='*.md' 'quarterly budget' /home/codex/brain`. Start with
   `-l` to get filenames, then search again inside the subdirectory that
   narrowed to.
3. Read only what the search returned: `sed -n '1,120p' <absolute path>`, and a
   further range on a long note. Widen the window only when the answer is
   visibly cut off.
4. Resolve a relative link found inside a note against `/home/codex/brain`
   before reading it.

Stop when you can answer. Two or three reads is a normal, complete retrieval.

Reformulate rather than repeat: a second identical search returns the same
nothing.

### What a miss means

`grep -r` reads hidden directories and ignores `.gitignore`, so a miss here is
a real miss rather than a file that was skipped. Case is the usual reason a
search finds nothing, which is why `-i` is in the command above.

Report the three cases differently:

- You searched and found notes: answer from them and cite them.
- You searched and found nothing: say the vault has no match, name the terms
  you used, and offer a different query.
- You did not search: say so. Never present an unsearched answer as if the
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
