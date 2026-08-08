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

A run costs real money, because it drives a real agent. `--dry-run` is free and
is the right first move after editing a scenario.

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
