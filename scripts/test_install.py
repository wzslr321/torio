#!/usr/bin/env python3
#
# AI-Provenance:
#   model: Cursor Grok 4.5
#   harness: Cursor
#   skills:
#     - mark-ai-provenance
#
"""Tests for scripts/install.sh using synthetic release assets."""

from __future__ import annotations

import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
INSTALL_SH = ROOT / "scripts" / "install.sh"
PACKAGE = ROOT / "scripts" / "package_release.py"


def run(cmd: list[str], **kwargs) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        text=True,
        capture_output=True,
        check=False,
        **kwargs,
    )


class InstallerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory(prefix="torio-install-test-")
        self.root = Path(self.tmp.name)
        self.assets = self.root / "assets"
        self.prefix = self.root / "prefix"
        self.bindir = self.root / "bin"
        self.assets.mkdir()
        self.prefix.mkdir()
        self.bindir.mkdir()
        self._write_uname_stub("Darwin", "arm64")
        # Build a tiny fake binary and package it.
        binary = self.root / "torio-bin"
        binary.write_bytes(b"#!/bin/sh\necho torio-fake\n")
        binary.chmod(0o755)
        license_path = self.root / "LICENSE"
        license_path.write_text("MIT test\n", encoding="utf-8")
        readme = self.root / "README.md"
        readme.write_text("# test\n", encoding="utf-8")
        proc = run(
            [
                sys.executable,
                str(PACKAGE),
                "--version",
                "9.9.9",
                "--binary",
                str(binary),
                "--license",
                str(license_path),
                "--readme",
                str(readme),
                "--out",
                str(self.assets),
            ]
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def _write_uname_stub(self, sysname: str, machine: str) -> None:
        """Stub uname on PATH so Linux CI can exercise the Darwin installer path."""
        stub = self.bindir / "uname"
        stub.write_text(
            "#!/bin/bash\n"
            'case "${1:-}" in\n'
            f'  -s) printf "%s\\n" "{sysname}" ;;\n'
            f'  -m) printf "%s\\n" "{machine}" ;;\n'
            f'  *) printf "%s\\n" "{sysname}" ;;\n'
            "esac\n",
            encoding="utf-8",
        )
        stub.chmod(0o755)

    def _env(self) -> dict[str, str]:
        env = os.environ.copy()
        env["HOME"] = str(self.root / "home")
        Path(env["HOME"]).mkdir(exist_ok=True)
        # Prefer stubs over system uname.
        env["PATH"] = f"{self.bindir}{os.pathsep}{env.get('PATH', '')}"
        return env

    def _install(self, *extra: str) -> subprocess.CompletedProcess[str]:
        return run(
            [
                "bash",
                str(INSTALL_SH),
                "--version",
                "9.9.9",
                "--prefix",
                str(self.prefix),
                "--base-url",
                str(self.assets),
                *extra,
            ],
            env=self._env(),
        )

    def test_installs_verified_binary(self):
        proc = self._install()
        self.assertEqual(proc.returncode, 0, proc.stderr)
        dest = self.prefix / "torio"
        self.assertTrue(dest.is_file())
        self.assertTrue(dest.stat().st_mode & stat.S_IXUSR)
        self.assertIn(str(dest), proc.stdout)
        self.assertIn("export PATH=", proc.stderr)
        self.assertNotIn("SSH_AUTH_SOCK", proc.stderr)
        self.assertNotIn("TOKEN", proc.stderr)

    def test_dry_run_does_not_install(self):
        proc = self._install("--dry-run")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertFalse((self.prefix / "torio").exists())
        self.assertIn("dry-run", proc.stderr)

    def test_checksum_mismatch_refuses_install(self):
        archive = self.assets / "torio_9.9.9_darwin_arm64.tar.gz"
        # Corrupt archive after sums were written.
        archive.write_bytes(archive.read_bytes() + b"tamper")
        proc = self._install()
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("checksum mismatch", proc.stderr)
        self.assertFalse((self.prefix / "torio").exists())

    def test_idempotent_reinstall(self):
        self.assertEqual(self._install().returncode, 0)
        first = (self.prefix / "torio").read_bytes()
        self.assertEqual(self._install().returncode, 0)
        self.assertEqual((self.prefix / "torio").read_bytes(), first)

    def test_rejects_non_darwin_platform(self):
        """Real reject path: keep Darwin-only gate, stubbed as Linux for CI."""
        self._write_uname_stub("Linux", "x86_64")
        proc = self._install()
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("unsupported platform", proc.stderr)
        self.assertIn("Linux/x86_64", proc.stderr)
        self.assertFalse((self.prefix / "torio").exists())

    def test_help(self):
        proc = run(["bash", str(INSTALL_SH), "--help"])
        self.assertEqual(proc.returncode, 0)
        self.assertIn("--dry-run", proc.stdout)


if __name__ == "__main__":
    unittest.main()
