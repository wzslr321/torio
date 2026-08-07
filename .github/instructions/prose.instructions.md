---
applyTo: "docs/**,site/**,README.md,CONTRIBUTING.md,SECURITY.md,CHANGELOG.md"
---

# Prose

This repository is English throughout: code, comments, documentation, commit
messages, ADRs and CLI strings.

Write like a technically strong operator, not a landing page. Useful before
impressive. Every sentence carries a decision, an instruction, a constraint or a
verified fact, or it goes.

## Flag

- Marketing vocabulary: seamlessly, unlock, empower, leverage, robust,
  cutting-edge, powerful, effortless, next-level, game-changing, built for
  scale, all-in-one, "your journey", "whether you are…", "in today's…".
  "Comprehensive" is allowed only where the thing literally is.
- Em-dashes. Use a sentence break, a colon, a comma, a semicolon or
  parentheses, whichever fits.
- "Simply" and "just" in front of an instruction.
- A claim the repository does not support. Keep the distinction between
  operator-controlled and automated, documented and proven, prepared and
  deployed, a private prerequisite and a product feature. If something is
  untested, the text says so.
- The same claim restated on a second page. Say it once, in the right place.
- A rhetorical question, a filler heading, a feature grid, or a number no test
  produced.
- A pasteable credential. `make validate` fails on one, and the correct form is
  `[REDACTED]`.
- An internal milestone label on an operator-facing surface. No "V0", no "V1".

## Structure

- Most important action or constraint first.
- Bullets for decisions, prerequisites, boundaries and ordered actions. Prose
  for reasoning.
- Keep the four documentation modes separate: tutorial, how-to, reference,
  explanation. A page that teaches and specifies at the same time serves
  neither.

## Mechanics of the docs build

- A shared block in `docs/content/blocks/` renders to HTML and to Markdown, so
  it must not link to a `*.html` page. Those links are dead in a runbook, and
  the build rejects them. Cross-page links belong in the page source.
- Reuse a section with `<!-- include: id level=N heading="…" -->` instead of
  copying it.
- Mark command blocks ` ```bash `. Use ` ```text ` for output and for values to
  paste.
- Relative links must resolve. `make validate` checks every one.
