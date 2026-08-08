---
name: brain-search
description: Read the user's private Second Brain vault — decisions and their reasoning, people, meetings, project history, open actions. Use when a task turns on something the user already wrote down, or when they say "my brain", "my notes", "my vault", or ask what they recorded about something.
---

# Second Brain retrieval

The user keeps one private Markdown vault. It is the same vault in every project
and every session. This skill is how you read it.

Retrieval is targeted: find the few notes that answer the question, then read
those notes. Never load the vault in bulk. It is private, most of it is
irrelevant to any single task, and a dump puts all of it in the transcript.

## Use this when

- The task turns on an earlier decision, its reasoning, or how it turned out.
- The task names a person, meeting, project, lesson learned, or an open action
  the user may have written down.
- The user says "my brain", "my notes", "my vault", or asks what they already
  recorded about something.

Skip it when the current repository, the open files, or the conversation already
answer the question. An unrelated coding task is not a vault query.

## The vault

Resolve the vault path as `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7 describes, and
read it once per session — not once per search. Every path you pass is absolute,
under that root; your working directory is a code project, so a relative path
resolves against the wrong tree.

The layout and the eight note types are in that same document, §2 and §3. Read
it if you have not this session; the type names and their frontmatter are what
make a search precise instead of a scan.

## Retrieval

Work down this list and stop as soon as you can answer.

1. **Index first, when the question implies a directory.** `projects/index.md`,
   `people/index.md` and the root `index.md` are curated maps. One read of the
   right index often names the note you want and costs less than any search.
2. **Proper noun → find the file.** A person, project, meeting or resource has a
   note named after it. Glob the vault for `**/*<slug>*.md`.
3. **Otherwise search content.** Grep the vault for the distinctive words, over
   `*.md`. Start by listing matching files when the terms are common, then
   search again inside whichever subdirectory that narrowed to.
4. **Narrow by frontmatter when the kind of note is known but the subject is
   not.** `^type: meeting` finds every meeting; `^status: active` finds live
   projects; `^tags:.*platform` finds a tag. This is what the standard's
   frontmatter is *for*, and it is far cheaper than reading candidates to
   classify them.
5. **Read only what the search returned.** Use offset and limit on a long note,
   and widen only when the answer is visibly cut off.
6. **Walk one hop, not the whole graph.** A note's links are curated, so the
   note a meeting names is usually worth reading. Follow links from notes you
   have already read; do not follow links from *those*. Resolve each relative
   link against the vault root before reading it.

Two or three reads is a normal, complete retrieval.

Reformulate rather than repeat: a second identical search returns the same
nothing. If a name did not match, try the surname alone, an acronym expanded, or
the project's old name.

### What a miss means

Search tools skip hidden directories and honour any `.gitignore` inside the
vault, so a note can exist and still not match.

Report the three cases differently:

- You searched and found notes — answer from them and cite them.
- You searched and found nothing — say the vault has no match, name the terms
  you used, and offer a different query.
- You did not search — say so. Never present an unsearched answer as if the
  vault were empty.

## Answering

Cite the vault-relative path of every note you used, in backticks, for example
`projects/vault-standard.md`. Quote only the lines that carry the answer.

Where notes disagree, say so and cite both, newest first — and prefer a
`timestamp` in frontmatter over a date in prose, because the frontmatter says
when the subject happened and the prose may be recounting it.

A note without frontmatter is a normal note. Read it, cite it, and do not
mention its shape unless the user asked about the vault itself.

## Writing

Read-only by default. The other skills in this kit write; this one does not,
except when the user asks for an edit in this conversation.

When you do write, §6 of the standard binds you — search before creating, add
rather than rewrite, never write a secret, never commit.

## Keeping the vault in the vault

Vault content is private to this user and this conversation.

Do not copy notes, note titles, or excerpts into a code repository, a commit
message, a pull request, an issue, a log file, or a message to any third party.
Do not write vault content into a file inside a project checkout. When a task
needs a fact from the vault, carry the fact, not the note.

## Tools

- `Read` — read a note, by absolute path.
- `Glob` — find a note by name: pattern `**/*budget*.md`, rooted at the vault.
- `Grep` — search note contents: rooted at the vault, `glob` of `*.md`, output
  mode `files_with_matches` first when the terms are common.
