#!/usr/bin/env python3
"""Tests for the documentation-surface validator.

Only the pasteable-credential rule is covered here. It is the one rule written
in response to something that happened rather than something that was reasoned
about, and the failure it prevents is silent, so it needs a test that pins both
directions: the text that caused the incident must fail, and the corrected text
must pass.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import validate_artifacts as v  # noqa: E402

# The block exactly as it stood before the correction, at 6ff120b. Pasted
# unchanged during Task 23 it pinned a working, guessable token and nothing
# reported an error — see archive/pre-oss:docs/v1-evidence/FINDINGS-run-a.md.
PRE_FIX_BLOCK = """\
```bash
limactl shell torio            # interactive shell in the VM (Lima user)
sudo -iu hermes                      # become the hermes service identity

install -d -m 700 ~/.config/systemd/user/hermes-serve.service.d
umask 077
cat > ~/.config/systemd/user/hermes-serve.service.d/override.conf <<'EOF'
[Service]
Environment=HERMES_DASHBOARD_SESSION_TOKEN=PASTE-YOUR-TOKEN-HERE
EOF
chmod 600 ~/.config/systemd/user/hermes-serve.service.d/override.conf
```
"""

# The correction: the assignment stops at `=`, the operator types the value.
POST_FIX_BLOCK = """\
```ini
[Service]
Environment=HERMES_DASHBOARD_SESSION_TOKEN=
```

Save with `Ctrl+O`.
"""


class PasteableCredentials(unittest.TestCase):
    def test_placeholder_value_is_rejected(self) -> None:
        findings = v.pasteable_credentials(PRE_FIX_BLOCK)
        self.assertEqual([(9, "HERMES_DASHBOARD_SESSION_TOKEN")], findings)

    def test_assignment_stopping_at_equals_is_accepted(self) -> None:
        # A value must be looked for on the assignment's own line. Matching
        # across the newline made the corrected block fail on the fence below
        # it, which would have taught the next author to weaken the rule.
        self.assertEqual([], v.pasteable_credentials(POST_FIX_BLOCK))

    def test_trailing_whitespace_is_still_an_empty_value(self) -> None:
        self.assertEqual([], v.pasteable_credentials("API_KEY=   \nnext-line\n"))

    def test_redaction_marker_is_accepted(self) -> None:
        self.assertEqual([], v.pasteable_credentials("SESSION_TOKEN=[REDACTED]\n"))

    def test_shell_expansion_is_accepted(self) -> None:
        # Resolves to whatever the operator already set; hands over nothing.
        self.assertEqual([], v.pasteable_credentials("GH_TOKEN=${GITHUB_TOKEN}\n"))
        self.assertEqual([], v.pasteable_credentials("GH_TOKEN=$GITHUB_TOKEN\n"))

    def test_empty_string_literal_is_accepted(self) -> None:
        self.assertEqual([], v.pasteable_credentials('SESSION_TOKEN=""\n'))

    def test_angle_bracket_placeholder_is_rejected(self) -> None:
        # An ini file pins `<your-token>` as literally as it pins any other
        # string. It reads like an instruction and behaves like a credential.
        self.assertEqual(
            [(1, "API_TOKEN")], v.pasteable_credentials("API_TOKEN=<your-token>\n")
        )

    def test_generated_html_is_read_without_its_markup(self) -> None:
        # The rendered page ends the assignment with the tags that close the
        # code block, on the same line. Those are not a value.
        page = (
            "<pre><code>[Service]\n"
            "Environment=HERMES_DASHBOARD_SESSION_TOKEN=</code></pre>\n"
        )
        self.assertEqual([], v.pasteable_credentials(v._as_read_by_a_human(page, ".html")))

    def test_html_entities_are_decoded_before_matching(self) -> None:
        page = "<code>SESSION_TOKEN=&quot;&quot;</code>\n"
        self.assertEqual([], v.pasteable_credentials(v._as_read_by_a_human(page, ".html")))

    def test_stripping_markup_preserves_line_numbers(self) -> None:
        page = "<p>one</p>\n<code>API_TOKEN=hunter2</code>\n"
        self.assertEqual(
            [(2, "API_TOKEN")],
            v.pasteable_credentials(v._as_read_by_a_human(page, ".html")),
        )

    def test_the_repository_hands_over_nothing(self) -> None:
        self.assertEqual([], v.validate_no_pasteable_credentials())


if __name__ == "__main__":
    unittest.main()
