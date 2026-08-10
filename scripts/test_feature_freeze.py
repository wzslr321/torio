#!/usr/bin/env python3
"""Tests for the branch-local feature-freeze guard."""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_feature_freeze as freeze  # noqa: E402


class FeatureFreezeGuard(unittest.TestCase):
    def test_reads_state_only_from_the_issue_label(self) -> None:
        self.assertTrue(
            freeze.freeze_active(
                {"labels": [{"name": "feature-freeze"}], "body": "ignored"}
            )
        )
        self.assertFalse(
            freeze.freeze_active(
                {"labels": [], "body": "feature-freeze", "comments": "ignored"}
            )
        )

    def test_allows_refinement_moves_upgrades_and_docs(self) -> None:
        self.assertEqual(
            [],
            freeze.findings(
                "M\tgo.mod\nM\tinternal/cli/old.go\nA\tinternal/cli/ui.go\nA\tdocs/content/pages/status.md\n",
                """+++ b/go.mod
-github.com/example/module v1.0.0
+github.com/example/module v1.1.0
+++ b/internal/cli/old.go
-root.AddCommand(newStatusCmd(a))
+++ b/internal/cli/ui.go
+root.AddCommand(newStatusCmd(a))
+\tif err != nil { return err }
""",
                ["internal/cli/status_helpers.go"],
                "fix/hub-cancel",
                ["fix: stop the hub operation"],
                ["cmd/torio/main.go", "integrations/neovim/plugin/torio.lua"],
            ),
        )

    def test_blocks_net_structural_expansion_signals(self) -> None:
        got = "\n".join(
            freeze.findings(
                "M\tgo.mod\nA\tinternal/cli/export.go\nA\tcmd/torio-export/main.go\nA\tintegrations/vscode/extension.ts\nA\tbrainkit/commands/export.md\n",
                """+++ b/go.mod
+github.com/new/module v1.0.0
+++ b/internal/cli/export.go
+root.AddCommand(newExportCmd(a))
+cmd.Flags().BoolVar(&all, "all", false, "export everything")
+Backend string `json:"backend"`
+backend.Register(newBackend())
""",
                ["integrations/vscode/extension.ts"],
                "feat/export",
                ["feat: export the Brain"],
                ["cmd/torio/main.go", "integrations/neovim/plugin/torio.lua"],
            )
        )
        for signal in (
            "feature branch",
            "feature commit",
            "new dependency",
            "new command",
            "new flag",
            "new JSON or config field",
            "new backend",
            "new executable",
            "new integration",
            "new agent command",
        ):
            self.assertIn(signal, got)


if __name__ == "__main__":
    unittest.main()
