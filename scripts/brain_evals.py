#!/usr/bin/env python3
"""Run the Brain Kit behavioural benchmark and write a report.

ADR-0011 is the reasoning; this is the instrument. A scenario hands a real agent
a fixture vault, a prompt, and nothing else, then asks two questions afterwards:
what changed in the vault, and what did the answer say. Both are checked
mechanically. No model grades another model here.

Only the standard library is used, for the same reason the rest of `scripts/`
avoids dependencies: this has to run on a fresh checkout with nothing installed.

Two things about this file are deliberate and easy to get wrong when editing it.

The first is that assertions a runner cannot observe are reported **skipped**,
never passed. Tool-call traces need a runner that can see tool calls; a runner
that cannot must not score higher for being blind.

The second is that the report records the environment it measured — the model,
and the exact skill list the agent loaded. A pass-rate is a fact about a pair,
this kit and that model, under those skills. Detached from them it is not a
weaker fact, it is not a fact.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime
import fnmatch
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
KIT = ROOT / "brainkit"
EVALS = KIT / "evals"
SCENARIO_DIR = EVALS / "scenarios"
FIXTURE_DIR = EVALS / "fixtures"
RESULT_DIR = EVALS / "results"

# Every key a scenario document may carry. Unknown keys are an error rather than
# a shrug: a mistyped assertion name would otherwise disable the assertion and
# report the scenario as passing, which is the one failure an instrument may not
# have.
SCENARIO_KEYS = {"name", "family", "fixture", "workspace", "git", "sessions", "assert", "threshold", "claim"}
SESSION_KEYS = {"prompt"}
ASSERT_KEYS = {"vault_diff", "workspace_diff", "answer", "trace"}
# `workspace_diff` takes the same vocabulary as `vault_diff`. Some claims are
# only visible in the work product: a recorded convention has been applied when
# the file the agent wrote in the repository carries it, not when the agent says
# it remembered.
VAULT_DIFF_KEYS = {
    "unchanged", "created", "not_created", "modified", "not_modified",
    "no_deletions", "max_created", "created_frontmatter",
    "content_matches", "content_not_matches",
}
ANSWER_KEYS = {"session", "matches", "not_matches"}
TRACE_KEYS = {
    "session", "no_vault_access", "vault_reads_include",
    "max_vault_reads", "min_vault_reads", "min_vault_searches",
}

# Tool inputs that name a file. A runner reports tool calls as the agent issued
# them, so this is where a path shows up whatever the tool was called.
PATH_INPUTS = ("file_path", "path", "notebook_path")

READ_TOOLS = {"Read"}
SEARCH_TOOLS = {"Glob", "Grep"}


# --------------------------------------------------------------------------
# Scenarios
# --------------------------------------------------------------------------


def _reject_unknown(where: str, got: object, allowed: set[str]) -> None:
    if not isinstance(got, dict):
        raise ValueError(f"{where}: expected an object, got {type(got).__name__}")
    unknown = sorted(set(got) - allowed)
    if unknown:
        raise ValueError(f"{where}: unknown key(s) {unknown}; allowed: {sorted(allowed)}")


def load_scenario(path: Path) -> dict:
    scenario = json.loads(path.read_text(encoding="utf-8"))
    _reject_unknown(path.name, scenario, SCENARIO_KEYS)

    for required in ("name", "family", "fixture", "sessions", "assert", "threshold", "claim"):
        if required not in scenario:
            raise ValueError(f"{path.name}: missing required key {required!r}")

    if scenario["name"] != path.stem:
        raise ValueError(f"{path.name}: name {scenario['name']!r} does not match the filename")

    sessions = scenario["sessions"]
    if not isinstance(sessions, list) or not sessions:
        raise ValueError(f"{path.name}: sessions must be a non-empty list")
    for i, session in enumerate(sessions):
        _reject_unknown(f"{path.name}: sessions[{i}]", session, SESSION_KEYS)
        if not session.get("prompt"):
            raise ValueError(f"{path.name}: sessions[{i}] has no prompt")

    assertions = scenario["assert"]
    _reject_unknown(f"{path.name}: assert", assertions, ASSERT_KEYS)
    if not assertions:
        raise ValueError(f"{path.name}: assert is empty; a scenario that asserts nothing measures nothing")
    for key, allowed in (("vault_diff", VAULT_DIFF_KEYS), ("workspace_diff", VAULT_DIFF_KEYS),
                         ("answer", ANSWER_KEYS), ("trace", TRACE_KEYS)):
        if key in assertions:
            _reject_unknown(f"{path.name}: assert.{key}", assertions[key], allowed)

    if "workspace_diff" in assertions and not scenario.get("workspace"):
        raise ValueError(f"{path.name}: assert.workspace_diff needs a workspace to look at")

    for key in ("answer", "trace"):
        index = assertions.get(key, {}).get("session")
        if index is not None and not 0 <= index < len(sessions):
            raise ValueError(f"{path.name}: assert.{key}.session {index} is outside the session list")

    fixture = FIXTURE_DIR / scenario["fixture"]
    if not (fixture / "vault").is_dir():
        raise ValueError(f"{path.name}: fixture {scenario['fixture']!r} has no vault/ directory")
    if scenario.get("workspace") and not (fixture / scenario["workspace"]).is_dir():
        raise ValueError(f"{path.name}: fixture has no workspace {scenario['workspace']!r}")

    return scenario


def load_scenarios(names: list[str]) -> list[dict]:
    paths = sorted(SCENARIO_DIR.glob("*.json"))
    if names:
        wanted = set(names)
        paths = [p for p in paths if p.stem in wanted]
        missing = sorted(wanted - {p.stem for p in paths})
        if missing:
            raise SystemExit(f"no such scenario(s): {', '.join(missing)}")
    if not paths:
        raise SystemExit(f"no scenarios found under {SCENARIO_DIR}")
    return [load_scenario(p) for p in paths]


# --------------------------------------------------------------------------
# The vault, before and after
# --------------------------------------------------------------------------


@dataclass
class VaultDiff:
    created: list[str]
    modified: list[str]
    deleted: list[str]

    @property
    def empty(self) -> bool:
        return not (self.created or self.modified or self.deleted)


def snapshot(root: Path) -> dict[str, str]:
    """Every file in the vault, by vault-relative path, with its content hash."""
    out: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        if not path.is_file() or any(part.startswith(".") for part in path.relative_to(root).parts):
            continue
        out[str(path.relative_to(root))] = hashlib.sha256(path.read_bytes()).hexdigest()
    return out


def diff(before: dict[str, str], after: dict[str, str]) -> VaultDiff:
    return VaultDiff(
        created=sorted(set(after) - set(before)),
        modified=sorted(p for p in set(after) & set(before) if before[p] != after[p]),
        deleted=sorted(set(before) - set(after)),
    )


def frontmatter(text: str) -> dict[str, str]:
    """The note's frontmatter as flat `key: value` pairs.

    Not a YAML parser and not trying to be: the standard's frontmatter is flat
    scalars plus two list shapes, and the assertions here only ever ask about a
    scalar. A nested value simply does not match, which is the safe direction.
    """
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return {}
    out: dict[str, str] = {}
    for line in lines[1:]:
        if line.strip() == "---":
            break
        if line.startswith((" ", "\t", "-")) or ":" not in line:
            continue
        key, _, value = line.partition(":")
        out[key.strip()] = value.strip()
    return out


# --------------------------------------------------------------------------
# Running one session against a real agent
# --------------------------------------------------------------------------


@dataclass
class SessionResult:
    answer: str
    cost: float
    tool_calls: list[dict]
    environment: dict
    error: str | None = None


class ClaudeCodeRunner:
    """Drives Claude Code headlessly, one session per `claude -p` process.

    Sessions are separate processes on purpose. A scenario that claims the agent
    will remember a correction has to prove it against a context that never saw
    the correction, and the cheapest honest way to get one is a new process.
    """

    name = "claude-code"
    observes_tools = True

    def __init__(self, kit: Path, model: str, isolation: str, max_turns: int, timeout: int) -> None:
        self.kit = kit
        self.model = model
        self.isolation = isolation
        self.max_turns = max_turns
        self.timeout = timeout
        self._home: Path | None = None

    def describe(self) -> str:
        if self.isolation == "strict":
            return "strict — isolated HOME, only the kit is loaded"
        return "loose — the operator's HOME, user plugins disabled, user skills still load"

    def prepare(self, workdir: Path) -> None:
        if self.isolation != "strict":
            return
        if not os.environ.get("CLAUDE_CODE_OAUTH_TOKEN"):
            raise SystemExit(
                "strict isolation needs CLAUDE_CODE_OAUTH_TOKEN.\n"
                "Mint one with `claude setup-token` and export it, or pass --isolation loose\n"
                "and accept that the operator's own skills load alongside the kit."
            )
        self._home = workdir / "home"
        (self._home / ".claude").mkdir(parents=True, exist_ok=True)

    def _env(self, vault: Path) -> dict[str, str]:
        env = dict(os.environ)
        env["BRAIN_VAULT"] = str(vault)
        if self._home is not None:
            env["HOME"] = str(self._home)
        return env

    def _settings(self) -> str:
        """Settings that suppress what the operator's machine would otherwise add.

        Under strict isolation there is nothing to suppress. Under loose the
        user's enabled plugins are turned off one by one, because `--settings`
        merges with the settings file rather than replacing it: an empty
        `enabledPlugins` object changes nothing, and every plugin's skills stay
        in the prompt competing with the kit's.
        """
        settings: dict[str, object] = {}
        if self.isolation == "loose":
            user = Path(os.path.expanduser("~/.claude/settings.json"))
            enabled = {}
            if user.is_file():
                try:
                    enabled = json.loads(user.read_text(encoding="utf-8")).get("enabledPlugins", {})
                except (json.JSONDecodeError, OSError):
                    enabled = {}
            settings["enabledPlugins"] = {name: False for name in enabled}
        return json.dumps(settings)

    def run(self, prompt: str, cwd: Path, vault: Path, transcript: Path) -> SessionResult:
        cmd = [
            "claude", "-p", prompt,
            "--output-format", "stream-json",
            "--verbose",
            "--model", self.model,
            "--max-turns", str(self.max_turns),
            "--permission-mode", "bypassPermissions",
            "--plugin-dir", str(self.kit),
            "--settings", self._settings(),
            "--mcp-config", json.dumps({"mcpServers": {}}),
            "--strict-mcp-config",
        ]
        try:
            proc = subprocess.run(
                cmd, cwd=cwd, env=self._env(vault),
                capture_output=True, text=True, timeout=self.timeout,
            )
        except subprocess.TimeoutExpired:
            return SessionResult("", 0.0, [], {}, error=f"timed out after {self.timeout}s")

        transcript.write_text(proc.stdout, encoding="utf-8")
        if proc.stderr.strip():
            transcript.with_suffix(".stderr.txt").write_text(proc.stderr, encoding="utf-8")
        return parse_stream(proc.stdout)


def parse_stream(stdout: str) -> SessionResult:
    """The answer, the cost, the tool calls and the loaded environment.

    Kept separate from the subprocess so the offline tests can drive it from a
    recorded stream. An instrument nobody can check without spending money is
    one nobody can argue with when it disagrees with them.
    """
    answer, cost, error = "", 0.0, None
    tool_calls: list[dict] = []
    environment: dict = {}

    for line in stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue

        kind = event.get("type")
        if kind == "system" and event.get("subtype") == "init":
            environment = {
                "model": event.get("model"),
                "skills": event.get("skills") or [],
                "plugins": [p.get("name") for p in (event.get("plugins") or [])],
            }
        elif kind == "assistant":
            for block in event.get("message", {}).get("content", []) or []:
                if isinstance(block, dict) and block.get("type") == "tool_use":
                    tool_calls.append({"name": block.get("name", ""), "input": block.get("input") or {}})
        elif kind == "result":
            answer = event.get("result") or ""
            cost = float(event.get("total_cost_usd") or 0.0)
            if event.get("is_error"):
                error = answer or "the agent reported an error"

    if not answer and error is None:
        error = "the stream carried no result event"
    return SessionResult(answer, cost, tool_calls, environment, error)


# --------------------------------------------------------------------------
# What the tool calls touched
# --------------------------------------------------------------------------


def _input_strings(call: dict) -> list[str]:
    values: list[str] = []
    inputs = call.get("input") or {}
    for key in PATH_INPUTS:
        value = inputs.get(key)
        if isinstance(value, str):
            values.append(value)
    if call.get("name") == "Bash" and isinstance(inputs.get("command"), str):
        values.append(inputs["command"])
    return values


@dataclass
class Trace:
    reads: list[str] = field(default_factory=list)
    searches: int = 0
    touched: bool = False


# A shell command reads, or searches, or neither. Agents reach the vault through
# the shell often enough that ignoring it made the first run of this benchmark
# report an agent as having answered without searching when the transcript shows
# it ran `grep` and `cat` over the whole vault. Counting only typed tool calls
# does not measure less; it measures wrong, and in the direction that turns a
# read budget into an assertion nothing evaluates.
SHELL_READ = re.compile(r"\b(?:cat|head|tail|less|more|bat|sed|awk)\b")
SHELL_SEARCH = re.compile(r"\b(?:grep|rg|ag|find|fd|ls|glob)\b")
SHELL_ASSIGNMENT = re.compile(r"(\w+)=[\"']?([^\"'\s;]+)")


def _shell_paths(command: str, root: str) -> list[str]:
    """Vault-relative paths a shell command names, with its own variables resolved.

    An agent that writes `VAULT="/…"` and then `cat "$VAULT/index.md"` has read
    `index.md`, and a scan for literal paths would see only the assignment.
    """
    resolved = command
    for name, value in SHELL_ASSIGNMENT.findall(command):
        if value.startswith(root):
            resolved = resolved.replace(f"${{{name}}}", value).replace(f"${name}", value)
    escaped = re.escape(root.rstrip("/"))
    found = re.findall(rf"{escaped}/([A-Za-z0-9_./-]+\.md)\b", resolved)
    # An assignment is not a read: drop the occurrence that only named the root.
    return sorted(set(found))


def trace_of(calls: list[dict], vault: Path) -> Trace:
    """Which vault files the agent read, how often it searched, whether it looked at all.

    Shell-mediated access is attributed by inspecting the command, which is
    coarser than reading a typed tool call and is reported as such. The
    alternative — counting it as nothing — is not more conservative, because the
    assertions it silently satisfies are the upper bounds.
    """
    root = str(vault)
    trace = Trace()
    for call in calls:
        strings = _input_strings(call)
        if not any(root in value for value in strings):
            continue
        trace.touched = True
        name = call["name"]
        if name in READ_TOOLS:
            for value in strings:
                if value.startswith(root):
                    trace.reads.append(os.path.relpath(value, root))
        elif name in SEARCH_TOOLS:
            trace.searches += 1
        elif name == "Bash":
            command = (call.get("input") or {}).get("command", "")
            if SHELL_SEARCH.search(command):
                trace.searches += 1
            if SHELL_READ.search(command):
                trace.reads.extend(_shell_paths(command, root))
    return trace


# --------------------------------------------------------------------------
# Assertions
# --------------------------------------------------------------------------


@dataclass
class Check:
    name: str
    status: str  # pass | fail | skip
    detail: str = ""


def _matching(paths: list[str], pattern: str) -> list[str]:
    return [p for p in paths if fnmatch.fnmatch(p, pattern)]


def check_vault_diff(spec: dict, vault_diff: VaultDiff, vault: Path) -> list[Check]:
    checks: list[Check] = []

    def add(name: str, ok: bool, detail: str = "") -> None:
        checks.append(Check(name, "pass" if ok else "fail", "" if ok else detail))

    def seen() -> str:
        return f"created={vault_diff.created} modified={vault_diff.modified} deleted={vault_diff.deleted}"

    if spec.get("unchanged"):
        add("vault unchanged", vault_diff.empty, seen())
    if spec.get("no_deletions"):
        add("nothing deleted", not vault_diff.deleted, f"deleted={vault_diff.deleted}")
    if "max_created" in spec:
        limit = spec["max_created"]
        add(f"at most {limit} file(s) created", len(vault_diff.created) <= limit, seen())

    for pattern in spec.get("created", []):
        add(f"created {pattern}", bool(_matching(vault_diff.created, pattern)), seen())
    for pattern in spec.get("not_created", []):
        add(f"did not create {pattern}", not _matching(vault_diff.created, pattern), seen())
    for pattern in spec.get("modified", []):
        add(f"modified {pattern}", bool(_matching(vault_diff.modified, pattern)), seen())
    for pattern in spec.get("not_modified", []):
        add(f"left {pattern} alone", not _matching(vault_diff.modified, pattern), seen())

    for pattern, expected in (spec.get("created_frontmatter") or {}).items():
        hits = _matching(vault_diff.created, pattern)
        ok = False
        for relative in hits:
            fields = frontmatter((vault / relative).read_text(encoding="utf-8"))
            if all(fields.get(k) == v for k, v in expected.items()):
                ok = True
                break
        add(f"frontmatter of {pattern} is {expected}", ok, f"candidates={hits}")

    for pattern, patterns in (spec.get("content_matches") or {}).items():
        hits = _matching(vault_diff.created + vault_diff.modified, pattern)
        ok = False
        for relative in hits:
            text = (vault / relative).read_text(encoding="utf-8", errors="replace")
            if all(re.search(p, text) for p in patterns):
                ok = True
                break
        add(f"{pattern} contains {patterns}", ok, f"candidates={hits}")

    for pattern, patterns in (spec.get("content_not_matches") or {}).items():
        hits = _matching(vault_diff.created + vault_diff.modified, pattern)
        offenders = [
            relative for relative in hits
            if any(re.search(p, (vault / relative).read_text(encoding="utf-8", errors="replace"))
                   for p in patterns)
        ]
        add(f"{pattern} avoids {patterns}", not offenders, f"offenders={offenders}")

    return checks


def check_answer(spec: dict, answer: str) -> list[Check]:
    checks: list[Check] = []
    excerpt = (answer[:400] + "…") if len(answer) > 400 else answer
    for pattern in spec.get("matches", []):
        ok = bool(re.search(pattern, answer))
        checks.append(Check(f"answer matches /{pattern}/", "pass" if ok else "fail", "" if ok else excerpt))
    for pattern in spec.get("not_matches", []):
        ok = not re.search(pattern, answer)
        checks.append(Check(f"answer avoids /{pattern}/", "pass" if ok else "fail", "" if ok else excerpt))
    return checks


def check_trace(spec: dict, trace: Trace | None) -> list[Check]:
    names: list[str] = []
    if spec.get("no_vault_access"):
        names.append("agent left the vault alone")
    for wanted in spec.get("vault_reads_include", []):
        names.append(f"agent read {wanted}")
    if "max_vault_reads" in spec:
        names.append(f"at most {spec['max_vault_reads']} vault read(s)")
    if "min_vault_reads" in spec:
        names.append(f"at least {spec['min_vault_reads']} vault read(s)")
    if "min_vault_searches" in spec:
        names.append(f"at least {spec['min_vault_searches']} vault search(es)")

    if trace is None:
        # Skipped, never passed: a runner that cannot see tool calls must not
        # outscore one that can by being unable to fail.
        return [Check(name, "skip", "this runner does not observe tool calls") for name in names]

    checks: list[Check] = []
    seen = f"reads={trace.reads} searches={trace.searches} touched={trace.touched}"
    if spec.get("no_vault_access"):
        ok = not trace.touched
        checks.append(Check("agent left the vault alone", "pass" if ok else "fail", "" if ok else seen))
    for wanted in spec.get("vault_reads_include", []):
        ok = any(fnmatch.fnmatch(r, wanted) or r.endswith(wanted) for r in trace.reads)
        checks.append(Check(f"agent read {wanted}", "pass" if ok else "fail", "" if ok else seen))
    if "max_vault_reads" in spec:
        ok = len(trace.reads) <= spec["max_vault_reads"]
        checks.append(Check(f"at most {spec['max_vault_reads']} vault read(s)", "pass" if ok else "fail", "" if ok else seen))
    if "min_vault_reads" in spec:
        ok = len(trace.reads) >= spec["min_vault_reads"]
        checks.append(Check(f"at least {spec['min_vault_reads']} vault read(s)", "pass" if ok else "fail", "" if ok else seen))
    if "min_vault_searches" in spec:
        ok = trace.searches >= spec["min_vault_searches"]
        checks.append(Check(f"at least {spec['min_vault_searches']} vault search(es)", "pass" if ok else "fail", "" if ok else seen))
    return checks


def evaluate(scenario: dict, vault: Path, vault_diff: VaultDiff, sessions: list[SessionResult],
             traces: list[Trace] | None,
             workspace: Path | None = None, workspace_diff: VaultDiff | None = None) -> list[Check]:
    spec = scenario["assert"]
    checks: list[Check] = []
    if "vault_diff" in spec:
        checks += check_vault_diff(spec["vault_diff"], vault_diff, vault)
    if "workspace_diff" in spec and workspace is not None and workspace_diff is not None:
        checks += [
            Check(f"workspace: {c.name}", c.status, c.detail)
            for c in check_vault_diff(spec["workspace_diff"], workspace_diff, workspace)
        ]
    if "answer" in spec:
        index = spec["answer"].get("session", len(sessions) - 1)
        checks += check_answer(spec["answer"], sessions[index].answer)
    if "trace" in spec:
        index = spec["trace"].get("session", len(sessions) - 1)
        checks += check_trace(spec["trace"], traces[index] if traces is not None else None)
    return checks


# --------------------------------------------------------------------------
# Trials
# --------------------------------------------------------------------------


@dataclass
class Trial:
    scenario: str
    index: int
    checks: list[Check]
    cost: float
    error: str | None
    workdir: Path
    environment: dict = field(default_factory=dict)

    @property
    def passed(self) -> bool:
        return self.error is None and all(c.status != "fail" for c in self.checks) and bool(self.checks)


def _seed_git(workspace: Path) -> None:
    """A workspace with history, so `git diff` can answer "what changed".

    The fixture holds the *changed* tree. A `.evals-baseline/` directory beside
    it, when present, holds the earlier version of whatever the prompt talks
    about: those files are committed first and the changed versions are put back
    afterwards, so the uncommitted diff is exactly the change under discussion.

    Identity and config are pinned to throwaway values because the point is a
    diff the agent can read, not an entry in anyone's real history.
    """
    env = {
        **os.environ,
        "GIT_AUTHOR_NAME": "Brain Kit evals", "GIT_AUTHOR_EMAIL": "evals@example.invalid",
        "GIT_COMMITTER_NAME": "Brain Kit evals", "GIT_COMMITTER_EMAIL": "evals@example.invalid",
        "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull,
    }

    def git(*args: str) -> None:
        subprocess.run(["git", "-C", str(workspace), *args], env=env,
                       check=True, capture_output=True, text=True)

    baseline = workspace / ".evals-baseline"
    changed: dict[Path, bytes] = {}
    if baseline.is_dir():
        for source in sorted(p for p in baseline.rglob("*") if p.is_file()):
            target = workspace / source.relative_to(baseline)
            if target.is_file():
                changed[target] = target.read_bytes()
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
        shutil.rmtree(baseline)

    git("init", "-q", "-b", "main")
    git("add", "-A")
    git("commit", "-q", "-m", "baseline")

    for target, content in changed.items():
        target.write_bytes(content)


def replay_trial(scenario: dict, index: int, workdir: Path) -> Trial | None:
    """Score a finished trial again from what it left on disk.

    Assertions get corrected more often than agents change their behaviour, and
    the first correction to this instrument came *from* a run — a trace that
    counted only typed tool calls reported an agent as not having searched while
    its transcript showed it grepping the vault through the shell. Re-scoring a
    recorded run is how that fix gets applied to the evidence that produced it,
    instead of being paid for twice and compared across two different samples.

    The pre-run state is the fixture: nothing else wrote to these directories.
    """
    streams = sorted(workdir.glob("session-*.stream.jsonl"))
    if len(streams) != len(scenario["sessions"]) or not (workdir / "vault").is_dir():
        return None

    fixture = FIXTURE_DIR / scenario["fixture"]
    vault = workdir / "vault"
    sessions = [parse_stream(p.read_text(encoding="utf-8")) for p in streams]
    traces = [trace_of(s.tool_calls, vault) for s in sessions]
    cost = sum(s.cost for s in sessions)
    environment = next((s.environment for s in sessions if s.environment), {})

    error = next((f"session {n + 1}: {s.error}" for n, s in enumerate(sessions) if s.error), None)
    if error is not None:
        return Trial(scenario["name"], index, [], cost, error, workdir, environment)

    workspace: Path | None = None
    workspace_diff: VaultDiff | None = None
    if scenario.get("workspace"):
        workspace = workdir / scenario["workspace"]
        workspace_diff = diff(snapshot(fixture / scenario["workspace"]), snapshot(workspace))

    checks = evaluate(
        scenario, vault, diff(snapshot(fixture / "vault"), snapshot(vault)),
        sessions, traces, workspace, workspace_diff,
    )
    return Trial(scenario["name"], index, checks, cost, None, workdir, environment)


def run_trial(scenario: dict, index: int, runner: ClaudeCodeRunner, run_root: Path) -> Trial:
    workdir = run_root / scenario["name"] / f"trial-{index + 1}"
    workdir.mkdir(parents=True, exist_ok=True)

    fixture = FIXTURE_DIR / scenario["fixture"]
    vault = workdir / "vault"
    shutil.copytree(fixture / "vault", vault)

    cwd = vault
    workspace: Path | None = None
    if scenario.get("workspace"):
        cwd = workspace = workdir / scenario["workspace"]
        shutil.copytree(fixture / scenario["workspace"], workspace)
        if scenario.get("git"):
            _seed_git(workspace)
        else:
            # Without history there is nothing to seed, and leaving the earlier
            # versions lying around would put a second copy of every file in
            # front of the agent.
            shutil.rmtree(workspace / ".evals-baseline", ignore_errors=True)

    before = snapshot(vault)
    workspace_before = snapshot(workspace) if workspace is not None else {}
    sessions: list[SessionResult] = []
    traces: list[Trace] = []
    cost = 0.0
    error: str | None = None
    environment: dict = {}

    for n, session in enumerate(scenario["sessions"]):
        transcript = workdir / f"session-{n + 1}.stream.jsonl"
        result = runner.run(session["prompt"], cwd, vault, transcript)
        sessions.append(result)
        traces.append(trace_of(result.tool_calls, vault))
        cost += result.cost
        environment = environment or result.environment
        if result.error:
            error = f"session {n + 1}: {result.error}"
            break

    if error is not None:
        return Trial(scenario["name"], index, [], cost, error, workdir, environment)

    checks = evaluate(
        scenario, vault, diff(before, snapshot(vault)), sessions,
        traces if runner.observes_tools else None,
        workspace, diff(workspace_before, snapshot(workspace)) if workspace is not None else None,
    )
    return Trial(scenario["name"], index, checks, cost, None, workdir, environment)


# --------------------------------------------------------------------------
# Reporting
# --------------------------------------------------------------------------


def summarise(scenario: dict, trials: list[Trial]) -> dict:
    """One scenario's result, with enough of each failure to argue about it.

    A count alone sends the reader back to the transcripts. The first failing
    observation is kept alongside it so the report can answer, on its own,
    whether a miss was the kit behaving badly or the fixture failing to leave a
    trace the assertion could see.
    """
    passed = sum(1 for t in trials if t.passed)
    failures: dict[str, dict] = {}

    def note(name: str, detail: str) -> None:
        entry = failures.setdefault(name, {"count": 0, "detail": detail})
        entry["count"] += 1
        if not entry["detail"]:
            entry["detail"] = detail

    for trial in trials:
        if trial.error:
            note("the run did not complete", trial.error)
        for check in trial.checks:
            if check.status == "fail":
                note(check.name, check.detail)
    skipped = sorted({c.name for t in trials for c in t.checks if c.status == "skip"})
    return {
        "name": scenario["name"],
        "family": scenario["family"],
        "claim": scenario["claim"],
        "threshold": scenario["threshold"],
        "trials": len(trials),
        "passed": passed,
        "rate": passed / len(trials) if trials else 0.0,
        "met": (passed / len(trials) if trials else 0.0) >= scenario["threshold"],
        "cost": round(sum(t.cost for t in trials), 4),
        "failures": dict(sorted(failures.items(), key=lambda kv: -kv[1]["count"])),
        "skipped": skipped,
        "example": next((str(t.workdir) for t in trials if not t.passed), ""),
    }


def _one_line(text: str, limit: int = 220) -> str:
    """A failure detail, flattened so it cannot break the table or the fences."""
    flat = " ".join(text.split()).replace("`", "'")
    return flat if len(flat) <= limit else flat[: limit - 1] + "…"


def render(summaries: list[dict], meta: dict, baseline: dict | None) -> str:
    lines: list[str] = []
    lines.append(f"# Brain Kit benchmark — {meta['label']}")
    lines.append("")
    lines.append(f"- Date: {meta['date']}")
    lines.append(f"- Model: `{meta['model']}`")
    lines.append(f"- Runner: `{meta['runner']}`, isolation {meta['isolation']}")
    lines.append(f"- Kit hooks: {'installed' if meta['hooks'] else 'removed for this run'}")
    lines.append(f"- Trials per scenario: {meta['trials']}")
    lines.append(f"- Cost: ${meta['cost']:.2f}")
    lines.append("")
    lines.append(
        "A rate is a fraction of trials, not a probability. "
        f"At {meta['trials']} trials per scenario this separates working from broken; "
        "it does not separate 95% from 99%, and nothing here claims it does."
    )
    lines.append("")

    if meta.get("skills"):
        lines.append(f"The agent loaded {len(meta['skills'])} skills: " +
                     ", ".join(f"`{s}`" for s in meta["skills"]) + ".")
        lines.append("")

    header = "| Scenario | Family | Rate | Bar |"
    divider = "|---|---|---|---|"
    if baseline:
        header += " Baseline |"
        divider += "---|"
    lines.append(header)
    lines.append(divider)
    for s in summaries:
        row = (f"| [`{s['name']}`](#{s['name']}) | {s['family']} | "
               f"{s['passed']}/{s['trials']} | {'met' if s['met'] else '**missed**'} |")
        if baseline:
            prior = baseline.get(s["name"])
            row += f" {prior['passed']}/{prior['trials']} |" if prior else " — |"
        lines.append(row)
    lines.append("")

    total_pass = sum(s["passed"] for s in summaries)
    total_trials = sum(s["trials"] for s in summaries)
    lines.append(f"Overall: **{total_pass}/{total_trials}** trials passed across "
                 f"{len(summaries)} scenarios.")
    lines.append("")

    # A total is the least informative number here — a suite can score well by
    # passing every scenario that asks the agent to do nothing. Whoever ran this
    # says what it means, in the report, where the numbers are.
    if meta.get("note"):
        lines.append("## Reading this run")
        lines.append("")
        lines.append(meta["note"].strip())
        lines.append("")

    for s in summaries:
        lines.append(f"## {s['name']}")
        lines.append("")
        lines.append(f"{s['claim']}")
        lines.append("")
        lines.append(f"- Rate: {s['passed']}/{s['trials']} (bar: {s['threshold']:.0%})")
        if s["skipped"]:
            lines.append(f"- Skipped, not passed: {', '.join(f'`{n}`' for n in s['skipped'])}")
        if s["failures"]:
            lines.append("- Failed assertions, by how often:")
            for name, entry in s["failures"].items():
                lines.append(f"  - `{name}` — {entry['count']}/{s['trials']}")
                if entry["detail"]:
                    lines.append(f"    - first seen: `{_one_line(entry['detail'])}`")
        else:
            lines.append("- No assertion failed.")
        lines.append("")

    return "\n".join(lines) + "\n"


# --------------------------------------------------------------------------
# Entry point
# --------------------------------------------------------------------------


def stage_kit(destination: Path, hooks: bool) -> Path:
    """A copy of the kit, so a run measures a stated variant rather than the tree.

    `evals/` is left out, and that is not tidiness. The agent under test can read
    any file it is pointed at, and the scenarios are the answer key: a subject
    holding its own marking scheme is not being measured.

    `examples/` stays, because `STANDARD.md` links into it and a skill that
    follows one of those links must find the file.

    Removing `hooks/` from the copy is what makes the pre-hook measurement
    repeatable after the hook exists. A baseline that can only be taken before a
    change can never be re-taken when someone doubts it.
    """
    staged = destination / "kit"
    shutil.copytree(KIT, staged, ignore=shutil.ignore_patterns("evals"))
    if not hooks:
        shutil.rmtree(staged / "hooks", ignore_errors=True)
    return staged


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--trials", type=int, default=5, help="trials per scenario (default: 5)")
    parser.add_argument("--model", default="sonnet", help="model to measure (default: sonnet)")
    parser.add_argument("--scenario", action="append", default=[], help="run only this scenario; repeatable")
    parser.add_argument("--family", default="", help="run only scenarios in this family")
    parser.add_argument("--isolation", choices=("strict", "loose"), default="strict",
                        help="strict needs CLAUDE_CODE_OAUTH_TOKEN and loads only the kit (default)")
    parser.add_argument("--no-hooks", action="store_true", help="remove the kit's hooks before measuring")
    parser.add_argument("--jobs", type=int, default=3, help="trials to run concurrently (default: 3)")
    parser.add_argument("--max-turns", type=int, default=40)
    parser.add_argument("--timeout", type=int, default=600, help="seconds per session")
    parser.add_argument("--label", default="", help="report label, e.g. baseline")
    parser.add_argument("--note", default="",
                        help="what this run means, or a path to a Markdown file saying so; "
                             "it becomes the report's 'Reading this run' section")
    parser.add_argument("--out", default="", help="report path (default: brainkit/evals/results/<date>-<label>.md)")
    parser.add_argument("--baseline", default="", help="a previous run's .json, to add a comparison column")
    parser.add_argument("--dry-run", action="store_true", help="validate scenarios and print the plan; spend nothing")
    parser.add_argument("--replay", default="",
                        help="score a finished run's directory again, with today's assertions; spend nothing")
    args = parser.parse_args()

    try:
        scenarios = load_scenarios(args.scenario)
    except ValueError as exc:
        print(f"invalid scenario: {exc}", file=sys.stderr)
        return 2
    if args.family:
        scenarios = [s for s in scenarios if s["family"] == args.family]
        if not scenarios:
            print(f"no scenarios in family {args.family!r}", file=sys.stderr)
            return 2

    label = args.label or ("baseline" if args.no_hooks else "run")
    date = datetime.date.today().isoformat()

    if args.dry_run:
        sessions = sum(len(s["sessions"]) for s in scenarios) * args.trials
        print(f"{len(scenarios)} scenario(s), {args.trials} trial(s) each = {sessions} agent sessions")
        for s in scenarios:
            print(f"  {s['name']:<34} {s['family']:<12} bar {s['threshold']:.0%}  {len(s['sessions'])} session(s)")
        return 0

    results: dict[str, list[Trial]] = {s["name"]: [] for s in scenarios}
    runner_name, isolation = "claude-code", args.isolation

    if args.replay:
        run_root = Path(args.replay)
        # What the run was configured as is read back from the run itself. A
        # replay that took the current flags would happily label a hooks-off
        # sample as a hooks-on one, and a report whose header contradicts its
        # numbers is worse than no report.
        recorded = {}
        if (run_root / "run.json").is_file():
            recorded = json.loads((run_root / "run.json").read_text(encoding="utf-8"))
        args.no_hooks = recorded.get("no_hooks", args.no_hooks)
        args.model = recorded.get("model", args.model)
        print(f"replaying: {run_root}")
        if not recorded:
            print("  no run.json here — hooks and model are taken from the flags you passed")
        print(flush=True)
        for scenario in scenarios:
            for workdir in sorted((run_root / scenario["name"]).glob("trial-*")):
                index = int(workdir.name.split("-")[-1]) - 1
                trial = replay_trial(scenario, index, workdir)
                if trial is None:
                    print(f"  skip {scenario['name']} {workdir.name} (incomplete on disk)", flush=True)
                    continue
                results[scenario["name"]].append(trial)
                print(f"  {'pass' if trial.passed else 'FAIL':<4} {scenario['name']} trial {index + 1}",
                      flush=True)
        scenarios = [s for s in scenarios if results[s["name"]]]
        if not scenarios:
            print(f"nothing to replay under {run_root}", file=sys.stderr)
            return 2
        args.trials = max(len(v) for v in results.values() if v)
        isolation = "replayed from a recorded run"
    else:
        run_root = Path(tempfile.mkdtemp(prefix=f"torio-brain-evals-{date}-"))
        runner = ClaudeCodeRunner(
            kit=stage_kit(run_root, hooks=not args.no_hooks),
            model=args.model, isolation=args.isolation,
            max_turns=args.max_turns, timeout=args.timeout,
        )
        runner.prepare(run_root)
        runner_name, isolation = runner.name, runner.describe()
        (run_root / "run.json").write_text(
            json.dumps({"no_hooks": args.no_hooks, "model": args.model,
                        "isolation": args.isolation, "trials": args.trials}, indent=2) + "\n",
            encoding="utf-8")

        print(f"workdir: {run_root}")
        print(f"runner:  {runner.name}, {runner.describe()}")
        print(f"running: {len(scenarios)} scenario(s) × {args.trials} trial(s)\n", flush=True)

        work = [(s, i) for s in scenarios for i in range(args.trials)]
        with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, args.jobs)) as pool:
            futures = {pool.submit(run_trial, s, i, runner, run_root): (s, i) for s, i in work}
            for done in concurrent.futures.as_completed(futures):
                scenario, index = futures[done]
                trial = done.result()
                results[scenario["name"]].append(trial)
                mark = "pass" if trial.passed else "FAIL"
                note = f" ({trial.error})" if trial.error else ""
                print(f"  {mark:<4} {scenario['name']} trial {index + 1}{note}", flush=True)

    summaries = [summarise(s, sorted(results[s["name"]], key=lambda t: t.index)) for s in scenarios]
    environment = next((t.environment for ts in results.values() for t in ts if t.environment), {})
    meta = {
        "label": label,
        "date": date,
        "model": environment.get("model") or args.model,
        "runner": runner_name,
        "isolation": isolation,
        "hooks": not args.no_hooks,
        "trials": args.trials,
        "cost": sum(s["cost"] for s in summaries),
        "skills": environment.get("skills") or [],
        "note": Path(args.note).read_text(encoding="utf-8") if args.note and Path(args.note).is_file()
                else args.note,
    }

    baseline = None
    if args.baseline:
        baseline = {s["name"]: s for s in json.loads(Path(args.baseline).read_text(encoding="utf-8"))["scenarios"]}

    out = Path(args.out) if args.out else RESULT_DIR / f"{date}-{label}.md"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(render(summaries, meta, baseline), encoding="utf-8")
    out.with_suffix(".json").write_text(
        json.dumps({"meta": meta, "scenarios": summaries}, indent=2) + "\n", encoding="utf-8")

    missed = [s["name"] for s in summaries if not s["met"]]
    print(f"\nreport: {out}")
    print(f"cost:   ${meta['cost']:.2f}")
    if missed:
        print(f"missed the bar: {', '.join(missed)}")
    return 1 if missed else 0


if __name__ == "__main__":
    raise SystemExit(main())
