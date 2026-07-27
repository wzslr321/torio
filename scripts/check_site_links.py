#!/usr/bin/env python3
"""Dependency-free link/anchor check for the static Torio docs site.

Standard library only (matches scripts/validate_artifacts.py). For every HTML
file under site/ it verifies that:

  * local href/src targets (not http(s):, mailto:, or bare #fragments handled
    separately) resolve to an existing file inside site/;
  * every #fragment target (same-page or cross-page) points at an element whose
    id exists in the referenced file.

External (http/https/mailto) links are intentionally not fetched.
Exit 0 = clean, 1 = problems found.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

SITE = Path(__file__).resolve().parents[1] / "site"

ATTR = re.compile(r'(?:href|src)\s*=\s*"([^"]*)"', re.IGNORECASE)
ID = re.compile(r'\bid\s*=\s*"([^"]+)"', re.IGNORECASE)


def ids_in(path: Path) -> set[str]:
    return set(ID.findall(path.read_text(encoding="utf-8")))


def main() -> int:
    if not SITE.is_dir():
        print(f"no site directory at {SITE}", file=sys.stderr)
        return 1

    html_files = sorted(SITE.rglob("*.html"))
    if not html_files:
        print("no HTML files found under site/", file=sys.stderr)
        return 1

    id_cache: dict[Path, set[str]] = {}
    errors: list[str] = []

    for path in html_files:
        text = path.read_text(encoding="utf-8")
        rel = path.relative_to(SITE)
        for target in ATTR.findall(text):
            target = target.strip()
            if not target or target.startswith(
                ("http://", "https://", "mailto:", "data:")
            ):
                continue

            file_part, _, fragment = target.partition("#")

            if file_part:
                resolved = (path.parent / file_part).resolve()
                try:
                    resolved.relative_to(SITE.resolve())
                except ValueError:
                    errors.append(f"{rel}: link escapes site/: {target}")
                    continue
                if not resolved.exists():
                    errors.append(f"{rel}: broken local link: {target}")
                    continue
            else:
                resolved = path  # same-page fragment

            if fragment:
                if resolved not in id_cache:
                    id_cache[resolved] = (
                        ids_in(resolved) if resolved.suffix == ".html" else set()
                    )
                if fragment not in id_cache[resolved]:
                    errors.append(f"{rel}: missing anchor #{fragment} in {target}")

    if errors:
        print("site link check FAILED:")
        for e in errors:
            print(f"  - {e}")
        return 1

    print(f"site link check OK: {len(html_files)} HTML files, all local links and anchors resolve")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
