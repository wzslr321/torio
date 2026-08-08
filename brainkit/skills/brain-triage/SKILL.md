---
name: brain-triage
description: Empty the Second Brain inbox by merging or promoting each captured item. Use when the user says "process my inbox", "triage my brain", "clear the inbox", or asks what is sitting in their inbox.
---

# Inbox triage

Every item in `inbox/` gets one of four outcomes, and then the inbox is empty.
An item that stays is a decision deferred, not a decision made, so name it that
way rather than leaving it silently.

Resolve the vault as `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7 describes. The note
types and their frontmatter are §2; the rules that bind you while writing are
§6.

## The four outcomes

1. **Merge** into a note that already covers the subject. This is the common
   case and the one to reach for. Append to the right section; do not restate
   what is there.
2. **Promote** to a new typed note — `project`, `person`, `resource` — when
   nothing covers it yet. Full frontmatter, correct filename, and the links §2
   says that type owes.
3. **Action** — a line in `todo.md`, linking the note it belongs to. Use this
   when the item is something to *do* rather than something to know.
4. **Drop** — when the item turned out to be nothing, or has already happened.
   Say so; do not delete it quietly.

A routed capture is deleted from `inbox/`. That is the one deletion this kit
performs without asking, and it is safe only because the content survives at the
destination — so delete *after* the merge or promotion is written, never before,
and never at all if the content did not land somewhere.

Before deleting, grep the vault for the capture's filename. A daily note or an
index often links it, and those links must be repointed at the destination in
the same pass. Repoint the link and leave the surrounding sentence alone; if the
sentence stops making sense once repointed, add a short clause rather than
rewriting what the user wrote. A capture removed under a link that still points
at it turns triage into a broken-link generator, which is exactly the failure
the librarian then has to find.

## How to work

1. List `inbox/` and read every item. They are short; this is not the expensive
   part.
2. For each item, **search before deciding**. A merge target you did not look
   for becomes a duplicate note, which is the failure this whole ritual exists
   to prevent. Search by the item's proper nouns first, then by its distinctive
   words.
3. Decide the outcome, then write it.
4. Delete the routed capture.
5. Report a table: item, outcome, destination.

Work oldest first. An item captured three weeks ago is the one whose context is
about to be lost.

## When to hand off

If the inbox has more than ten items, delegate to the `brain-librarian`
subagent: pass it the vault path and the item list, and let it do the searching
and writing. Reading thirty captures and their merge targets in this
conversation costs the user the context they are actually working in.

## Judgement calls

- **Ambiguous destination.** Ask, once, in a batch at the end — not per item.
  Present the item and the two candidates; do not guess between two existing
  notes, because a merge into the wrong one is invisible afterwards.
- **An item that is really three things.** Split it. Each part gets its own
  outcome.
- **An item that contradicts an existing note.** Do not resolve it. Append it to
  the existing note under the existing text, marked with its date, and tell the
  user the two disagree.
- **A secret in a capture.** Route the rest, drop that part, and say so. Never
  copy it to the destination.

## Tools

- `Glob` — list `inbox/`, find merge targets by name.
- `Grep` — find merge targets by content and by frontmatter (`^type:`, `^tags:`).
- `Read` — read each capture and each candidate target.
- `Edit` — merge into an existing note; append to `todo.md`.
- `Write` — create a promoted note.
- `Bash` — remove a routed capture file, one `rm` per file, by absolute path.
- `Task` — hand a large inbox to the `brain-librarian` subagent.
