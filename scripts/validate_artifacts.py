#!/usr/bin/env python3
"""Portable validation for the Torio documentation surface.

Uses only Python's standard library so it can run before Go or project dependencies
are installed. It validates relative Markdown links and obvious secret material.

Scope note: this script used to also check a fixed list of required artifacts and
a JSON Schema subset. Both belonged to the pre-V1 exploration removed by ADR-0017;
nothing in the delivered product reads `schemas/`, and the required-file list only
pinned files that no longer exist.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

SECRET_PATTERNS = {
    "OpenAI-like API key": re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b"),
    "GitHub token": re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}\b"),
    "Slack token": re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{10,}\b"),
    "AWS access key": re.compile(r"\bAKIA[0-9A-Z]{16}\b"),
    "private key": re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
}

MARKDOWN_LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")


def validate_links() -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.rglob("*.md")):
        # Runtime worktrees/state are ignored by git and must not affect design validation.
        if any(part in {".worktrees", "state", "artifacts"} for part in path.parts):
            continue
        # Site page sources render to site/*.html; their links are resolved
        # against the built site, not the repository tree. scripts/check_site_links.py
        # validates them in the generated output instead.
        if path.parent == ROOT / "docs" / "content" / "pages":
            continue
        text = path.read_text(encoding="utf-8")
        for target in MARKDOWN_LINK.findall(text):
            target = target.strip().split()[0].strip("<>\"'")
            if not target or target.startswith(("http://", "https://", "mailto:", "#")):
                continue
            clean = target.split("#", 1)[0]
            if not clean:
                continue
            resolved = (path.parent / clean).resolve()
            try:
                resolved.relative_to(ROOT.resolve())
            except ValueError:
                errors.append(f"{path.relative_to(ROOT)}: link escapes repository: {target}")
                continue
            if not resolved.exists():
                errors.append(f"{path.relative_to(ROOT)}: broken relative link: {target}")
    return errors


def validate_secrets() -> list[str]:
    errors: list[str] = []
    text_extensions = {".md", ".json", ".yaml", ".yml", ".tmpl", ".mod", ".txt"}
    explicit_names = {"Makefile", ".tool-versions", ".gitignore"}
    for path in sorted(ROOT.rglob("*")):
        if not path.is_file() or (path.suffix not in text_extensions and path.name not in explicit_names):
            continue
        if ".git" in path.parts:
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        for name, pattern in SECRET_PATTERNS.items():
            if pattern.search(text):
                errors.append(f"{path.relative_to(ROOT)}: possible {name}")
    return errors


def main() -> int:
    checks = [
        ("relative Markdown links", validate_links),
        ("secret patterns", validate_secrets),
    ]
    errors: list[str] = []
    for name, check in checks:
        result = check()
        if result:
            print(f"FAIL  {name}: {len(result)} issue(s)")
            errors.extend(result)
        else:
            print(f"PASS  {name}")

    if errors:
        print("\nValidation errors:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print("\nTorio documentation surface is internally consistent.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
