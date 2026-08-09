# Brain Kit

A second brain your agent can actually use: a private Markdown vault with a
written standard, and the skills and rituals that keep it worth having.

No database, no application, no lock-in. A directory of Markdown files with YAML
frontmatter, linked by ordinary relative links — readable by any editor, any
`grep`, and you, in twenty years.

## Install

```
/plugin marketplace add wzslr321/torio
/plugin install brain-kit@torio
/brain-kit:init
```

`init` creates the vault at `~/brain`, or adopts a directory of notes you
already have — it adds what is missing and does not touch a single existing
note. Pass a path to put it somewhere else.

Then:

```
/brain-kit:daily      # open the day: carry-over, active projects, inbox depth
/brain-kit:triage     # empty the inbox
/brain-kit:meeting    # prep <who>, or debrief <who>
/brain-kit:weekly     # the review that asks whether "active" is still true
```

## What you get

Six skills the agent reaches for on its own, when what you are doing needs them.

| Skill | It fires when you |
| --- | --- |
| `brain-search` | ask what you already wrote down, or a task turns on an earlier decision |
| `brain-capture` | say "remember this" — one file in `inbox/`, no filing |
| `brain-triage` | say "process my inbox" — merge, promote, action or drop, to zero |
| `brain-daily` | start the day, or log something to today |
| `brain-meeting` | have a call coming up, or notes to write up afterwards |
| `brain-people` | ask who someone is, or what you owe them |

One subagent, `brain-librarian`, does the bulk work — a thirty-item inbox, every
index, every link — somewhere other than the conversation you are working in.

And one thing that is not a skill: at the start of every session, a map of your
vault goes into the agent's context — the path, your root `index.md`, the
directories and how many notes are in each. Never note contents.

That map is the difference between an agent that *can* read your notes and one
that knows they are there. Whether a given question turns on something you wrote
down is a judgement worth leaving to a model; whether the vault exists is not.
If you have no vault, or the path does not resolve, nothing is added and nothing
is said — this runs in every session, including all the ones that have nothing
to do with notes.

Your root `index.md` is carried up to **its first 25 lines below the
frontmatter**, and the rest is dropped without a word. That is a budget, not a
suggestion: write the index short and put what matters at the top, because a
section past the cut is one you will never see arrive.

## The standard

[`STANDARD.md`](STANDARD.md) is the normative part, and it is short. Eight note
types, each with its frontmatter; where files live and how they are named; which
links a note of each type owes; and the rules an agent follows when writing.

It is a profile of the **Open Knowledge Format** — Markdown plus YAML
frontmatter, `type` the only required field, relative links as the graph. Using
a published base means the frontmatter is not ours to design, only ours to
constrain.

Two clauses are worth knowing before you adopt it:

- **A note without frontmatter is valid to read.** Point this at a decade of
  existing notes and everything still works. Frontmatter arrives when a note is
  next edited for a real reason — never in a sweep.
- **No wikilinks.** Relative Markdown links only, because those can be checked
  by a link checker and followed by a plain reader.

[`examples/vault/`](examples/vault/index.md) is a complete tiny vault, one note
per type. Copy it somewhere scratch and run the commands at it.

## What it will not do

- **Hold a secret.** No token, password, key or recovery code reaches a note,
  ever. If you dictate one, the agent writes the rest and tells you what it left
  out.
- **Take the vault anywhere.** Note content does not go into a commit message, a
  pull request, an issue, a log, or a message to a third party. When a task
  needs a fact from your vault, the agent carries the fact, not the note.
- **Commit for you.** Your vault may be a Git repository. Its history is yours.
- **Delete.** Triage removes a capture it has already merged. Nothing else in
  the vault is the agent's to delete.
- **Rewrite what you wrote.** Skills append and preserve. Your phrasing in your
  own vault is not a draft awaiting improvement.

These are instructions to a model, which is not the same as a boundary. The
agent runs on your machine with your permissions; nothing here sandboxes it.
That is exactly the gap [Torio](../README.md) closes — see below.

## With Torio, or without it

Without: the kit is a plugin, the vault is a directory in your home, and the
agent is whatever you already run. That is a complete product and it is where
most people should start.

With: [Torio](../README.md) puts the same vault inside a Lima VM, owned by an
unprivileged guest identity with no `sudo`, no credential that reaches a Git
remote, and the edge of a VM between the agent and everything of yours it was
not given. Same standard, same shape, a boundary underneath it.

## Local development

Iterate without installing:

```bash
claude --plugin-dir /path/to/torio/brainkit
```

Skills live in `skills/<name>/SKILL.md`, commands in `commands/<name>.md`, the
subagent in `agents/`. Change the standard first and the skills second — they
cite it rather than repeating it, and that is deliberate.

The reasoning behind all of this is
[ADR-0010](../docs/adr/0010-okf-vault-standard-and-brain-kit.md).

MIT. See [LICENSE](../LICENSE).
