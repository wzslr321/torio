#!/usr/bin/env python3
"""Tests for the documentation-surface validator.

Two rules are covered here. The pasteable-credential rule is the one written in
response to something that happened rather than something that was reasoned
about, and the failure it prevents is silent, so it needs a test that pins both
directions: the text that caused the incident must fail, and the corrected text
must pass.

The command-coverage rule is the other kind of silent failure: a subcommand can
ship without a line of documentation and every existing check still passes. Its
derivation reads Go source with regular expressions, so the cases that would
make a naive reader wrong — a `Use:` field on something that is not a cobra
command, a `Use:` in a comment — are pinned here too.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import validate_artifacts as v  # noqa: E402

# A heredoc that assigns the placeholder itself produces a live, guessable token.
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


# A constructor shaped exactly like the ones in internal/cli/: one cobra literal,
# a nested struct literal inside the closure that happens to have a `Use` field,
# and a commented-out one for good measure. A grep for `Use:` reports three
# commands here; two of them do not exist.
DECOY_SOURCE = """\
func newThingCmd(a *app) *cobra.Command {
	var use bool
	cmd := &cobra.Command{
		Use:   "thing <name> <remote>",
		Short: "Do the thing",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Use: "ghost" is prose, not a command.
			return service.Add(ctx, projects.AddRequest{
				ID:  args[0],
				Use: use,
			})
		},
	}
	return cmd
}
"""

ROOT_SOURCE = """\
func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{Use: "torio"}
	root.AddCommand(newThingCmd(a))
	root.AddCommand(newGroupCmd(a))
	return root
}
"""

GROUP_SOURCE = """\
func newGroupCmd(a *app) *cobra.Command {
	g := &cobra.Command{
		Use:   "group",
		Short: "Parent that takes no action itself",
	}
	g.AddCommand(newGroupRunCmd(a))
	return g
}

func newGroupRunCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "run -- COMMAND...",
		Short: "Run it",
	}
}
"""


# A deliberate ratchet, updated on purpose when a command is added or removed:
# the derivation reads internal/cli/, and this pin is what makes an accidental
# change to the surface fail a test instead of passing silently.
PINNED_COMMAND_COUNT = 29


class CommandSurface(unittest.TestCase):
    def test_a_use_field_on_another_struct_is_not_a_command(self) -> None:
        # `projects.AddRequest{… Use: use}` is a field of a request object. It is
        # excluded because it is not at the top level of a cobra.Command literal,
        # not because of anything about the file it lives in.
        self.assertEqual(
            ["torio thing"], v.command_paths({"decoy.go": DECOY_SOURCE + ROOT_SOURCE})
        )

    def test_argument_placeholders_are_not_part_of_the_name(self) -> None:
        paths = v.command_paths(
            {"a.go": ROOT_SOURCE, "b.go": DECOY_SOURCE, "c.go": GROUP_SOURCE}
        )
        self.assertEqual(["torio group run", "torio thing"], paths)

    def test_only_leaves_are_commands_to_document(self) -> None:
        # `torio group` takes no action of its own; the documented unit is the
        # subcommand that does something.
        self.assertNotIn(
            "torio group",
            v.command_paths({"a.go": ROOT_SOURCE, "b.go": DECOY_SOURCE, "c.go": GROUP_SOURCE}),
        )

    def test_the_command_surface_is_pinned(self) -> None:
        surface = v.command_surface()
        self.assertEqual(PINNED_COMMAND_COUNT, len(surface))
        self.assertIn("torio vm bootstrap", surface)
        self.assertIn("torio version", surface)

    def test_a_parent_that_acts_is_documented_even_though_it_is_not_a_leaf(self) -> None:
        # `torio status` is the one parent that does something itself, so the
        # derivation above — which documents leaves — stopped demanding it the
        # moment it gained a subcommand. Telling "a parent that acts" from "a
        # parent that dispatches" by reading source text would be a guess, and a
        # guess that failed quietly is the failure this whole check exists to
        # prevent. So the one command with the hole is pinned by name instead.
        self.assertNotIn("torio status", v.command_surface())
        documented = "\n".join(
            path.read_text(encoding="utf-8")
            for glob in v.COMMAND_DOC_GLOBS
            for path in sorted(v.ROOT.glob(glob))
        )
        self.assertIn("torio status", documented)

    def test_an_undocumented_command_is_named(self) -> None:
        self.assertEqual(
            ["torio group run"],
            v.undocumented_commands(
                ["torio group run", "torio thing"], {"d.md": "Run `torio thing` first."}
            ),
        )

    def test_the_repository_documents_every_command(self) -> None:
        self.assertEqual([], v.validate_command_coverage())

    def test_command_coverage_includes_the_normative_contract(self) -> None:
        self.assertIn("docs/contracts/*.md", v.COMMAND_DOC_GLOBS)


if __name__ == "__main__":
    unittest.main()
