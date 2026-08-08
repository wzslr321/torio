#!/usr/bin/env python3
"""Portable validation for the Torio documentation surface.

Uses only Python's standard library so it can run before Go or project dependencies
are installed. It validates relative Markdown links, obvious secret material, that
every document Go cites actually exists, and that no product version label reaches
a surface an operator reads.

Scope note: this script used to also check a fixed list of required artifacts and
a JSON Schema subset. Both belonged to the pre-V1 exploration removed by ADR-0005;
nothing in the delivered product reads `schemas/`, and the required-file list only
pinned files that no longer exist.
"""

from __future__ import annotations

import html
import re
import sys
from collections.abc import Iterable, Mapping
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

# A credential the documentation hands over as a ready-to-paste literal.
#
# A documented assignment must stop at `=` and let the operator type the value,
# so an unread instruction produces something that does not work rather than
# something that works badly.
#
# The value is horizontal-whitespace-separated on purpose. Letting `\s*` cross
# the newline made the corrected block fail on the closing code fence below it —
# a rule that fires on its own fix teaches the next author to weaken it.
CREDENTIAL_ASSIGNMENT = re.compile(
    r"\b[A-Z][A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSPHRASE|API_KEY)[ \t]*=[ \t]*(\S*)"
)

# Values that are not handed to anyone: a redaction marker, or a shell expansion
# that resolves to whatever the operator already set. An empty value is the
# shape this rule exists to ask for and is exempt by being empty.
CREDENTIAL_VALUE_EXEMPT = re.compile(r"^(?:\[REDACTED\]|\$\{?[A-Za-z_]|['\"]{2}$)")

# Where an operator reads instructions and follows them. Block sources are
# included because that is where the text is authored; the generated copies are
# checked for drift by build_docs.py --check.
CREDENTIAL_GLOBS = (
    "README.md",
    "docs/content/blocks/*.md",
    "docs/content/pages/*.md",
    "docs/content/runbooks/*.md",
    "docs/runbooks/*.md",
    "site/*.html",
    "site/*.md",
)

# A docs/ path cited from Go source — in a comment or in help text the operator
# reads. Either way the file has to exist: six references to a contract archived
# by ADR-0005 survived three pull requests because nothing checked (ADR-0005).
#
# A path qualified by a Git ref (`archive/pre-v1:docs/…`) is exempt: it names
# something in history on purpose, and the tree is the wrong place to look.
GO_DOC_REFERENCE = re.compile(r"(?<![:\w])docs/[A-Za-z0-9_./-]*\.md")

# A product version label: a standalone V0/V1/v2 token. The lookbehind is what
# separates a label from a path segment, an identifier, or a flag value — so
# `archive/pre-v1`, `archive/pre-oss:docs/adr/0015-torio-v1-…`, `IPv4` and Git's own
# `--porcelain=v1` are not labels and never trip this.
VERSION_LABEL = re.compile(r"(?<![\w/=-])[Vv][0-9]+\b")

# Everything a user of Torio reads. ADR-0005 keeps labels out of exactly this
# set; ADRs, docs/contracts/ and AGENTS.md deliberately keep theirs, because
# there the version scope is the subject of the record rather than decoration.
#
# `site/*.md` and the installer joined the set after the first pass covered only
# `site/*.html`: the deployment handoff still called itself the "V0 docs site"
# and the installer still refused a host in the name of a release scope, and
# neither was reachable by a glob that stopped at generated HTML.
USER_FACING_GLOBS = (
    "README.md",
    "site/*.html",
    "site/*.md",
    "docs/runbooks/*.md",
    "scripts/install.sh",
)

# Comment syntax per file type, or None where a file has no comments. Comments
# are exempt for the same reason Go comments are: they carry the ADR context
# that explains a rule, and no operator reads them.
COMMENT_PREFIXES = {".go": "//", ".sh": "#", ".py": "#"}

GO_STRING_LITERAL = re.compile(r'"(?:[^"\\\n]|\\.)*"')

# Everything in a Go file that looks like code but is not: comments, strings,
# rune literals. Masking these is what lets the command-surface derivation count
# braces without a Go parser, and it is why a `Use:` written in a comment is not
# a command.
GO_LEXICAL_NOISE = re.compile(
    r"//[^\n]*"
    r"|/\*.*?\*/"
    r'|"(?:[^"\\\n]|\\.)*"'
    r"|`[^`]*`"
    r"|'(?:[^'\\\n]|\\.)*'",
    re.DOTALL,
)

# A top-level declaration, as gofmt writes it: `func` in column 0, body closed by
# `}` in column 0. That is enough structure to attribute every literal and every
# AddCommand call to the constructor that owns it.
GO_TOP_LEVEL_FUNC = re.compile(r"^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)", re.MULTILINE)
GO_TOP_LEVEL_CLOSE = re.compile(r"^\}", re.MULTILINE)

# `&cobra.Command{` or `cobra.Command{` — the opening of a command literal. The
# brace is required: `func(cmd *cobra.Command, args []string)` is a parameter
# list, not a command.
COBRA_COMMAND_LITERAL = re.compile(r"\bcobra\.Command\{")

# How a parent takes ownership of a child in this package: the child's
# constructor is called inline. Reading the call rather than the variable is what
# reconstructs `torio vm init` from a `Use:` that only says `init`.
COBRA_ADD_COMMAND = re.compile(r"\.AddCommand\(\s*([A-Za-z_]\w*)\(")

COBRA_USE_VALUE = re.compile(r'Use:\s*"((?:[^"\\]|\\.)*)"')

# The constructor of the root command. The surface is what hangs off it, so a
# command literal no parent ever adds is not part of the surface either.
ROOT_COMMAND_CONSTRUCTOR = "newRootCmd"

# Where the operator surface is authored. The comparison is against the source,
# not against site/ or docs/runbooks/: those are generated, so checking them
# would report the same gap twice and let a fix be applied to the wrong file.
# docs/contracts/cli.md is normative but is not the operator surface — a command
# documented only there still reaches no reader who is running the binary.
COMMAND_DOC_GLOBS = ("docs/content/**/*.md", "docs/contracts/*.md")


def _go_sources() -> list[Path]:
    """Production Go sources. Tests are excluded: a test may legitimately name a
    version to prove that the thing it names is gone."""
    return sorted(
        p
        for d in ("internal", "cmd")
        for p in (ROOT / d).rglob("*.go")
        if not p.name.endswith("_test.go")
    )


def validate_go_doc_references() -> list[str]:
    errors: list[str] = []
    for path in _go_sources():
        for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            for target in GO_DOC_REFERENCE.findall(line):
                if not (ROOT / target).exists():
                    where = f"{path.relative_to(ROOT)}:{lineno}"
                    errors.append(f"{where}: cites a document that does not exist: {target}")
    return errors


def validate_no_version_labels() -> list[str]:
    errors: list[str] = []

    for pattern in USER_FACING_GLOBS:
        for path in sorted(ROOT.glob(pattern)):
            comment = COMMENT_PREFIXES.get(path.suffix)
            for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
                if comment and line.lstrip().startswith(comment):
                    continue
                if match := VERSION_LABEL.search(line):
                    where = f"{path.relative_to(ROOT)}:{lineno}"
                    errors.append(f"{where}: product version label {match.group()!r}")

    # Go string literals only. Comments keep their labels — they are not a
    # user-facing surface and they carry the ADR context behind a rule.
    for path in _go_sources():
        for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            if line.lstrip().startswith("//"):
                continue
            for literal in GO_STRING_LITERAL.findall(line):
                if match := VERSION_LABEL.search(literal):
                    where = f"{path.relative_to(ROOT)}:{lineno}"
                    errors.append(f"{where}: product version label {match.group()!r} in a string")
    return errors


def validate_links() -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.rglob("*.md")):
        # Runtime artifacts are ignored by git and must not affect design validation.
        if "artifacts" in path.parts:
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


def _as_read_by_a_human(text: str, suffix: str) -> str:
    """Generated HTML, reduced to the text an operator actually reads.

    Without this the rule reports the markup that closes a code block as the
    value of an assignment that ends the line — `…SESSION_TOKEN=</code></pre>`.
    Tags are stripped one line at a time so reported line numbers still point
    into the file.
    """
    if suffix != ".html":
        return text
    return html.unescape(re.sub(r"<[^>\n]*>", "", text))


def pasteable_credentials(text: str) -> list[tuple[int, str]]:
    """(line, variable name) for every credential this text hands the reader.

    Known limitation: a documented command that greps for the assignment by
    writing `NAME=` in its pattern trips this. Phrase such a check so the name
    and the `=` are not adjacent — `awk -F= '/NAME/ …'` rather than
    `awk '/NAME=/ …'`.
    """
    findings: list[tuple[int, str]] = []
    for match in CREDENTIAL_ASSIGNMENT.finditer(text):
        value = match.group(1)
        if not value or CREDENTIAL_VALUE_EXEMPT.match(value):
            continue
        line = text.count("\n", 0, match.start()) + 1
        findings.append((line, match.group(0).split("=", 1)[0].rstrip()))
    return findings


def validate_no_pasteable_credentials() -> list[str]:
    """No document hands the operator a credential value to paste."""
    errors: list[str] = []
    seen: set[Path] = set()
    for glob in CREDENTIAL_GLOBS:
        for path in sorted(ROOT.glob(glob)):
            if not path.is_file() or path in seen:
                continue
            seen.add(path)
            text = _as_read_by_a_human(
                path.read_text(encoding="utf-8", errors="replace"), path.suffix
            )
            for line, name in pasteable_credentials(text):
                errors.append(
                    f"{path.relative_to(ROOT)}:{line}: documented credential carries a "
                    f"value the reader can paste; stop the assignment at '=' ({name}=)"
                )
    return errors


def _mask_go_noise(text: str) -> str:
    """`text` with comments and literals blanked out, index for index.

    Braces can then be counted without a Go parser, and the value of a field can
    still be read out of the original text at the same offset. Newlines survive
    so line-anchored patterns keep working over the result.
    """

    def blank(match: re.Match[str]) -> str:
        return "".join("\n" if ch == "\n" else "\0" for ch in match.group(0))

    return GO_LEXICAL_NOISE.sub(blank, text)


def _top_level_functions(masked: str) -> list[tuple[str, int, int]]:
    """(name, start, end) for every top-level func, over masked source."""
    spans: list[tuple[str, int, int]] = []
    for match in GO_TOP_LEVEL_FUNC.finditer(masked):
        close = GO_TOP_LEVEL_CLOSE.search(masked, match.end())
        spans.append((match.group(1), match.start(), close.end() if close else len(masked)))
    return spans


def _command_use(masked: str, text: str, brace: int) -> str:
    """The `Use` value of the cobra literal whose `{` sits at `brace`.

    Only a field of the literal itself counts. `internal/cli/project.go` builds a
    `projects.AddRequest{… Use: use}` inside the command's RunE closure: that
    `Use` is two literals deeper, describes a request rather than a command, and
    is excluded by depth — not by knowing which file it is in.
    """
    depth = 0
    for i in range(brace, len(masked)):
        char = masked[i]
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                break
        elif depth == 1 and masked.startswith("Use:", i):
            if match := COBRA_USE_VALUE.match(text, i):
                return match.group(1)
    return ""


def command_paths(sources: Mapping[str, str]) -> list[str]:
    """Every leaf command the given Go sources build, as full command paths.

    The tree is reconstructed from how a constructor is handed to a parent's
    `AddCommand`, because a `Use` string only ever names its own segment: `init`
    is a command under `vm` and under nothing else.

    A parent is not a leaf. `torio vm` takes no action of its own — an absent or
    unknown subcommand is a usage error — so the unit a document has to describe
    is the subcommand beneath it.
    """
    constructors: dict[str, tuple[str, list[str]]] = {}
    for text in sources.values():
        masked = _mask_go_noise(text)
        for name, start, end in _top_level_functions(masked):
            body_masked, body_text = masked[start:end], text[start:end]
            use = ""
            if literal := COBRA_COMMAND_LITERAL.search(body_masked):
                use = _command_use(body_masked, body_text, literal.end() - 1)
            children = COBRA_ADD_COMMAND.findall(body_masked)
            if use or children:
                constructors[name] = (use, children)

    paths: list[str] = []

    def walk(name: str, prefix: list[str], seen: frozenset[str]) -> None:
        if name in seen:
            return
        use, children = constructors.get(name, ("", []))
        # `add <name> <remote>`, `import <host-directory>` and `ssh -- COMMAND...`
        # each name one command; the rest of the string describes its arguments.
        segments = use.split()
        path = prefix + segments[:1]
        if not children:
            # Only a node that named itself is a command. A constructor these
            # sources do not define names nothing, so it contributes nothing
            # rather than silently duplicating its parent's path.
            if segments:
                paths.append(" ".join(path))
            return
        for child in children:
            walk(child, path, seen | {name})

    walk(ROOT_COMMAND_CONSTRUCTOR, [], frozenset())
    return sorted(paths)


def command_surface() -> list[str]:
    """The delivered command surface, derived from internal/cli/.

    Tests are excluded: a test builds command trees of its own, and none of them
    is something an operator can run.
    """
    return command_paths(
        {
            str(path.relative_to(ROOT)): path.read_text(encoding="utf-8")
            for path in sorted((ROOT / "internal" / "cli").rglob("*.go"))
            if not path.name.endswith("_test.go")
        }
    )


def undocumented_commands(commands: Iterable[str], documents: Mapping[str, str]) -> list[str]:
    return sorted(c for c in commands if not any(c in text for text in documents.values()))


def validate_command_coverage() -> list[str]:
    """Every command the binary exposes is described on the operator surface.

    That the two agreed before this check existed was luck: nothing failed when a
    subcommand shipped undocumented, so nothing would have failed the next time.
    """
    surface = command_surface()
    if not surface:
        return [
            "internal/cli/: no cobra command tree was found, so this check would "
            "pass without proving anything"
        ]
    documents = {
        str(path.relative_to(ROOT)): path.read_text(encoding="utf-8")
        for glob in COMMAND_DOC_GLOBS
        for path in sorted(ROOT.glob(glob))
    }
    return [
        f"operator documentation: nothing documents {command!r}; the binary exposes a "
        f"command the operator surface does not describe"
        for command in undocumented_commands(surface, documents)
    ]


def main() -> int:
    checks = [
        ("relative Markdown links", validate_links),
        ("secret patterns", validate_secrets),
        ("docs cited from Go exist", validate_go_doc_references),
        ("no version labels for the operator", validate_no_version_labels),
        ("no pasteable credentials in docs", validate_no_pasteable_credentials),
        ("every command is documented", validate_command_coverage),
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
