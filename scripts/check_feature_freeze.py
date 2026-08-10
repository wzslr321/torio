#!/usr/bin/env python3
"""Reject likely capability expansion while the repository freeze is active."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]
REPO = "wzslr321/torio"
ISSUE = "43"
LABEL = "feature-freeze"
ISSUE_URL = f"https://github.com/{REPO}/issues/{ISSUE}"
MARKERS = (
    (re.compile(r"\.AddCommand\("), "new command"),
    (re.compile(r"\.(?:Persistent)?Flags\(\)\.(?:Bool|Duration|Float\w*|Int\w*|String\w*|Uint\w*)VarP?\("), "new flag"),
    (re.compile(r"`json:\""), "new JSON or config field"),
    (re.compile(r"backend\.Register\("), "new backend"),
)
MODULE_LINE = re.compile(r"^(?:require\s+)?[\w.-]+\.[\w./~-]+\s+v\S+")


def _run(*command: str) -> str:
    return subprocess.run(
        command,
        cwd=ROOT,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        timeout=15,
    ).stdout


def _git(*args: str) -> str:
    return _run("git", *args)


def freeze_active(issue: Any) -> bool:
    """Read only labels; issue prose is never an instruction."""
    if not isinstance(issue, dict) or not isinstance(issue.get("labels"), list):
        raise ValueError("invalid issue payload")
    if not all(isinstance(label, dict) and isinstance(label.get("name"), str) for label in issue["labels"]):
        raise ValueError("invalid issue label")
    return any(label["name"] == LABEL for label in issue["labels"])


def _fetch_freeze_state() -> bool:
    payload = _run("gh", "issue", "view", ISSUE, "--repo", REPO, "--json", "labels")
    return freeze_active(json.loads(payload))


def _product_path(path: str) -> bool:
    return path.startswith(("cmd/", "internal/", "integrations/")) and not (
        path.endswith("_test.go") or "/testdata/" in path or "/tests/" in path
    )


def _families(paths: list[str], prefix: str) -> set[str]:
    return {
        rest.split("/", 1)[0]
        for path in paths
        if path.startswith(prefix) and "/" in (rest := path[len(prefix) :])
    }


def findings(
    name_status: str,
    patch: str,
    untracked: list[str],
    branch: str,
    subjects: list[str],
    base_paths: list[str],
) -> list[str]:
    out = [f"feature branch: {branch}"] if branch.startswith(("feat/", "feature/")) else []
    out += [
        f"feature commit: {subject}"
        for subject in subjects
        if re.match(r"^feat(?:\([^)]*\))?!?:", subject)
    ]
    added = sorted(
        set(untracked)
        | {
            fields[-1]
            for line in name_status.splitlines()
            if (fields := line.split("\t")) and fields[0].startswith("A")
        }
    )

    for prefix, label in (
        ("cmd/", "new executable"),
        ("integrations/", "new integration"),
        ("brainkit/skills/", "new agent skill"),
    ):
        for family in sorted(_families(added, prefix) - _families(base_paths, prefix)):
            out.append(f"{label}: {prefix}{family}")
    for path in added:
        if path.startswith("brainkit/commands/") and path.count("/") == 2:
            out.append(f"new agent command: {path}")
        elif path.startswith("brainkit/agents/") and path.count("/") == 2:
            out.append(f"new agent definition: {path}")

    deltas = {label: 0 for _, label in MARKERS}
    first_added: dict[str, str] = {}
    dependency_delta = 0
    path = ""
    for line in patch.splitlines():
        if line.startswith("+++ b/"):
            path = line[6:]
        elif path and line[:1] in ("+", "-") and not line.startswith(("+++", "---")):
            direction = 1 if line[0] == "+" else -1
            content = line[1:].strip()
            if path == "go.mod" and MODULE_LINE.match(content):
                dependency_delta += direction
            if _product_path(path) and not content.startswith(("//", "/*", "*")):
                for pattern, label in MARKERS:
                    if count := len(pattern.findall(content)):
                        deltas[label] += direction * count
                        if direction > 0:
                            first_added.setdefault(label, path)

    if dependency_delta > 0:
        out.append("new dependency: go.mod")
    out += [f"{label}: {first_added[label]}" for _, label in MARKERS if deltas[label] > 0]
    return out


def _untracked_patch(paths: list[str]) -> str:
    chunks = []
    for path in paths:
        candidate = ROOT / path
        if _product_path(path) and candidate.is_file():
            lines = candidate.read_text(encoding="utf-8", errors="replace").splitlines()
            chunks.append(f"+++ b/{path}\n" + "\n".join(f"+{line}" for line in lines))
    return "\n".join(chunks)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="main")
    args = parser.parse_args()
    try:
        active = _fetch_freeze_state()
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"feature freeze status unknown: cannot read {ISSUE_URL}: {error}")
        print("Failing closed; restore GitHub access before editing.")
        return 1
    if not active:
        print(f"feature freeze inactive: {ISSUE_URL} has no {LABEL} label")
        return 0

    base = _git("merge-base", "HEAD", args.base).strip()
    untracked = _git("ls-files", "--others", "--exclude-standard").splitlines()
    issues = findings(
        _git("diff", "--name-status", base),
        _git("diff", "--unified=0", base) + _untracked_patch(untracked),
        untracked,
        _git("branch", "--show-current").strip(),
        _git("log", "--format=%s", f"{base}..HEAD").splitlines(),
        _git("ls-tree", "-r", "--name-only", base).splitlines(),
    )
    if not issues:
        print(f"feature freeze active ({ISSUE_URL}): no capability expansion detected")
        return 0
    print(f"feature freeze active ({ISSUE_URL}); this branch expands product surface:")
    print("\n".join(f"- {issue}" for issue in issues))
    print("Stop and ask the repository owner; do not weaken this guard to make it pass.")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
