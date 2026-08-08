#!/usr/bin/env python3
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
        broker = self.root / "broker-bin"
        relay = self.root / "relay-bin"
        broker.write_bytes(b"#!/bin/sh\necho broker-fake\n")
        relay.write_bytes(b"#!/bin/sh\necho relay-fake\n")
        broker.chmod(0o755)
        relay.chmod(0o755)
        license_path = self.root / "LICENSE"
        license_path.write_text("MIT test\n", encoding="utf-8")
        readme = self.root / "README.md"
        readme.write_text("# test\n", encoding="utf-8")
        # Every supported host is packaged into one asset directory, sharing a
        # single SHA256SUMS -- the shape a real release has. An installer test
        # that only ever saw its own platform's archive could not tell "picked
        # the right asset" from "picked the only asset".
        for platform in ("darwin/arm64", "linux/amd64"):
            proc = run(
                [
                    sys.executable,
                    str(PACKAGE),
                    "--version",
                    "9.9.9",
                    "--platform",
                    platform,
                    "--binary",
                    str(binary),
                    "--broker-binary",
                    str(broker),
                    "--relay-binary",
                    str(relay),
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
        for guest_payload in (
            self.prefix / "torio-mcp-broker-linux-arm64",
            self.prefix / "torio-mcp-connect-linux-arm64",
        ):
            self.assertTrue(guest_payload.is_file(), guest_payload)
            self.assertTrue(guest_payload.stat().st_mode & stat.S_IXUSR)
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

    def test_installs_on_linux_x86_64(self):
        """The Linux host installs the Linux asset, from the same directory."""
        self._write_uname_stub("Linux", "x86_64")
        proc = self._install()
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue((self.prefix / "torio").is_file())
        self.assertIn("torio_9.9.9_linux_amd64.tar.gz", proc.stderr)
        self.assertNotIn("darwin_arm64", proc.stderr)

    def test_each_host_selects_its_own_asset(self):
        """The asset name follows the machine, not the order of the directory."""
        for sysname, machine, want in (
            ("Darwin", "arm64", "torio_1.2.3_darwin_arm64.tar.gz"),
            ("Linux", "x86_64", "torio_1.2.3_linux_amd64.tar.gz"),
        ):
            with self.subTest(host=f"{sysname}/{machine}"):
                self._write_uname_stub(sysname, machine)
                proc = self._source_lib("require_platform; asset_urls 1.2.3")
                self.assertEqual(proc.returncode, 0, proc.stderr)
                self.assertIn(want, proc.stdout)

    def test_rejects_unsupported_platform(self):
        """Hosts outside the matrix fail before anything is downloaded.

        Both of these are plausible mistakes rather than absurd ones: an Intel
        Mac cannot run Lima's vz driver, and arm64 Linux is a configuration
        nothing here has ever booted. The installer must not place a binary that
        would refuse every command it is given.
        """
        for sysname, machine in (("Darwin", "x86_64"), ("Linux", "aarch64")):
            with self.subTest(host=f"{sysname}/{machine}"):
                self._write_uname_stub(sysname, machine)
                proc = self._install()
                self.assertNotEqual(proc.returncode, 0)
                self.assertIn("unsupported platform", proc.stderr)
                self.assertIn(f"{sysname}/{machine}", proc.stderr)
                self.assertFalse((self.prefix / "torio").exists())

    def test_help(self):
        proc = run(["bash", str(INSTALL_SH), "--help"])
        self.assertEqual(proc.returncode, 0)
        self.assertIn("--dry-run", proc.stdout)
        self.assertIn("--base-url URL", proc.stdout)

    def _source_lib(self, script: str, env_extra: dict[str, str] | None = None):
        """Run a snippet with install.sh sourced, so its functions can be called
        without invoking main()."""
        env = self._env()
        env["TORIO_INSTALL_LIB"] = "1"
        env.update(env_extra or {})
        return run(["bash", "-c", f"source {INSTALL_SH}\n{script}"], env=env)

    def test_bad_archive_leaves_no_temp_directory(self):
        """`die` exits, which skips the RETURN trap: the extract dir must be
        removed on that path too, not only on normal return."""
        import tarfile

        member = self.root / "not-torio"
        member.write_text("x", encoding="utf-8")
        bad = self.root / "bad.tar.gz"
        with tarfile.open(bad, "w:gz") as tf:
            tf.add(member, arcname="not-torio")
        tmpdir = self.root / "tmpdir"
        tmpdir.mkdir()
        proc = self._source_lib(
            f"install_from_archive '{bad}' '{self.prefix}'",
            {"TMPDIR": str(tmpdir)},
        )
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("archive missing torio binary", proc.stderr)
        self.assertEqual(list(tmpdir.iterdir()), [], "temp extract dir leaked")

    def test_repository_defaults_to_the_upstream_slug(self):
        proc = self._source_lib('require_platform; asset_urls 1.2.3')
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("wzslr321/torio/releases/download/v1.2.3", proc.stdout)

    def test_repository_can_be_overridden_by_environment(self):
        # The slug is the one thing that changes when the repository moves to
        # an organization. Without an override the installer resolves assets
        # from a repository that no longer holds them.
        proc = self._source_lib(
            "require_platform; asset_urls 1.2.3", {"TORIO_REPO": "an-org/torio"}
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("an-org/torio/releases/download/v1.2.3", proc.stdout)
        self.assertNotIn("wzslr321", proc.stdout)


if __name__ == "__main__":
    unittest.main()
