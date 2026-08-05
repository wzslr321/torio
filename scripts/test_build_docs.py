#!/usr/bin/env python3
"""Tests for scripts/build_docs.py — the single-source docs generator.

Standard library only (matches the other scripts in this directory). Run with:

    python3 -m unittest discover -s scripts -p 'test_*.py'
"""

from __future__ import annotations

import sys
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import build_docs as bd  # noqa: E402


def dedent(text: str) -> str:
    return textwrap.dedent(text).strip("\n")


class ParseSourceTests(unittest.TestCase):
    def test_splits_front_matter_from_body(self):
        src = dedent(
            """
            ---
            title: Connect the tunnel
            anchor: tunnel
            ---

            Body text.
            """
        )
        meta, body = bd.parse_source(src, where="x.md")
        self.assertEqual(meta["title"], "Connect the tunnel")
        self.assertEqual(meta["anchor"], "tunnel")
        self.assertEqual(body.strip(), "Body text.")

    def test_quoted_value_keeps_colons_and_drops_the_quotes(self):
        meta, _ = bd.parse_source('---\nnote: "Torio V0: read this"\n---\n\nx\n', where="x.md")
        self.assertEqual(meta["note"], "Torio V0: read this")

    def test_body_without_front_matter_is_returned_whole(self):
        meta, body = bd.parse_source("Just a body.\n", where="x.md")
        self.assertEqual(meta, {})
        self.assertEqual(body.strip(), "Just a body.")


class IncludeTests(unittest.TestCase):
    def test_include_marker_is_replaced_by_block_body(self):
        blocks = {"tunnel": bd.Block(id="tunnel", meta={}, body="Open the forward.")}
        out = bd.expand_includes("Before\n\n<!-- include: tunnel -->\n\nAfter", blocks, where="p.md")
        self.assertIn("Open the forward.", out)
        self.assertIn("Before", out)
        self.assertIn("After", out)
        self.assertNotIn("include:", out)

    def test_same_block_can_be_included_by_two_pages(self):
        blocks = {"shared": bd.Block(id="shared", meta={}, body="One source.")}
        a = bd.expand_includes("<!-- include: shared -->", blocks, where="a.md")
        b = bd.expand_includes("<!-- include: shared -->", blocks, where="b.md")
        self.assertEqual(a, b)
        self.assertIn("One source.", a)

    def test_unknown_block_is_an_error(self):
        with self.assertRaises(bd.BuildError) as ctx:
            bd.expand_includes("<!-- include: nope -->", {}, where="p.md")
        self.assertIn("nope", str(ctx.exception))

    def test_includes_nest(self):
        blocks = {
            "outer": bd.Block(id="outer", meta={}, body="A\n\n<!-- include: inner -->"),
            "inner": bd.Block(id="inner", meta={}, body="B"),
        }
        out = bd.expand_includes("<!-- include: outer -->", blocks, where="p.md")
        self.assertIn("A", out)
        self.assertIn("B", out)

    def test_include_can_shift_heading_level(self):
        blocks = {
            "t": bd.Block(id="t", meta={}, body="## Title {#t}\n\nBody\n\n### Sub\n"),
        }
        out = bd.expand_includes("<!-- include: t level=3 -->", blocks, where="p.md")
        self.assertIn("### Title {#t}", out)
        self.assertIn("#### Sub", out)

    def test_include_without_level_keeps_authored_depth(self):
        blocks = {"t": bd.Block(id="t", meta={}, body="## Title\n")}
        out = bd.expand_includes("<!-- include: t -->", blocks, where="p.md")
        self.assertIn("## Title", out)
        self.assertNotIn("### Title", out)

    def test_heading_shift_does_not_touch_fenced_content(self):
        blocks = {
            "t": bd.Block(id="t", meta={}, body="## Title\n\n```text\n# not a heading\n```\n"),
        }
        out = bd.expand_includes("<!-- include: t level=3 -->", blocks, where="p.md")
        self.assertIn("# not a heading", out)
        self.assertNotIn("## not a heading", out)

    def test_include_can_override_the_top_heading(self):
        blocks = {"b": bd.Block(id="b", meta={}, body="## Build it {#build}\n\nBody\n")}
        out = bd.expand_includes('<!-- include: b heading="Step 1 — Build it" -->', blocks, where="p.md")
        self.assertIn("## Step 1 — Build it {#build}", out)
        self.assertNotIn("## Build it {#build}", out)
        self.assertIn("Body", out)

    def test_heading_override_applies_after_level_shift(self):
        blocks = {"b": bd.Block(id="b", meta={}, body="## Build it {#build}\n\n### Sub\n")}
        out = bd.expand_includes(
            '<!-- include: b level=3 heading="Step 1" -->', blocks, where="p.md"
        )
        self.assertIn("### Step 1 {#build}", out)
        self.assertIn("#### Sub", out)

    def test_include_cycle_is_an_error_not_a_hang(self):
        blocks = {
            "a": bd.Block(id="a", meta={}, body="<!-- include: b -->"),
            "b": bd.Block(id="b", meta={}, body="<!-- include: a -->"),
        }
        with self.assertRaises(bd.BuildError):
            bd.expand_includes("<!-- include: a -->", blocks, where="p.md")


class TableOfContentsTests(unittest.TestCase):
    def test_toc_lists_third_level_headings_with_anchors(self):
        src = dedent(
            """
            ## Get started

            <!-- toc -->

            ### Step 1 — Build {#build}

            ### Step 2 — Start the VM
            """
        )
        out = bd.expand_toc(src)
        self.assertIn("- [Step 1 — Build](#build)", out)
        self.assertIn("- [Step 2 — Start the VM](#step-2-start-the-vm)", out)
        self.assertNotIn("<!-- toc -->", out)

    def test_toc_ignores_headings_inside_code_fences(self):
        src = "<!-- toc -->\n\n```text\n### not a step\n```\n\n### real step\n"
        out = bd.expand_toc(src)
        self.assertIn("- [real step](#real-step)", out)
        self.assertNotIn("not a step](#", out)

    def test_document_without_toc_marker_is_unchanged(self):
        src = "### Step 1\n"
        self.assertEqual(bd.expand_toc(src), src)


class RunbookOutputTests(unittest.TestCase):
    def test_anchor_syntax_is_stripped_from_markdown(self):
        out = bd.strip_anchor_syntax("## Open the tunnel {#tunnel}\n\nBody\n")
        self.assertIn("## Open the tunnel", out)
        self.assertNotIn("{#tunnel}", out)

    def test_anchor_stripping_leaves_fenced_content_alone(self):
        out = bd.strip_anchor_syntax("```text\ncurl '{#x}'\n```\n")
        self.assertIn("{#x}", out)


class BlockPortabilityTests(unittest.TestCase):
    """Blocks render into runbook Markdown too, so they must stay portable."""

    def test_site_relative_html_link_in_a_block_is_rejected(self):
        blocks = {"b": bd.Block(id="b", meta={}, body="See [ref](reference.html#boundaries).")}
        problems = bd.check_block_portability(blocks)
        self.assertEqual(len(problems), 1)
        self.assertIn("reference.html", problems[0])

    def test_fragment_and_absolute_links_are_allowed(self):
        blocks = {
            "b": bd.Block(
                id="b",
                meta={},
                body="See [here](#tunnel) and [there](https://example.com).",
            )
        }
        self.assertEqual(bd.check_block_portability(blocks), [])


class MarkdownRenderTests(unittest.TestCase):
    def render(self, md: str) -> str:
        return bd.render_markdown(dedent(md))

    def test_heading_gets_slug_id(self):
        self.assertIn('<h2 id="start-the-vm">Start the VM</h2>', self.render("## Start the VM"))

    def test_explicit_heading_anchor_wins(self):
        html = self.render("## Start the VM {#vm-start}")
        self.assertIn('<h2 id="vm-start">Start the VM</h2>', html)
        self.assertNotIn("{#", html)

    def test_paragraph_and_inline_markup(self):
        html = self.render("Run `torio vm start` for the **VM**, see [docs](x.html).")
        self.assertIn("<code>torio vm start</code>", html)
        self.assertIn("<strong>VM</strong>", html)
        self.assertIn('<a href="x.html">docs</a>', html)

    def test_fenced_code_block_is_escaped_and_not_inline_parsed(self):
        html = self.render(
            """
            ```text
            git diff <a> & **b**
            ```
            """
        )
        self.assertIn("<code>", html)
        self.assertIn("&lt;a&gt; &amp; **b**", html)
        self.assertNotIn("<strong>", html)

    def test_code_block_is_wrapped_and_tagged_with_its_language(self):
        html = self.render(
            """
            ```bash
            torio vm start
            ```
            """
        )
        self.assertIn('<div class="code-block">', html)
        self.assertIn('<pre data-lang="bash">', html)

    def test_code_block_without_a_language_carries_no_lang_attribute(self):
        html = self.render("```\nhb vm start\n```")
        self.assertIn("<pre>", html)
        self.assertNotIn("data-lang", html)

    def test_unordered_and_ordered_lists(self):
        self.assertIn("<ul>", self.render("- one\n- two"))
        self.assertIn("<ol>", self.render("1. one\n2. two"))

    def test_table_renders_with_header(self):
        html = self.render(
            """
            | Command | Effect |
            | --- | --- |
            | `torio vm start` | Starts it |
            """
        )
        self.assertIn("<thead>", html)
        self.assertIn("<th>Command</th>", html)
        self.assertIn("<code>torio vm start</code>", html)

    def test_blockquote_becomes_callout(self):
        html = self.render("> Watch out for this.")
        self.assertIn('class="callout"', html)
        self.assertIn("Watch out for this.", html)

    def test_bare_ampersand_in_prose_is_escaped(self):
        self.assertIn("&amp;", self.render("Fish & chips"))


class ShellHighlightTests(unittest.TestCase):
    """Command blocks are the substance of these docs, so they get tinted.

    Only shell fences are touched: tinting arbitrary output would invent
    meaning that isn't there.
    """

    def test_trailing_comment_is_marked(self):
        out = bd.highlight_shell("torio vm status   # read-only")
        self.assertIn('<span class="tok-comment"># read-only</span>', out)
        self.assertTrue(out.startswith("torio vm status"))

    def test_hash_inside_a_word_is_not_a_comment(self):
        out = bd.highlight_shell("curl http://host/api#frag")
        self.assertNotIn("tok-comment", out)

    def test_quoted_strings_are_marked(self):
        out = bd.highlight_shell("curl -w '%{http_code}\\n' http://x")
        self.assertIn("tok-str", out)
        self.assertIn("%{http_code}", out)

    def test_hash_inside_quotes_is_not_a_comment(self):
        out = bd.highlight_shell("git log --format='%h # subject'")
        self.assertNotIn("tok-comment", out)

    def test_markup_characters_are_still_escaped(self):
        out = bd.highlight_shell("python3 <repo>/check.py && echo 'a<b'")
        self.assertIn("&lt;repo&gt;", out)
        self.assertIn("&amp;&amp;", out)
        self.assertIn("a&lt;b", out)

    def test_only_shell_fences_are_highlighted(self):
        html = bd.render_markdown("```text\nhb vm status   # note\n```")
        self.assertNotIn("tok-comment", html)


class SlugTests(unittest.TestCase):
    def test_slug_is_url_safe_and_stable(self):
        self.assertEqual(bd.slug("Point Hermes Desktop at the backend"), "point-hermes-desktop-at-the-backend")
        self.assertEqual(bd.slug("`torio serve` — exit codes"), "torio-serve-exit-codes")


class CheckModeTests(unittest.TestCase):
    def test_check_reports_drift_when_output_differs(self):
        self.assertEqual(bd.diff_outputs({"a.html": "same"}, {"a.html": "same"}), [])
        drift = bd.diff_outputs({"a.html": "new"}, {"a.html": "old"})
        self.assertEqual(len(drift), 1)
        self.assertIn("a.html", drift[0])

    def test_check_reports_missing_output_file(self):
        drift = bd.diff_outputs({"a.html": "x"}, {})
        self.assertEqual(len(drift), 1)
        self.assertIn("a.html", drift[0])


if __name__ == "__main__":
    unittest.main()
