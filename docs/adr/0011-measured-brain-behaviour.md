# ADR-0011: What the brain does autonomously is measured, and the measurement is backend-neutral

- Status: Accepted
- Date: 2026-08-08
- Applies to: `brainkit/evals/`, `scripts/brain_evals.py`, `brainkit/hooks/`

## Context

[ADR-0010](0010-okf-vault-standard-and-brain-kit.md) wrote the vault format down
and shipped it as a kit. The kit's claim is not that the format is well specified
— that is checkable by reading — but that an agent holding it *behaves* a certain
way: reaches for the vault when a task turns on something written there, leaves
the vault alone when it does not, cites the note it used instead of inventing an
answer, and records a correction the user gives so the next session does not need
it again.

Nothing checks any of that. The kit shipped on one manual behavioural pass, and
that pass was not decoration: it found two real defects that reading could not
have found. `brain-capture` filed a "remember this" into assistant-side memory
instead of the user's vault, and `brain-triage` deleted a routed capture while
leaving the notes that linked to it pointing at nothing. Both were fixed. Neither
would have been noticed by a link checker, a test of the Go binary, or a careful
re-read of the skill.

That is the whole problem. The kit's quality lives in a layer nothing in this
repository can currently observe: what a model does with a document, in a
directory, when nobody told it to. A second brain that is powerful only on paper
is indistinguishable, from the tree, from one that works — which means the next
regression ships silently, exactly as those two nearly did.

There is a second reason to build this now rather than later. The kit is about to
grow mechanisms whose only justification is behavioural — a hook that puts a map
of the vault into context at session start is a bet that awareness is the binding
constraint on autonomy. Without a measurement taken *before* it, that bet can
never be settled. It will simply be added, and everyone will assume it helped.

## Decision

### Behaviour is measured, and the measurement is committed

`brainkit/evals/` holds scenarios; `scripts/brain_evals.py` runs them against a
real agent over a scratch copy of a fixture vault; the reports go in
`brainkit/evals/results/` and are committed alongside the code they judge.

The deliverable is the report, not the harness. A pass-rate that lives only in a
terminal is a claim again, and this record exists because claims about this layer
are what failed.

Every report names its model and its date, and neither is decoration. An eval
measures a *pair* — this kit, that model — at a point in time. A pass-rate
without a model attached is not a weaker fact; it is not a fact.

### Scenarios are backend-neutral; only the runner is per-backend

Torio runs more than one agent backend and intends to run more, so a behavioural
suite that can only speak to one of them would measure the kit's quality on one
backend and say nothing about the product. Scenarios are therefore data — a
fixture, one or more prompts, and assertions — with no backend named anywhere in
them, and a runner is the small piece that knows how to drive one agent.

The split that makes this work is in the assertions:

- **Portable assertions** — what changed in the vault, and what the answer said —
  are observable from any agent that can be handed a prompt and a directory.
  They are also the assertions that matter most, for the reason below.
- **Capability-gated assertions** — which files the agent read, and how many —
  need a runner that can observe tool calls. A runner that cannot declares so,
  and the report marks those assertions **skipped**. It never counts them as
  passing.

A suite that silently drops what it cannot check reports a higher number on a
weaker runner, which is precisely backwards.

### The vault diff is the primary evidence

Where an assertion could be written either against the agent's prose or against
the state of the vault afterwards, it is written against the vault. The prose is
what the agent said it did; the vault is what it did. Only one of them is still
true tomorrow, and only one of them is what the user actually keeps.

This is also what makes the self-update family meaningful. "Did the agent record
the correction?" is answered by a file appearing or a section growing — and then
by a *second, fresh session* behaving differently because of it. A single session
claiming it will remember something proves nothing; the memory is the artifact,
and the artifact is testable.

### Negative scenarios carry the same weight as positive ones

For every family that asserts the agent reaches for the vault, there is a
scenario asserting it does not. An agent that reads the vault on every prompt
would score perfectly on recall and be unusable: it would leak private notes into
unrelated work, spend context on nothing, and make invariant 13's read-path an
everyday event rather than a deliberate one.

"Autonomous" and "trustworthy" are the same measurement taken from two sides.
Only measuring one side optimises for the wrong thing, and the optimisation is
invisible until someone notices the agent grepping their private notes to answer
a question about a shell flag.

### Assertions are mechanical, not judged by a model

Assertions are file-system diffs, frontmatter reads and regular expressions over
the answer. No model grades another model's output here.

Rejected: an LLM judge for assertions like "does this answer reflect the recorded
writing style". It is more expressive, and it puts a second stochastic system
inside the instrument. A failure would then be ambiguous between the subject and
the judge, and the only way to resolve it would be a third measurement. Mechanical
assertions are cruder and they are *evidence*: when one fails, the failure is the
finding. The cost is that fixtures must be written so the right behaviour leaves a
mechanically visible trace — a distinctive phrase, a named file — which is a
constraint on scenario design, and an acceptable one.

### No CI gate yet, and that is a cost decision with a date on it

The suite runs by hand, from `make brain-evals`. There is no workflow, no
schedule, and no required check.

Every trial spends real tokens against a real API, so a gate here is a recurring
bill and needs a number behind it, not an intuition. The first run produces that
number — the harness reports per-run cost — and the cadence decision is made
against it in a later change.

Rejected: adding the nightly workflow now and tuning it afterwards. It commits
the project to a monthly cost before anyone has seen a single real invoice for
this suite, and an unloved scheduled job that nobody reads is worse than no job:
it converts a measurement into noise.

Rejected: gating pull requests that touch `brainkit/**`. Same objection with a
worse failure mode — a stochastic check on a required path teaches contributors
to re-run until green, which is how a suite stops meaning anything.

### The suite does not claim a precision it cannot buy

The motivating ambition was "99% of the time". At five or ten trials per
scenario, a pass-rate is reported as the fraction it is — `5/5`, `4/5` — and the
report says what that does and does not support. Separating 99% from 95% takes
several hundred trials per scenario; that is a decision about money, and it
belongs to whoever later decides the cadence.

Reporting `5/5` as "99%" would be the same failure this record exists to fix,
committed by the instrument instead of the prose.

## Consequences

- The kit gains a second audience for its fixtures. `brainkit/evals/fixtures/`
  vaults are behavioural inputs first, but `scripts/validate_artifacts.py` walks
  every `*.md` in the tree, so their internal links are checked on every run like
  any other document. A fixture with a dangling link fails `make validate`.
- The harness is stdlib-only Python under `scripts/`, tested by
  `scripts/test_brain_evals.py` against recorded agent output. The instrument is
  itself testable without an API key, and those tests run in `make validate`.
  An instrument nobody can check offline is one nobody can trust when it
  disagrees with them.
- Two numbers now exist for every behavioural change to the kit: before and
  after. The vault-map hook is the first thing measured this way, and if the
  delta is small that is a finding worth having rather than a disappointment.
- Nothing under `internal/`, `cmd/` or `e2e/` changes. The suite drives an agent
  holding the kit, not the Go binary; a Torio-guest runner is future work and is
  a runner, not a new set of scenarios.
- A failing scenario is not automatically a kit defect. It may be a fixture that
  fails to leave a mechanical trace, or a prompt more ambiguous than intended.
  The report carries the failing assertion and the relevant fragment so that
  question can be answered from the report rather than by re-running.

## Rejected alternatives

Collected above at the decisions they belong to: an LLM judge inside the
instrument; a nightly workflow before a cost is known; a required check on
`brainkit/**`. Two more:

**Testing the skills as documents** — asserting that `SKILL.md` contains certain
instructions. It is cheap, deterministic, and measures the wrong thing entirely:
it would have passed on both defects the manual pass found, because in each case
the skill said the right thing and the agent did something else.

**A single long scenario exercising every ritual in one session.** It reads like
a realistic day and it is useless as an instrument. One failure anywhere marks
the whole session failed, the failure cannot be attributed, and the trials cost
the same. Scenarios stay small, single-claim and independent, which is also what
makes a pass-rate per scenario meaningful.
