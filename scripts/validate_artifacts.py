#!/usr/bin/env python3
"""Portable validation for the Torio design/implementation pack.

Uses only Python's standard library so it can run before Go or project dependencies
are installed. It validates the JSON Schema subset used by this repository,
relative Markdown links, required artifacts, and obvious secret material.
"""

from __future__ import annotations

import json
import re
import sys
import uuid
from datetime import datetime
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]

REQUIRED = [
    "README.md",
    "AGENTS.md",
    "SECURITY.md",
    "docs/01-product-brief.md",
    "docs/02-scope.md",
    "docs/03-architecture.md",
    "docs/04-threat-model.md",
    "docs/05-responsibilities.md",
    "docs/07-source-verification.md",
    "docs/11-testing-strategy.md",
    "docs/13-requirements-traceability.md",
    "docs/contracts/cli.md",
    "docs/contracts/effective-policy.md",
    "docs/plans/01-spike.md",
    "docs/plans/02-demo-a.md",
    "docs/plans/03-demo-b.md",
    "docs/adr/0006-content-addressed-approval.md",
    "schemas/project.schema.json",
    "schemas/task-request.schema.json",
    "schemas/effective-policy.schema.json",
    "schemas/review-evidence.schema.json",
    "prompts/00-implementer-system.md",
    "prompts/01-spike.md",
    ".hermes/plans/2026-07-23_172055-hermes-box.md",
]

EXAMPLES = {
    "examples/project.json": "schemas/project.schema.json",
    "examples/task-request.json": "schemas/task-request.schema.json",
    "examples/effective-policy.json": "schemas/effective-policy.schema.json",
    "examples/review-evidence.json": "schemas/review-evidence.schema.json",
}

SECRET_PATTERNS = {
    "OpenAI-like API key": re.compile(r"\bsk-[A-Za-z0-9_-]{20,}\b"),
    "GitHub token": re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}\b"),
    "Slack token": re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{10,}\b"),
    "AWS access key": re.compile(r"\bAKIA[0-9A-Z]{16}\b"),
    "private key": re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
}

MARKDOWN_LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")


def is_type(value: Any, expected: str) -> bool:
    if expected == "object":
        return isinstance(value, dict)
    if expected == "array":
        return isinstance(value, list)
    if expected == "string":
        return isinstance(value, str)
    if expected == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if expected == "boolean":
        return isinstance(value, bool)
    if expected == "null":
        return value is None
    return True


def validate_schema(value: Any, schema: dict[str, Any], where: str) -> list[str]:
    errors: list[str] = []

    declared_type = schema.get("type")
    if declared_type is not None:
        types = declared_type if isinstance(declared_type, list) else [declared_type]
        if not any(is_type(value, item) for item in types):
            return [f"{where}: expected type {types}, got {type(value).__name__}"]

    if "const" in schema and value != schema["const"]:
        errors.append(f"{where}: expected constant {schema['const']!r}, got {value!r}")
    if "enum" in schema and value not in schema["enum"]:
        errors.append(f"{where}: {value!r} not in enum {schema['enum']!r}")

    if isinstance(value, str):
        if "minLength" in schema and len(value) < schema["minLength"]:
            errors.append(f"{where}: string shorter than {schema['minLength']}")
        if "maxLength" in schema and len(value) > schema["maxLength"]:
            errors.append(f"{where}: string longer than {schema['maxLength']}")
        if "pattern" in schema and re.search(schema["pattern"], value) is None:
            errors.append(f"{where}: value does not match {schema['pattern']!r}")
        if schema.get("format") == "uuid":
            try:
                uuid.UUID(value)
            except ValueError:
                errors.append(f"{where}: invalid UUID")
        if schema.get("format") == "date-time":
            try:
                datetime.fromisoformat(value.replace("Z", "+00:00"))
            except ValueError:
                errors.append(f"{where}: invalid RFC3339 date-time")

    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if "minimum" in schema and value < schema["minimum"]:
            errors.append(f"{where}: value below minimum {schema['minimum']}")
        if "maximum" in schema and value > schema["maximum"]:
            errors.append(f"{where}: value above maximum {schema['maximum']}")
        if "exclusiveMinimum" in schema and value <= schema["exclusiveMinimum"]:
            errors.append(f"{where}: value not above {schema['exclusiveMinimum']}")

    if isinstance(value, dict):
        properties = schema.get("properties", {})
        for required in schema.get("required", []):
            if required not in value:
                errors.append(f"{where}: missing required field {required!r}")
        if schema.get("additionalProperties") is False:
            for key in value:
                if key not in properties:
                    errors.append(f"{where}: unknown field {key!r}")
        for key, child in value.items():
            if key in properties:
                errors.extend(validate_schema(child, properties[key], f"{where}.{key}"))

    if isinstance(value, list):
        if "minItems" in schema and len(value) < schema["minItems"]:
            errors.append(f"{where}: fewer than {schema['minItems']} items")
        if "maxItems" in schema and len(value) > schema["maxItems"]:
            errors.append(f"{where}: more than {schema['maxItems']} items")
        if schema.get("uniqueItems"):
            serialized = [json.dumps(item, sort_keys=True) for item in value]
            if len(serialized) != len(set(serialized)):
                errors.append(f"{where}: duplicate array items")
        if isinstance(schema.get("items"), dict):
            for index, item in enumerate(value):
                errors.extend(validate_schema(item, schema["items"], f"{where}[{index}]"))

    return errors


def validate_required() -> list[str]:
    return [f"missing required artifact: {path}" for path in REQUIRED if not (ROOT / path).exists()]


def validate_json() -> list[str]:
    errors: list[str] = []
    parsed: dict[str, Any] = {}
    for path in sorted(list((ROOT / "schemas").glob("*.json")) + list((ROOT / "examples").glob("*.json"))):
        rel = str(path.relative_to(ROOT))
        try:
            parsed[rel] = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            errors.append(f"{rel}: invalid JSON: {exc}")

    for example, schema_path in EXAMPLES.items():
        if example not in parsed or schema_path not in parsed:
            continue
        errors.extend(validate_schema(parsed[example], parsed[schema_path], example))

    for schema_path in sorted((ROOT / "schemas").glob("*.json")):
        rel = str(schema_path.relative_to(ROOT))
        schema = parsed.get(rel)
        if not isinstance(schema, dict):
            continue
        if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            errors.append(f"{rel}: schema must declare JSON Schema draft 2020-12")
        if schema.get("type") != "object":
            errors.append(f"{rel}: root schema must be an object")
        if schema.get("additionalProperties") is not False:
            errors.append(f"{rel}: root must fail closed with additionalProperties=false")
    return errors


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
        ("required artifacts", validate_required),
        ("JSON schemas/examples", validate_json),
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

    print("\nTorio artifact pack is internally consistent.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
