# The behavioural benchmark

The rest of this kit is documents. Documents can be reviewed, and reviewing them
is how the two worst defects found so far got *shipped*: a capture skill that
filed the user's thought into the assistant's own memory instead of their vault,
and a triage skill that deleted a routed capture while leaving four notes
pointing at nothing. Both skills read correctly. Both behaved wrongly.

This directory is the answer to that. It hands a real agent a fixture vault and
a prompt, and then checks what actually happened. [ADR-0011](../../docs/adr/0011-measured-brain-behaviour.md)
is the reasoning; this is the operating manual.

## Running it

```bash
make brain-evals                        # every scenario, 5 trials each
make brain-evals TRIALS=1               # one pass, for wiring changes
make brain-evals SCENARIO=retrieval-one-hop
make brain-evals ARGS="--family precision --trials 3"
python3 scripts/brain_evals.py --dry-run   # validate scenarios, spend nothing
```

A run drives a real agent, so it spends real tokens. What it spends them *from*
depends on how you are authenticated: with `ANTHROPIC_API_KEY` set, an API
account is billed; on a Claude subscription, the run draws against that plan's
usage and nothing is billed.

Reports state token spend valued at API list prices, because that is the number
the agent reports and the only one comparable between runs. On a subscription it
is a valuation, not an invoice — read it as "this run was worth about that much
of my usage". `--dry-run` spends nothing and is the right first move after
editing a scenario.

### Scoring a finished run again

Every trial leaves its transcripts and its final vault in a working directory,
printed at the top of the run. `--replay` scores that directory again with
today's assertions, and spends nothing:

```bash
python3 scripts/brain_evals.py --replay /tmp/torio-brain-evals-… --label baseline
```

This exists because the first correction this instrument needed came out of a
run. The trace counted only typed tool calls, so an agent that searched the
vault through the shell — `grep`, then `cat` — was recorded as never having
searched at all, and a read budget became an assertion nothing evaluated.
Replaying applied the fix to the evidence that produced it, rather than paying
for a second sample and comparing two different ones.

Assertions get corrected more often than agents change their behaviour. Reach
for `--replay` whenever the thing you changed is the question, not the subject.

### Authentication and isolation

The benchmark measures **the kit**, so nothing else may be in the room. Your own
skills and plugins are exactly the kind of thing that quietly changes a result:
a personal skill for meeting notes, or a plugin that captures tasks, competes
for the same prompts these scenarios use.

Strict isolation is therefore the default. It runs the agent under a throwaway
`HOME`, so only the kit is loaded, and it needs a token of its own:

```bash
claude setup-token          # once; prints a long-lived token
export CLAUDE_CODE_OAUTH_TOKEN=…
make brain-evals
```

Without a token, pass `--isolation loose`. That reuses your own `HOME`, disables
your enabled plugins, and cannot stop your personal skills from loading. It is a
usable signal and it is not a clean measurement — so the report lists every skill
that was in the room, and you should read that list before believing a number.

## Reading a report

Reports land in `results/`, as a Markdown document and a JSON sibling, and they
are committed. The report is the deliverable; the harness is how it was made.

A rate is a fraction of trials — `4/5`, not "80% of the time". At five trials
this separates working from broken. It does not separate 95% from 99%, and
telling those apart takes several hundred trials per scenario, which is a
decision about money rather than about code.

Three things in a report are worth more than the headline number:

- **Skipped assertions.** A runner that cannot observe tool calls reports those
  assertions as skipped, never as passed. A suite that quietly drops what it
  cannot check scores *higher* when it can see less.
- **The first failure fragment.** Each failed assertion carries the first
  observation that failed it, so you can tell a kit defect from a fixture that
  never left a trace the assertion could see.
- **The skill list.** A pass-rate is a fact about this kit and that model under
  those skills.

Comparing two runs:

```bash
python3 scripts/brain_evals.py --baseline results/2026-08-08-baseline.json --label with-hook
```

## What has been measured

| Run | Overall | What it showed |
| --- | --- | --- |
| [2026-08-08, skills only](results/2026-08-08-baseline.md) | 24/55 | The retrieval skill is good and almost never consulted. Without a map of the vault in context the agent did not know one existed, so `linkage` scored 0/10 — and `precision` scored 10/10 for the same reason, not a different one. |
| [2026-08-08, with the vault map](results/2026-08-08-with-hook.md) | 45/55 | Same skills, same fixture, one hook: `linkage` 0/10 → 10/10 and `retrieval` 12/20 → 20/20, with `precision` unchanged at 10/10. Autonomy did not cost privacy. Writing is still the broken half. |

The open finding both runs agree on: told a standing rule, the agent records it
in assistant-side memory rather than the user's vault — somewhere the user
cannot read, cannot grep, and did not choose. That is the same defect the
pre-release manual pass found in `brain-capture`, arriving through a different
door, and it is why this directory exists.

## The families

| Family | Asks |
| --- | --- |
| `linkage` | Does the agent reach for the vault when the task turns on something written there — without being told to? |
| `precision` | Does it leave the vault alone when the task does not? |
| `retrieval` | Does it find the right note, cite it, stay inside a read budget, and admit a miss instead of inventing one? |
| `self-update` | Does a correction become a note, in the right note, and does a later session that never heard it behave differently? |

`precision` is not an afterthought. An agent that reads the vault on every prompt
scores perfectly on linkage and is unusable: it spends context on nothing and
puts private notes in front of unrelated work. Autonomy and trustworthiness are
one measurement seen from two sides.

## Writing a scenario

A scenario is a JSON document in `scenarios/`, named after its file. It names a
fixture, one or more prompts, and what must be true afterwards. No backend
appears anywhere in it — a scenario is data, and a runner is the part that knows
how to drive one agent.

```json
{
  "name": "retrieval-one-hop",
  "family": "retrieval",
  "claim": "One sentence stating what a passing run proves.",
  "fixture": "engineering",
  "workspace": "fake-repo",
  "git": true,
  "threshold": 0.8,
  "sessions": [{ "prompt": "…" }],
  "assert": {
    "vault_diff": { "unchanged": true },
    "workspace_diff": { "created": ["test_slug.py"] },
    "answer": { "matches": ["(?i)digest"] },
    "trace": { "vault_reads_include": ["resources/image-pinning.md"] }
  }
}
```

Each session is a separate agent process. That is what makes a multi-session
scenario mean anything: the second session cannot have heard the first, so if it
behaves differently, something in the vault is why.

Assertions:

| Group | Keys |
| --- | --- |
| `vault_diff`, `workspace_diff` | `unchanged`, `created`, `not_created`, `modified`, `not_modified`, `no_deletions`, `max_created`, `created_frontmatter`, `content_matches`, `content_not_matches` |
| `answer` | `matches`, `not_matches`, `session` |
| `trace` | `no_vault_access`, `vault_reads_include`, `max_vault_reads`, `min_vault_reads`, `min_vault_searches`, `session` |

`answer` and `trace` look at the last session unless `session` says otherwise.
An unknown key is an error, not a warning: a mistyped assertion name would
otherwise disable the assertion and report the scenario as passing.

Two rules that decide whether a scenario is worth having:

**Assert against the artifact, not the prose.** Where a claim could be checked
either in what the agent said or in what the vault now contains, check the
vault. The prose is what it said it did.

**Make the right behaviour leave a mechanical trace.** Assertions here are
diffs and regular expressions — no model grades another model, because that puts
a second stochastic system inside the instrument and makes every failure
ambiguous. The cost lands on fixture design: a convention in a fixture note has
to be specific enough to be visible, like a required `## Risk` heading or a
literal `# owner: platform-team` line, rather than "writes clearly".

## Another backend

Torio runs more than one agent and intends to run more, so a suite that could
only speak to one of them would measure the kit on one backend and say nothing
about the product. Adding a second is a runner, not a second set of scenarios —
and if it ever needs its own scenarios, the scenario format is what got it wrong.

A runner is five members, stated as a `Protocol` in `scripts/brain_evals.py`:
`name`, `observes_tools`, `describe()`, `prepare()`, `run()`. The shipped one is
about 90 lines of a 1000-line harness; nothing in `scenarios/` or `fixtures/`
names a backend at all. `scripts/test_brain_evals.py` drives the whole trial path
with a runner that is not Claude Code and does not exist, so the seam stays real
rather than aspirational.

The honest part is `observes_tools`. A backend that cannot report which files its
agent touched sets it to `False`, and every `trace` assertion is then **skipped,
never passed**. The two strongest assertion groups survive that intact — the
vault diff and the answer are observable from any agent you can hand a prompt and
a directory — so such a backend still gets a real measurement, with a smaller
part of it visible. What it must not do is score higher for seeing less.

What does not travel is the *rendering*. The session-start vault map ships here
as a Claude Code plugin hook; another backend needs its own plumbing for the same
job, which is why `STANDARD.md` §9 states the requirement in terms of what a
rendering owes the agent rather than in terms of hooks.

Numbers do not travel either. A report names its model and its runner because a
pass-rate is a fact about a pair. A second backend earns its own reports; it does
not inherit these.

## Fixtures

A fixture is a directory under `fixtures/` with a `vault/` and optionally a
workspace directory beside it. Both are copied into a scratch directory per
trial; nothing here ever runs against a real vault.

`engineering/` is the one fixture so far: a small conforming vault whose
resource notes carry deliberately checkable conventions, plus `fake-repo/`, a
repository small enough to reason about. A `.evals-baseline/` directory inside a
workspace holds the earlier version of any file the scenario's prompt treats as
"the change I just made"; with `"git": true` those versions are committed first
and the current ones restored, so `git diff` shows exactly that change.

Fixture notes are ordinary Markdown in this repository, so `make validate`
link-checks them like anything else. A fixture with a dangling link fails the
build.
