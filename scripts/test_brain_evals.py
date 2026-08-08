#!/usr/bin/env python3
"""Offline tests for the behavioural benchmark's instrument.

Every test here runs without an API key and without spending anything. That is
the point: an instrument nobody can check offline is one nobody can argue with
when it disagrees with them, and the failure mode that matters most — an
assertion that silently passes because nothing evaluated it — is invisible from
a run that costs money to repeat.
"""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

import brain_evals as be


def recorded_stream(events: list[dict]) -> str:
    return "\n".join(json.dumps(e) for e in events) + "\n"


def tool_use(name: str, **inputs: object) -> dict:
    return {
        "type": "assistant",
        "message": {"content": [{"type": "tool_use", "name": name, "input": inputs}]},
    }


INIT = {
    "type": "system",
    "subtype": "init",
    "model": "claude-sonnet-5",
    "skills": ["brain-kit:brain-search"],
    "plugins": [{"name": "brain-kit"}],
}


def result_event(text: str, cost: float = 0.01, error: bool = False) -> dict:
    return {"type": "result", "result": text, "total_cost_usd": cost, "is_error": error}


class ParseStream(unittest.TestCase):
    def test_reads_answer_cost_tools_and_environment(self) -> None:
        stream = recorded_stream([
            INIT,
            tool_use("Read", file_path="/vault/index.md"),
            tool_use("Grep", pattern="digest", path="/vault"),
            result_event("pinned by digest", cost=0.042),
        ])
        parsed = be.parse_stream(stream)
        self.assertEqual(parsed.answer, "pinned by digest")
        self.assertAlmostEqual(parsed.cost, 0.042)
        self.assertEqual([c["name"] for c in parsed.tool_calls], ["Read", "Grep"])
        self.assertEqual(parsed.environment["model"], "claude-sonnet-5")
        self.assertEqual(parsed.environment["plugins"], ["brain-kit"])
        self.assertIsNone(parsed.error)

    def test_reports_an_agent_error(self) -> None:
        parsed = be.parse_stream(recorded_stream([INIT, result_event("Not logged in", error=True)]))
        self.assertEqual(parsed.error, "Not logged in")

    def test_a_stream_with_no_result_is_an_error_not_an_empty_pass(self) -> None:
        # A truncated stream must never look like a session that answered
        # nothing successfully, or a crashed run scores as a passing one.
        parsed = be.parse_stream(recorded_stream([INIT]))
        self.assertIsNotNone(parsed.error)

    def test_survives_a_malformed_line(self) -> None:
        stream = json.dumps(INIT) + "\nnot json at all\n" + json.dumps(result_event("ok")) + "\n"
        self.assertEqual(be.parse_stream(stream).answer, "ok")


class TraceOfToolCalls(unittest.TestCase):
    def setUp(self) -> None:
        self.vault = Path("/tmp/vault-under-test")

    def test_counts_reads_and_searches_inside_the_vault(self) -> None:
        calls = [
            {"name": "Read", "input": {"file_path": "/tmp/vault-under-test/resources/a.md"}},
            {"name": "Grep", "input": {"pattern": "x", "path": "/tmp/vault-under-test"}},
            {"name": "Read", "input": {"file_path": "/tmp/elsewhere/b.py"}},
        ]
        trace = be.trace_of(calls, self.vault)
        self.assertEqual(trace.reads, ["resources/a.md"])
        self.assertEqual(trace.searches, 1)
        self.assertTrue(trace.touched)

    def test_work_outside_the_vault_is_not_vault_access(self) -> None:
        calls = [{"name": "Edit", "input": {"file_path": "/tmp/repo/slug.py"}}]
        self.assertFalse(be.trace_of(calls, self.vault).touched)

    def test_a_shell_command_naming_the_vault_counts_as_touching_it(self) -> None:
        calls = [{"name": "Bash", "input": {"command": "grep -r digest /tmp/vault-under-test"}}]
        self.assertTrue(be.trace_of(calls, self.vault).touched)

    # The first correction this instrument needed, and the reason it needed one:
    # an agent that greps and cats its way through the vault had been recorded as
    # never searching it, which turned a read budget into an assertion nothing
    # evaluated.
    def test_a_shell_search_of_the_vault_counts_as_a_search(self) -> None:
        calls = [{"name": "Bash", "input": {"command": "grep -ril postgres /tmp/vault-under-test"}}]
        self.assertEqual(be.trace_of(calls, self.vault).searches, 1)

    def test_a_shell_read_of_the_vault_counts_the_files_it_named(self) -> None:
        calls = [{"name": "Bash", "input": {"command": "cat /tmp/vault-under-test/index.md /tmp/vault-under-test/todo.md"}}]
        self.assertEqual(be.trace_of(calls, self.vault).reads, ["index.md", "todo.md"])

    def test_a_shell_read_resolves_the_command_s_own_variable(self) -> None:
        command = 'V="/tmp/vault-under-test"\ncat "$V/resources/writing-style.md"'
        calls = [{"name": "Bash", "input": {"command": command}}]
        self.assertEqual(be.trace_of(calls, self.vault).reads, ["resources/writing-style.md"])

    def test_a_shell_command_that_only_writes_is_not_a_read(self) -> None:
        calls = [{"name": "Bash", "input": {"command": "mkdir -p /tmp/vault-under-test/inbox"}}]
        trace = be.trace_of(calls, self.vault)
        self.assertTrue(trace.touched)
        self.assertEqual((trace.reads, trace.searches), ([], 0))


class Frontmatter(unittest.TestCase):
    def test_reads_flat_scalars(self) -> None:
        fields = be.frontmatter("---\ntype: capture\nsource: manual\n---\nbody\n")
        self.assertEqual(fields, {"type": "capture", "source": "manual"})

    def test_a_note_without_frontmatter_has_none(self) -> None:
        self.assertEqual(be.frontmatter("# just a heading\n"), {})

    def test_list_items_do_not_become_fields(self) -> None:
        fields = be.frontmatter("---\ntype: meeting\nattendees:\n  - people/jan.md\n---\n")
        self.assertEqual(fields, {"type": "meeting", "attendees": ""})


class Diffing(unittest.TestCase):
    def test_names_created_modified_and_deleted(self) -> None:
        d = be.diff({"a": "1", "b": "2"}, {"b": "9", "c": "3"})
        self.assertEqual((d.created, d.modified, d.deleted), (["c"], ["b"], ["a"]))
        self.assertFalse(d.empty)

    def test_an_untouched_vault_is_empty(self) -> None:
        self.assertTrue(be.diff({"a": "1"}, {"a": "1"}).empty)


class VaultDiffAssertions(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.vault = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)

    def write(self, relative: str, text: str) -> None:
        path = self.vault / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def statuses(self, spec: dict, d: be.VaultDiff) -> list[str]:
        return [c.status for c in be.check_vault_diff(spec, d, self.vault)]

    def test_unchanged_fails_when_anything_moved(self) -> None:
        d = be.VaultDiff(created=["inbox/x.md"], modified=[], deleted=[])
        self.assertEqual(self.statuses({"unchanged": True}, d), ["fail"])
        self.assertEqual(self.statuses({"unchanged": True}, be.VaultDiff([], [], [])), ["pass"])

    def test_created_patterns_match_by_glob(self) -> None:
        d = be.VaultDiff(created=["inbox/2026-08-08-1200-note.md"], modified=[], deleted=[])
        self.assertEqual(self.statuses({"created": ["inbox/*.md"]}, d), ["pass"])
        self.assertEqual(self.statuses({"created": ["people/*.md"]}, d), ["fail"])

    def test_not_created_catches_a_second_note_about_one_thing(self) -> None:
        d = be.VaultDiff(created=["resources/pr-rules.md"], modified=["resources/writing-style.md"], deleted=[])
        self.assertEqual(self.statuses({"not_created": ["resources/*"]}, d), ["fail"])

    def test_frontmatter_of_a_created_note(self) -> None:
        self.write("inbox/a.md", "---\ntype: capture\nsource: conversation\n---\n")
        d = be.VaultDiff(created=["inbox/a.md"], modified=[], deleted=[])
        spec = {"created_frontmatter": {"inbox/*.md": {"type": "capture"}}}
        self.assertEqual(self.statuses(spec, d), ["pass"])
        spec = {"created_frontmatter": {"inbox/*.md": {"type": "daily"}}}
        self.assertEqual(self.statuses(spec, d), ["fail"])

    def test_content_matches_needs_every_pattern_in_one_file(self) -> None:
        self.write("a.md", "carries alpha\n")
        self.write("b.md", "carries beta\n")
        d = be.VaultDiff(created=["a.md", "b.md"], modified=[], deleted=[])
        self.assertEqual(self.statuses({"content_matches": {"*.md": ["alpha", "beta"]}}, d), ["fail"])
        self.assertEqual(self.statuses({"content_matches": {"*.md": ["alpha"]}}, d), ["pass"])

    def test_content_not_matches_reports_the_offender(self) -> None:
        self.write("test_slug.py", "from unittest.mock import MagicMock\n")
        d = be.VaultDiff(created=["test_slug.py"], modified=[], deleted=[])
        checks = be.check_vault_diff({"content_not_matches": {"*.py": ["MagicMock"]}}, d, self.vault)
        self.assertEqual(checks[0].status, "fail")
        self.assertIn("test_slug.py", checks[0].detail)

    def test_max_created_bounds_the_sprawl(self) -> None:
        d = be.VaultDiff(created=["a.md", "b.md", "c.md"], modified=[], deleted=[])
        self.assertEqual(self.statuses({"max_created": 2}, d), ["fail"])


class AnswerAssertions(unittest.TestCase):
    def test_matches_and_avoids(self) -> None:
        checks = be.check_answer({"matches": ["(?i)digest"], "not_matches": ["latest tag"]},
                                 "We pin by DIGEST, never by tag.")
        self.assertEqual([c.status for c in checks], ["pass", "pass"])

    def test_a_failure_carries_the_answer_back(self) -> None:
        checks = be.check_answer({"matches": ["digest"]}, "no idea")
        self.assertEqual(checks[0].status, "fail")
        self.assertIn("no idea", checks[0].detail)


class TraceAssertions(unittest.TestCase):
    def test_a_blind_runner_skips_and_never_passes(self) -> None:
        # The invariant this suite exists to protect: a runner that cannot see
        # tool calls must not score higher than one that can.
        spec = {"no_vault_access": True, "max_vault_reads": 3, "vault_reads_include": ["index.md"]}
        checks = be.check_trace(spec, None)
        self.assertEqual({c.status for c in checks}, {"skip"})
        self.assertEqual(len(checks), 3)

    def test_no_vault_access(self) -> None:
        quiet = be.Trace(reads=[], searches=0, touched=False)
        busy = be.Trace(reads=["index.md"], searches=0, touched=True)
        self.assertEqual(be.check_trace({"no_vault_access": True}, quiet)[0].status, "pass")
        self.assertEqual(be.check_trace({"no_vault_access": True}, busy)[0].status, "fail")

    def test_read_budget_and_required_reads(self) -> None:
        trace = be.Trace(reads=["index.md", "resources/writing-style.md"], searches=1, touched=True)
        self.assertEqual(be.check_trace({"max_vault_reads": 2}, trace)[0].status, "pass")
        self.assertEqual(be.check_trace({"max_vault_reads": 1}, trace)[0].status, "fail")
        self.assertEqual(
            be.check_trace({"vault_reads_include": ["resources/writing-style.md"]}, trace)[0].status,
            "pass",
        )
        self.assertEqual(be.check_trace({"min_vault_searches": 2}, trace)[0].status, "fail")


class TrialOutcome(unittest.TestCase):
    def trial(self, checks: list[be.Check], error: str | None = None) -> be.Trial:
        return be.Trial("s", 0, checks, 0.0, error, Path("/tmp"))

    def test_a_trial_with_no_checks_never_passes(self) -> None:
        # A scenario whose assertions all evaluated to nothing is not a pass;
        # it is a scenario that measured nothing.
        self.assertFalse(self.trial([]).passed)

    def test_skipped_assertions_do_not_fail_a_trial(self) -> None:
        self.assertTrue(self.trial([be.Check("a", "pass"), be.Check("b", "skip")]).passed)

    def test_a_run_error_fails_the_trial(self) -> None:
        self.assertFalse(self.trial([be.Check("a", "pass")], error="timed out").passed)


class ScenarioValidation(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)

    def scenario(self, **overrides: object) -> dict:
        base = {
            "name": "example",
            "family": "retrieval",
            "claim": "It does the thing.",
            "fixture": "engineering",
            "threshold": 0.8,
            "sessions": [{"prompt": "go"}],
            "assert": {"vault_diff": {"unchanged": True}},
        }
        base.update(overrides)
        return base

    def write(self, scenario: dict, name: str = "example") -> Path:
        path = self.dir / f"{name}.json"
        path.write_text(json.dumps(scenario), encoding="utf-8")
        return path

    def test_a_valid_scenario_loads(self) -> None:
        self.assertEqual(be.load_scenario(self.write(self.scenario()))["name"], "example")

    def test_an_unknown_assertion_key_is_an_error(self) -> None:
        # The whole reason for strict keys: a typo would otherwise disable the
        # assertion and report the scenario as passing.
        broken = self.scenario(**{"assert": {"vault_diff": {"unchagned": True}}})
        with self.assertRaises(ValueError) as raised:
            be.load_scenario(self.write(broken))
        self.assertIn("unchagned", str(raised.exception))

    def test_an_empty_assert_block_is_an_error(self) -> None:
        with self.assertRaises(ValueError):
            be.load_scenario(self.write(self.scenario(**{"assert": {}})))

    def test_the_name_must_match_the_filename(self) -> None:
        with self.assertRaises(ValueError):
            be.load_scenario(self.write(self.scenario(name="other")))

    def test_a_session_index_outside_the_list_is_an_error(self) -> None:
        broken = self.scenario(**{"assert": {"answer": {"matches": ["x"], "session": 3}}})
        with self.assertRaises(ValueError):
            be.load_scenario(self.write(broken))

    def test_workspace_assertions_need_a_workspace(self) -> None:
        broken = self.scenario(**{"assert": {"workspace_diff": {"created": ["a.py"]}}})
        with self.assertRaises(ValueError):
            be.load_scenario(self.write(broken))

    def test_an_unknown_fixture_is_an_error(self) -> None:
        with self.assertRaises(ValueError):
            be.load_scenario(self.write(self.scenario(fixture="no-such-fixture")))


class ShippedScenarios(unittest.TestCase):
    """The scenarios in this repository, held to their own rules."""

    def setUp(self) -> None:
        self.paths = sorted(be.SCENARIO_DIR.glob("*.json"))

    def test_there_are_scenarios(self) -> None:
        self.assertTrue(self.paths, "no scenarios found; the benchmark would pass by being empty")

    def test_every_scenario_is_valid(self) -> None:
        for path in self.paths:
            with self.subTest(scenario=path.stem):
                be.load_scenario(path)

    def test_every_regular_expression_compiles(self) -> None:
        import re

        for path in self.paths:
            scenario = be.load_scenario(path)
            spec = scenario["assert"]
            patterns: list[str] = []
            patterns += spec.get("answer", {}).get("matches", [])
            patterns += spec.get("answer", {}).get("not_matches", [])
            for group in ("vault_diff", "workspace_diff"):
                for key in ("content_matches", "content_not_matches"):
                    for values in (spec.get(group, {}).get(key) or {}).values():
                        patterns += values
            for pattern in patterns:
                with self.subTest(scenario=path.stem, pattern=pattern):
                    re.compile(pattern)

    def test_the_precision_family_exists(self) -> None:
        # ADR-0011: measuring only whether the agent reaches for the vault
        # optimises for an agent that always does, which is the failure nobody
        # notices until it is reading private notes to answer a shell question.
        families = {be.load_scenario(p)["family"] for p in self.paths}
        self.assertIn("precision", families)


class Rendering(unittest.TestCase):
    def summary(self, **overrides: object) -> dict:
        base = {
            "name": "retrieval-one-hop", "family": "retrieval", "claim": "It follows the link.",
            "threshold": 0.8, "trials": 5, "passed": 4, "rate": 0.8, "met": True,
            "cost": 0.5, "failures": {}, "skipped": [], "example": "",
        }
        base.update(overrides)
        return base

    def meta(self) -> dict:
        return {
            "label": "baseline", "date": "2026-08-08", "model": "claude-sonnet-5",
            "runner": "claude-code", "isolation": "strict", "hooks": False,
            "trials": 5, "cost": 1.25, "skills": ["brain-kit:brain-search"],
        }

    def test_a_report_states_the_model_and_the_skills_in_the_room(self) -> None:
        text = be.render([self.summary()], self.meta(), None)
        self.assertIn("claude-sonnet-5", text)
        self.assertIn("brain-kit:brain-search", text)
        self.assertIn("4/5", text)

    def test_a_report_refuses_to_call_a_fraction_a_probability(self) -> None:
        self.assertIn("not a probability", be.render([self.summary()], self.meta(), None))

    def test_a_report_does_not_present_a_token_valuation_as_a_bill(self) -> None:
        # A subscription run is not billed the API list price of its tokens.
        # Labelling the estimate "cost" reads as an invoice, and a report has no
        # business implying one.
        text = be.render([self.summary()], self.meta(), None)
        self.assertIn("not an amount billed", text)
        self.assertNotIn("- Cost:", text)

    def test_failures_carry_their_first_observation(self) -> None:
        summary = self.summary(
            passed=1, met=False,
            failures={"agent read resources/writing-style.md": {"count": 4, "detail": "reads=[] searches=0"}},
        )
        text = be.render([summary], self.meta(), None)
        self.assertIn("4/5", text)
        self.assertIn("reads=[] searches=0", text)

    def test_a_baseline_adds_a_comparison_column(self) -> None:
        baseline = {"retrieval-one-hop": {"passed": 2, "trials": 5}}
        text = be.render([self.summary()], self.meta(), baseline)
        self.assertIn("Baseline", text)
        self.assertIn("2/5", text)


class Summarising(unittest.TestCase):
    def test_counts_passes_and_gathers_failures(self) -> None:
        scenario = {"name": "s", "family": "f", "claim": "c", "threshold": 0.8}
        trials = [
            be.Trial("s", 0, [be.Check("a", "pass")], 0.1, None, Path("/tmp")),
            be.Trial("s", 1, [be.Check("a", "fail", "reads=[]")], 0.1, None, Path("/tmp")),
        ]
        summary = be.summarise(scenario, trials)
        self.assertEqual(summary["passed"], 1)
        self.assertEqual(summary["failures"]["a"], {"count": 1, "detail": "reads=[]"})
        self.assertFalse(summary["met"])

    def test_a_skipped_assertion_is_listed_rather_than_counted(self) -> None:
        scenario = {"name": "s", "family": "f", "claim": "c", "threshold": 1.0}
        trials = [be.Trial("s", 0, [be.Check("a", "pass"), be.Check("t", "skip")], 0.0, None, Path("/tmp"))]
        summary = be.summarise(scenario, trials)
        self.assertEqual(summary["skipped"], ["t"])
        self.assertTrue(summary["met"])


if __name__ == "__main__":
    unittest.main()
