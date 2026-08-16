# Status contract

What `torio status --json` emits, what a backend declares to appear in it, and
the waiting-marker convention a backend's hooks write. The decision behind all
of it is [ADR-0017](../adr/0017-status-is-a-poll-of-facts.md); this document is
the shape.

One sentence carries the rest: the poll reads facts a backend cannot help
producing, never a document that reports its own state. No backend announces its
own death, so a status file written by hooks says "running" forever after a
crash and a poll faithfully relays the lie.

## The document

`data` is one object with one key. Instances are name-ordered.

```json
{
  "instances": [
    {
      "instance": "torio-claude-code",
      "box": "running",
      "backend":       { "state": "known", "name": "claude-code" },
      "session":       { "state": "known", "sessions": [
                           { "pid": 1234, "started_at": "2026-08-08T20:11:04Z", "age_seconds": 612 } ] },
      "waiting":       { "state": "known", "waiting": true, "waits": [
                           { "session_id": "abc123", "pid": 1234,
                             "age_seconds": 120 } ] },
      "last_progress": { "state": "not-applicable" }
    }
  ]
}
```

### `state` is read before anything beside it

Every field carries one of three states, and the payload beside it means nothing
until the state has been read:

| `state` | Meaning |
|---|---|
| `known` | The payload is proven. |
| `unknown` | The question applies and could not be answered right now: a box that could not be reached, output that could not be parsed, a marker too old to trust. |
| `not-applicable` | This backend answers no such question. Nothing was asked. |

The distinction is the point. On a host running several backends most of any row
is "not knowable here", and an operator who cannot tell that from "all quiet"
stops looking at the surface — which is the failure a status surface exists to
prevent. A renderer shows `unknown` as `?`, `not-applicable` as `—`, and neither
as a zero or a green light.

Payload fields carry their zero value when the state is not `known`. Read
`.state` first; `.waiting.waiting` is `false` under `"state": "unknown"` and
means nothing there.

### Fields

- **`instance`** — the Lima instance name.
- **`box`** — Lima's own word: `running`, `stopped`, `broken`, `unknown`. It is
  a host-side fact costing no guest command, so it is the one field always
  answered and the one field with no `state` beside it.
- **`backend`** — which agent the box was provisioned for, read from the
  document that box owns. Never `not-applicable`: every box runs exactly one
  backend. `unknown` means the document could not be read, or names a backend
  this binary does not have.
- **`session.sessions[]`** — the agent's live processes. `pid`, `started_at`
  (RFC 3339, UTC) and `age_seconds` are derived from the guest's process table
  and the guest's own clock. Always an array, never `null`, so a recipe can
  count it without first testing the state; `known` with an empty array is the
  proven answer that nothing is running.
- **`waiting`** — whether a human is being waited on. `waits` is always an
  array, with one entry per live waiting session. `session_id` is the bounded
  identifier Claude supplies to every hook, `pid` names the matching live
  process and `age_seconds` is that entry's age. `waits` is the only copy of
  those facts; renderers use its first entry and its length.
- **`last_progress`** — the newest modification time among files the backend
  cannot help writing while it works, as `at` (RFC 3339, UTC) and `age_seconds`.
  Deliberately not "when the last message was recorded": a backend that writes a
  row per turn reads as dead throughout a long tool call, which is exactly when
  an operator is watching to see whether they are needed.

Every age is computed on the guest, from its own clock, so drift between a
laptop that slept and a VM that did not can never invent one.

### Nothing an agent wrote appears here

Every value in the document is an identifier, an enumerated value, or a number.
There is no field for a session title, a prompt, a path or a message, and none
will be added: this output is rendered into terminals that interpret escape
sequences, and the guests it is read from run agents that write their own prose.

This is what makes the one-line formats safe rather than lucky. `torio status
--format tmux` interpolates document values straight into tmux's own `#[...]`
style sequences; it can do that because the schema has nowhere for an agent to
put one, not because the renderer escapes anything.

## What a backend declares

A backend declares a status probe or declares none. Declaring none is an answer:
the poll reports `not-applicable` and runs no guest command to discover what it
was already told.

| Declaration | Effect when empty |
|---|---|
| `SessionProcess` | The name a session's process reports in the guest's process table, as `ps -o comm=` prints it. Empty ⇒ `session` is `not-applicable`. |
| `ProgressPaths` | Guest files whose modification time is evidence of work. Empty ⇒ `last_progress` is `not-applicable`. |
| `WaitingMarker` | Whether this backend's hooks write the marker below. False ⇒ `waiting` is `unknown`, which is not the same as not waiting. |

Two constraints come from the kernel rather than from Torio. A process is named
after the path it was launched through, so a binary reached by a pinned symlink
reports the symlink's name; and that name is truncated to **fifteen characters**,
so a longer declaration silently matches nothing.

What the two shipped backends declare, and why each omission is deliberate:

| | `SessionProcess` | `ProgressPaths` | `WaitingMarker` |
|---|---|---|---|
| Claude Code | `claude` | none — its per-session transcript lives at a path named after the project and session, which no fixed declaration can point at | yes |

### What the poll runs

Per running box, as the backend's own identity, each a fixed argv with no shell:

1. `date +%s` — the guest clock every age is measured against.
2. `ps -o pid=,etimes=,comm= -u <identity>` — only when a session process is declared.
3. `find <fixed parent> -maxdepth 1 -name <fixed name> -type f -printf …`
   — one bounded call per declared path. It prints no other filename and exits
   zero when the exact name is absent, so absence is not inferred from a failed
   `stat`.
4. `cat -- <marker>` — only when the marker passed its ownership and mode gate.

A stopped box is asked nothing: nothing runs there, which proves both that no
session exists and that nobody on it is waiting. When it last progressed stays
`unknown` — that evidence is inside a VM that is not running.

`broken` and Lima's own `unknown` state prove no such thing. They are not
reachable, so they are asked nothing, but session, waiting and progress all stay
`unknown`; only the explicit `stopped` state proves a quiet box. This host-side
liveness answer does not depend on reading the box's backend document.

Output crosses the ADR-0002 boundary and is treated as such: bounded, refused
when truncated, and never used to derive a path, a command or a control-flow
decision.

## The waiting marker

"Waiting on a human" is the one state nothing continuously writes — an agent is
waiting only from the moment it asks until the moment it is answered — so it is
the single event-carried field in the document. Every rule around it exists to
bound what a lost or stale event can claim.

**Path** — `<identity home>/.torio-waiting.json`, written by the backend's hooks
as the backend's identity. It stays one fixed path: the poll never enumerates
agent-controlled filenames or derives a path from guest output.

**Content** — strictly decoded: unknown fields and a second document after it
are refused.

```json
{
  "schema_version": "2",
  "waits": [
    {
      "session_id": "abc123",
      "pid": 1234,
      "since_unix": 1786222152
    }
  ]
}
```

`session_id` comes from the common hook input of the pinned Claude Code 2.1.220
runtime. The root-owned helper selects it with the provisioned and verified
`jq-1.7`, accepts only a bounded identifier alphabet and discards every
prose-bearing field. `pid` is found by walking up the process tree to the
nearest ancestor that is the agent. Both are required: without a process the
entry cannot be ranked below liveness, so the hook fails closed rather than
writing a box-wide flag.

There is deliberately no free-text field, so there is nothing in a marker that a
rendered line could carry.

**Gate** — the file must be owned by the backend identity and must not be
group- or world-writable. A marker that fails the gate is `unknown`, never
absent: "someone else could have written this" and "nobody is waiting" are
different answers. The gate is checked from the guest's exact-name path fact before
the content is fetched. This is an operational drift detector, **not a security
boundary**: the agent runs as that same identity and can forge or remove its own
marker. Root ownership protects the helper and managed hook configuration from
silent retuning; it cannot make an agent-owned status signal authoritative.

**Age and size** — every wait older than **one hour**, measured from its `since_unix`
against the guest clock, expires. The empty document itself does not expire: it
is the persistent proof that bootstrap installed a working marker integration.
A wait nobody cleared would otherwise stay on the surface forever, and an
operator who learns to ignore one stale plea ignores the real one beside it.
One expired entry does not hide another live, fresh wait. Each helper update
prunes expired entries and keeps at most 64; the reader refuses a larger
document.

**Rank** — liveness wins, in both directions. Each entry whose pid is gone is
dropped; where liveness itself is `unknown`, so is waiting. The box reports
waiting while at least one fresh entry names a live session.

**Scope** — one fixed document per box, with one entry per session. Updates are
serialized by a fixed lock and rewrite the document atomically. A set replaces
only the entry with its `session_id`; a clear removes only that entry and
publishes an empty document when none remain. Two sessions waiting at once
therefore remain two independently actionable entries without making the poll
enumerate a directory. Bootstrap creates that empty document atomically; a
missing document means hook readiness is `unknown`, never proven quiet.

**Cost of a lost marker** — the waiting field becomes `unknown`. It does not
turn a broken hook path into a quiet answer.

### On a Claude Code box

Four hooks, named in the root-owned managed settings, run the root-owned helper
at `/usr/local/bin/torio-waiting-marker`:

| Event | Argument |
|---|---|
| `Notification` | `set` |
| `Stop` | `set` |
| `UserPromptSubmit` | `clear` |
| `SessionEnd` | `clear` |

Both root-owned files are installed by `torio vm bootstrap` and proven by digest
on every run: installed when absent, reported and never rewritten when they have
drifted. Bootstrap also initializes the private, agent-owned empty marker; this
is a drift detector and readiness fact, not a security boundary. A box
bootstrapped before the hooks existed reports `waiting` as unknown until the
operator resolves the reported drift and bootstraps it again.

The helper takes exactly one argument, `set` or `clear`. From
standard input it selects only the validated `session_id`; Claude Code also
feeds hooks fields containing session text, and none of them is copied, logged
or rendered.
