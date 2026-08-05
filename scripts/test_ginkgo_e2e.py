#!/usr/bin/env python3
"""Contracts for the Ginkgo/Gomega E2E migration."""

from __future__ import annotations

import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ROOT_GO_MOD = ROOT / "go.mod"
E2E_GO_MOD = ROOT / "e2e" / "go.mod"
SMOKE = ROOT / "e2e" / "vm_lifecycle_test.go"
PLATFORM_SUITE = ROOT / "e2e" / "platform" / "platform_suite_test.go"
PLATFORM_JOURNEY = ROOT / "e2e" / "platform" / "journey_test.go"
PLATFORM_SHELL = ROOT / "e2e" / "platform" / "run.sh"
ASSERT_JSON = ROOT / "e2e" / "platform" / "assert_json.py"
MAKEFILE = ROOT / "Makefile"


class GinkgoE2EContractTests(unittest.TestCase):
    def test_compiled_cli_smoke_uses_ginkgo_and_gomega(self) -> None:
        module = E2E_GO_MOD.read_text(encoding="utf-8")
        smoke = SMOKE.read_text(encoding="utf-8")
        self.assertIn("github.com/onsi/ginkgo/v2", module)
        self.assertIn("github.com/onsi/gomega", module)
        self.assertIn("RunSpecs", smoke)
        self.assertIn("Describe(", smoke)
        self.assertIn("It(", smoke)
        self.assertIn("DeferCleanup", smoke)
        self.assertIn("Expect(", smoke)

    def test_real_platform_journey_is_a_tagged_ginkgo_suite(self) -> None:
        self.assertTrue(PLATFORM_SUITE.exists(), "platform Ginkgo suite is missing")
        self.assertTrue(PLATFORM_JOURNEY.exists(), "platform Ginkgo journey is missing")
        suite = PLATFORM_SUITE.read_text(encoding="utf-8")
        journey = PLATFORM_JOURNEY.read_text(encoding="utf-8")
        self.assertIn("//go:build platform_e2e", suite)
        self.assertIn("RunSpecs", suite)
        self.assertIn("Ordered", journey)
        self.assertIn("BeforeAll", journey)
        self.assertIn("DeferCleanup", journey)
        self.assertIn("Eventually", journey)
        self.assertIn("PLATFORM_E2E_TORIO_BIN", journey)
        self.assertIn("PLATFORM_E2E_ARTIFACT_DIR", journey)
        driver = (ROOT / "e2e" / "platform" / "driver_test.go").read_text(
            encoding="utf-8"
        )
        self.assertIn("configureProcessCancellation", driver)
        # Diagnostics stay the caller's job: the suite must not collect them.
        self.assertNotIn("e2e/platform/diagnostics.sh", journey)
        self.assertFalse(PLATFORM_SHELL.exists())
        self.assertFalse(ASSERT_JSON.exists())

    def test_make_targets_run_the_ginkgo_suites_through_go_test(self) -> None:
        makefile = MAKEFILE.read_text(encoding="utf-8")
        self.assertIn("go test -C e2e -count=1 -tags=e2e", makefile)
        self.assertIn("go test -C e2e -count=1 -tags=platform_e2e", makefile)
        self.assertNotIn("bash e2e/platform/run.sh", makefile)

    def test_the_e2e_suites_stay_out_of_the_product_module(self) -> None:
        """The whole point of the separate module: a test framework's
        dependency graph must not reach the module holding the credential
        boundary. Asserted on the manifests, because that is where it is lost."""
        root = ROOT_GO_MOD.read_text(encoding="utf-8")
        self.assertNotIn("onsi/ginkgo", root)
        self.assertNotIn("onsi/gomega", root)
        self.assertIn("module github.com/wzslr321/torio/e2e", E2E_GO_MOD.read_text(encoding="utf-8"))
        for source in (ROOT / "e2e").rglob("*.go"):
            self.assertNotIn(
                "github.com/wzslr321/torio/internal",
                source.read_text(encoding="utf-8"),
                f"{source} imports product code; the suites drive the compiled binary instead",
            )


if __name__ == "__main__":
    unittest.main()
