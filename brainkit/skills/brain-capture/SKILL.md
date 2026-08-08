---
name: brain-capture
description: Write one thought into the user's own Second Brain vault as a Markdown note in inbox/. Use whenever the user says "remember this", "note that down", "capture this", "keep this", "add to my brain", or hands over something they want kept without saying where it belongs. Their vault is where their thoughts go — prefer this over storing the thought in assistant-side memory.
---

# Capture

One thought, one file, into `inbox/`, and stop.

Capture and filing are separate acts on purpose. Deciding where something
belongs is slower than having the thought, and doing both at once is why inboxes
stop being used. `brain-triage` files things later, deliberately.

## Use this when

- The user says "remember this", "note that down", "capture this", "for the
  brain", or similar.
- Something worth keeping came out of the conversation and the user agreed it
  should be kept.

Do not capture unprompted. A running commentary written into someone's private
vault is noise they have to clean up.

## Not the assistant's memory

"Remember this" means the user's vault. If you also have a memory of your own —
a store of facts about the user, their preferences, or how you should work —
that is a different thing with a different owner, and it is not where a captured
thought goes.

The test is whose thought it is. Something the user thought, decided, noticed or
wants to find again is theirs: it goes in `inbox/`, in a file they can open,
edit and keep after this conversation and this assistant are gone. Only a fact
about how *you* should behave belongs in your own memory, and if a request is
genuinely both, write the capture first and mention the other.

A thought filed only in assistant-side memory is a thought the user cannot read,
cannot grep, and did not agree to store somewhere they do not control.

## What to write

Resolve the vault as `${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7 describes. The file
is `inbox/YYYY-MM-DD-HHMM-<slug>.md`, in the user's local time, with a
kebab-case slug of three or four words from the thought itself.

Frontmatter is exactly the `capture` schema in §2.1:

```yaml
---
type: capture
timestamp: <ISO 8601, with offset>
source: conversation
---
```

`source` is `conversation` when it came out of this session, `clip` when the
user pasted it from somewhere, `email` when it arrived as mail, `manual` when
they dictated it to be written down.

The body is the thought, in the user's own words where you have them. Add one
line of context if the thought will be unreadable in a month without it —
"came up while debugging the ingest retries" — and nothing else. No headings,
no summary of the conversation, no analysis.

## Rules

- **Never route at capture time.** Not into `projects/`, not into a person's
  note, not into `todo.md`. If the destination is obvious, still write the
  capture; triage will merge it in one step.
- **One thought per file.** Three things said in one breath are three captures.
- **Never write a secret** — no token, password, key or recovery code. If the
  thought contains one, write the rest and tell the user what you left out.
- **Do not overwrite.** If a file with that name exists, extend the slug rather
  than replacing it.
- **Confirm with the path.** Say what you wrote, as the vault-relative path, in
  backticks. That is the whole confirmation; do not read the file back.

If the user asks for something durable rather than a thought — a project note, a
person, a meeting — this is the wrong skill. Use the one that owns that type.

## Tools

- `Write` — create the capture file, at an absolute path under the vault.
- `Glob` — check whether that filename is already taken.
