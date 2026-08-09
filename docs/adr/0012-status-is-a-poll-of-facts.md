# ADR-0012: Status is a poll of facts; events only ring the bell

- Status: Accepted
- Date: 2026-08-08
- Superseded in part by: [ADR-0013](0013-status-projections-are-maintained-by-torio.md),
  for the decision that tmux and prompt projections ship only as recipes rather
  than as maintained Torio renderers
- Applies to: `internal/backend`, `internal/status`, `internal/lima`,
  `internal/backend/claudecode`, `internal/config`, `internal/cli`

## Context

ADR-0009 made instances plural: one operator, several boxes, each running a
different agent. That creates a question no command answers today — across all
of them, which agents exist, which are working, which are waiting on a human,
which are dead. The operator wants that answer ambiently, in the terminal they
already sit in: a tmux status line, a prompt segment, a notification when an
agent blocks on a permission. Rendering any of those is trivial and none of it
is Torio's business. What is Torio's business is that there is currently no
truthful state to render, and a status surface that renders an untruthful one
is worse than none: a bar that says "working" over a dead agent teaches the
operator to stop looking at it.

The obvious design is event-driven — agents announce their state changes and
the host relays them. Three findings, from reading both backends and this
codebase, decide against it:

**No backend reports its own death.** Claude Code's hooks fire on session
start, stop, permission requests and notifications, but nothing fires on a
crash or a SIGKILL, and its session-end hooks run under a default budget of
1.5 seconds. Hermes ends a session only in its graceful teardown path: a
killed process leaves the session open in its database forever, and its
`is_active` flag is a 300-second heuristic over the last message row — false
for a live session inside a long tool call, true for a process that died four
minutes ago. The one state change a status surface exists to show — "this
agent stopped existing" — is exactly the one that emits no event. A lost
event lies until someone notices; a missed poll lies for one interval.

**Every event is a file write anyway.** A hook or a gateway callback runs in
the guest and can only leave a file behind. Turning that into a host-side
event needs one of: a held SSH channel watching for changes — and this
codebase has no long-lived child processes, no streams, and one sanctioned
long-lived shape, handing the terminal to ssh for an interactive session; a
shared filesystem — which bootstrap explicitly proves absent
(`verifyNoHostMounts`); or a host daemon listening for pushes — the shape
ADR-0008 deleted five and a half thousand lines to avoid shipping. Meanwhile
the poll shape already exists whole: one fresh `limactl shell` per question,
output capped, redacted, and refused when truncated. A stream has no
equivalent of `StdoutTruncated` — there is no moment at which a reader can
know it saw everything — so the streaming shape would reopen a solved
problem, not just cost more.

**The facts already exist; only the relays are missing.** A running agent is a
process in the guest's own table, and a working one leaves modification times
behind it: state files rewritten at turn boundaries, databases whose mtime moves
with a write. A probe that reads those at poll time answers "does this agent
exist and when did it last provably progress" without either backend changing at
all.

## Decision

### One schema, owned by Torio

`torio status` emits one document describing every instance. Torio never
learns a backend's native status format: a backend integrates by declaring a
probe whose output conforms to Torio's schema, and a future backend — Codex,
whatever follows — is added by writing that probe, touching no reader, no
schema, no renderer.

The schema is the intersection of what every backend can prove, not the
union of what each one knows. Every field carries four-valued semantics, the
same set `credentialState` already uses: not applicable (the capability is
undeclared), unknown (declared, but unprovable right now), or a value. A
renderer must show absence as absence — `—`, never `0`, never a green light —
because on a multi-backend host most of the surface is "not knowable here"
and that must be distinguishable from "all quiet".

The v1 vocabulary, chosen because each entry is provable today:

- **box** — running or stopped, a host-side fact from `limactl list --json`,
  which never enters a VM and costs nothing.
- **session** — exists or not, from the guest's own process table, filtered to
  the process name the backend declares its sessions run under. Written first as
  "a pid the backend already records": checked against both running boxes,
  neither backend records one — Claude Code keeps no roster and no pid file,
  Hermes holds its sessions as rows inside one long-lived service. The live
  table is the stronger reading anyway, because it cannot outlive the process it
  describes, which is exactly what a record left behind by a killed agent does.
- **waiting on a human** — the one field that justifies the whole surface,
  and the one exception below.
- **last progress** — the newest modification time among files the backend
  cannot help writing while it works. Explicitly not "last message": a backend
  that records a row per turn reads as dead throughout a long tool call, which
  is exactly when an operator is watching to see whether they are needed.

There is no `failed` state. Neither backend can prove one — Hermes has no
error value in its end reasons, Claude Code's failure hook covers API errors
only — and a field no probe can prove is the "check that passes without
checking" ADR-0009 forbids.

### Status is a poll of facts, never a cache of events

The reader is `torio status`: one `limactl list --json` for every instance's
box state, then one probe per running instance over the existing one-shot
transport. Probes read facts — pid liveness, file mtimes and sizes — not
documents that claim things. A status file written by hooks was considered
and rejected as this design's central trap: it is the event model wearing a
poll costume. The hook that would mark "ended" does not fire on a crash, so
the file says "running" forever, and the poll faithfully relays the lie.
Polling is only honest when what is polled cannot fail to be updated —
which is precisely what distinguishes a fact from a report.

One state is not a fact and gets a named exception: **waiting on a human**
exists only at the moment the agent asks, so it is carried by a marker file
an event writes, consumed with a TTL, and ranked below liveness — a waiting
marker for a session whose pid is gone renders as gone, and a marker older
than its TTL renders as unknown, never as a stale plea. The failure cost is
bounded and asymmetric on purpose: a lost marker is a missed ping, a stale
marker expires by itself.

### Events ring the bell; they never set the state

Push signals — Claude Code hooks today, a Hermes gateway change when it
lands — may trigger a notification: a terminal bell, a tab title, an OS
notification. That is a latency optimization over the poll, worth having
because the "an agent needs you now" moment deserves seconds, not a poll
interval. It is never a source of state, and no Torio process subscribes to
anything: whatever listens — tmux's status interval, a shell prompt, a hook
emitting an escape sequence — is owned by a process the operator already
runs. `--watch` is deferred with it: a foreground watcher is the first
long-lived Torio process outside an interactive session, and it adds
freshness, not correctness.

### The probe is a declared capability, and its output is untrusted

`Status()` joins `Registry()`, `Service()` and `Session()` in the backend
contract, nil first-class, under ADR-0009's rule: an instance whose backend
declares no status capability reports exactly that, exits 0, and runs no
guest command to discover what it was already told.

Probe output crosses the ADR-0002 boundary and is handled like every other
guest answer: size-capped, decoded strictly with unknown fields refused and
a second decode required to find EOF, and refused entirely when truncated.
Its values are render-only — no path, command, or control flow is ever
derived from them. Only identifiers and enum values may reach a terminal
escape sequence; agent-authored prose (session titles above all) never does,
for the same reason guest filenames never reach Torio's output today.

## Consequences

- A new command, `torio status`, with `--json`, one object per instance,
  absent capabilities explicit. It is the only piece of this design that is
  Torio code: the tmux line, the prompt segment, and the notification hook
  are configuration for tools the operator already runs, and ship as a
  documented recipe, not as a feature.
- Claude Code is the first backend with a probe: it declares the process name
  its sessions run under, and the marker its hooks write. It declares no
  progress reading, because the evidence it cannot help producing is a
  per-session transcript at a path named after the project and the session,
  which no fixed declaration can point at — and the one file that does sit
  still moves when a prompt is submitted rather than while one is worked on,
  which is the "last message" reading this record refuses. A session's own age
  answers that question better.
- Hermes reports last progress from what it already writes, and nothing else.
  It declares no session process, because a Hermes session is not one: the
  service holds its sessions as rows, and whether that service is up is what
  `serve status` already proves from systemd and the endpoint. Its truthful
  "waiting on approval" predicate lives only in process memory today, so its
  waiting field is unknown until a change in Hermes exports it; this ADR does
  not pretend otherwise.
- The waiting marker's path, format, and TTL become a documented convention
  a backend's hooks write to — owned by Torio like the schema, so the third
  backend's integration is a shim plus a declaration.

## Rejected alternatives

Collected above at the decisions they belong to: relaying events to the host
over a held channel, a shared filesystem, or a listening daemon (each
violates a standing decision — no streams, `verifyNoHostMounts`, ADR-0008);
a status document written by hooks as the source of truth (the lost-event
lie in poll costume); querying Hermes over HTTP through the operator's
tunnel (works for one backend, only while a hand-run tunnel lives — generic
on no day); Torio parsing each backend's native status format (a parser per
product, schema ownership inverted); a `failed` state in v1 (unprovable by
every current backend); and `--watch` (deferred, not refused).
