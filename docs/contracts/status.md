# Status contract

What `torio status --json` emits, what a backend declares to appear in it, and
the waiting-marker convention a backend's hooks write. The decision behind all
of it is [ADR-0012](../adr/0012-status-is-a-poll-of-facts.md); this document is
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
      "waiting":       { "state": "known", "waiting": true, "kind": "permission", "age_seconds": 120 },
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
prevent. A renderer must show both silences as absence, never as a zero and
never as a green light.

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
- **`waiting`** — whether a human is being waited on. `kind` is one of
  `permission` or `notification`; an unrecognized value makes the whole field
  `unknown` rather than reaching a renderer. `age_seconds` is the marker's own
  age.
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
| Hermes | none — a Hermes session is not a process; the service holds sessions as rows, and whether that service is up is what `torio serve status` answers | `state.db`, `state.db-wal` | no — the predicate exists but only in the memory of the running process |

### What the poll runs

Per running box, as the backend's own identity, each a fixed argv with no shell:

1. `date +%s` — the guest clock every age is measured against.
2. `ps -o pid=,etimes=,comm= -u <identity>` — only when a session process is declared.
3. `stat -c '%n|%U|%a|%Y' <progress paths...> [marker]` — one call for every path fact.
4. `cat -- <marker>` — only when the marker passed its ownership, mode and age gate.

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
as the backend's identity.

**Content** — strictly decoded: unknown fields refused, a second document after
it refused, an unrecognized `kind` refused.

```json
{"schema_version": "1", "kind": "permission", "pid": 1234}
```

`pid` is optional and is the session that is waiting. A backend's hook finds it
by walking up the process tree to the nearest ancestor that is the agent — a
hook runs as a child of the session that fired it — and writes the marker
without one if no ancestor matches, which is a weaker answer rather than a
missing one.

There is deliberately no free-text field, so there is nothing in a marker that a
rendered line could carry.

**Gate** — the file must be owned by the backend identity and must not be
group- or world-writable. A marker that fails the gate is `unknown`, never
absent: "someone else could have written this" and "nobody is waiting" are
different answers. The gate is checked from the guest's own `stat` output before
the content is fetched.

**Age** — a marker older than **one hour**, measured from its modification time
against the guest clock, is `unknown`. A marker nobody cleared would otherwise
stay on the surface forever, and an operator who learns to ignore one stale plea
ignores the real one beside it.

**Rank** — liveness wins, in both directions. A marker naming a pid that is gone
reports not-waiting; a marker naming no pid survives only while some session on
that box does. Where liveness itself is `unknown`, so is waiting: a marker that
cannot be ranked is not reported.

**Scope** — one marker per box, and it names one session. `pid` is what makes
the answer actionable where several agents share a box: it appears in `sessions`
above, so the waiting one can be told from the others by its age. A marker
without a pid is a statement about the box instead, ranked against every session
on it.

One file per box still bounds what it can say. Two sessions waiting at once are
one marker — the second to speak overwrites the first — and a prompt submitted
in either clears it for both, so a box with one session waiting and one being
answered reports not-waiting until the waiting one next speaks. That is the same
lost-ping failure this design accepts elsewhere, arriving through a second
session rather than through a missed event. A marker per session is the fix if
one box routinely runs several agents; it needs the reader to enumerate a
directory rather than stat a fixed path.

**Cost of a lost marker** — a missed notification, and nothing else. That is the
failure this design chose to accept, which is why absence with a live session is
reported as `known: not waiting` rather than as unknown.

### On a Claude Code box

Four hooks, named in the root-owned managed settings, run the root-owned helper
at `/usr/local/bin/torio-waiting-marker`:

| Event | Argument |
|---|---|
| `Notification` | `notification` |
| `Stop` | `notification` |
| `UserPromptSubmit` | `clear` |
| `SessionEnd` | `clear` |

Both files are installed by `torio vm bootstrap` and proven by digest on every
run: installed when absent, reported and never rewritten when they have drifted.
A box bootstrapped before the hooks existed reports drift and refuses until the
settings file is removed and bootstrap runs again — until then its `waiting`
field reads not-waiting, because no marker is ever written.

The helper takes exactly one argument, matched against a fixed list, and reads
nothing from standard input: Claude Code feeds a hook a JSON document carrying
the session's own text.
