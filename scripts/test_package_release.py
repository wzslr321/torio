#!/usr/bin/env python3
"""Unit tests for scripts/package_release.py."""

from __future__ import annotations

import contextlib
import hashlib
import io
import re
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(Path(__file__).resolve().parent))

import package_release as pr  # noqa: E402


class AssetNameTests(unittest.TestCase):
    def test_semver_name(self):
        self.assertEqual(pr.asset_name("1.0.0", "darwin/arm64"), "torio_1.0.0_darwin_arm64.tar.gz")

    def test_prerelease_allowed(self):
        self.assertEqual(
            pr.asset_name("1.0.0-rc.1", "linux/amd64"), "torio_1.0.0-rc.1_linux_amd64.tar.gz"
        )

    def test_rejects_garbage(self):
        with self.assertRaises(pr.PackageError):
            pr.asset_name("../evil", "darwin/arm64")


class PackageArchiveTests(unittest.TestCase):
    def _touch_binary(self, path: Path) -> None:
        path.write_bytes(b"\x7fELF-fake-torio-binary")
        path.chmod(0o755)

    def test_archive_contents_and_sums(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "torio"
            license_path = root / "LICENSE"
            readme = root / "README.md"
            out = root / "dist"
            self._touch_binary(binary)
            license_path.write_text("MIT test license\n", encoding="utf-8")
            readme.write_text("# Torio test\n", encoding="utf-8")

            archive, sums = pr.build_archive(
                version="0.0.0",
                platform="darwin/arm64",
                binary=binary,
                license_path=license_path,
                readme_path=readme,
                out_dir=out,
            )

            self.assertEqual(archive.name, "torio_0.0.0_darwin_arm64.tar.gz")

            with tarfile.open(archive, "r:gz") as tf:
                names = {m.name for m in tf.getmembers()}
                self.assertEqual(names, set(pr.REQUIRED_MEMBERS))
                # No nested paths / escape
                for m in tf.getmembers():
                    self.assertFalse(m.name.startswith("/"))
                    self.assertNotIn("..", m.name.split("/"))

            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            self.assertEqual(sums.read_text(encoding="utf-8"), f"{digest}  {archive.name}\n")

    def test_refuses_overwrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "torio"
            self._touch_binary(binary)
            license_path = root / "LICENSE"
            readme = root / "README.md"
            license_path.write_text("L\n", encoding="utf-8")
            readme.write_text("R\n", encoding="utf-8")
            out = root / "dist"
            pr.build_archive(
                version="0.0.1",
                platform="darwin/arm64",
                binary=binary,
                license_path=license_path,
                readme_path=readme,
                out_dir=out,
            )
            with self.assertRaises(pr.PackageError):
                pr.build_archive(
                    version="0.0.1",
                    platform="darwin/arm64",
                    binary=binary,
                    license_path=license_path,
                    readme_path=readme,
                    out_dir=out,
                )

    def test_archive_member_modes_are_deterministic(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "torio"
            self._touch_binary(binary)
            license_path = root / "LICENSE"
            readme = root / "README.md"
            license_path.write_text("L\n", encoding="utf-8")
            license_path.chmod(0o664)
            readme.write_text("R\n", encoding="utf-8")
            readme.chmod(0o600)

            archive, _ = pr.build_archive(
                version="0.0.3",
                platform="darwin/arm64",
                binary=binary,
                license_path=license_path,
                readme_path=readme,
                out_dir=root / "dist",
            )

            with tarfile.open(archive, "r:gz") as tf:
                modes = {member.name: member.mode & 0o777 for member in tf.getmembers()}
            self.assertEqual(
                modes,
                {"torio": 0o755, "LICENSE": 0o644, "README.md": 0o644},
            )

    def test_rejects_secret_shaped_filename(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "torio"
            self._touch_binary(binary)
            bad = root / "credentials.json"
            bad.write_text("{}", encoding="utf-8")
            readme = root / "README.md"
            readme.write_text("R\n", encoding="utf-8")
            with self.assertRaises(pr.PackageError):
                pr.build_archive(
                    version="0.0.2",
                    platform="darwin/arm64",
                    binary=binary,
                    license_path=bad,
                    readme_path=readme,
                    out_dir=root / "dist",
                )

    def test_release_readme_names_the_host_it_is_for(self):
        for platform, expected in (
            ("darwin/arm64", "Apple Silicon"),
            ("linux/amd64", "x86_64"),
        ):
            with self.subTest(platform=platform):
                text = pr.default_release_readme("1.2.3", platform)
                self.assertIn("1.2.3", text)
                self.assertIn(platform, text)
                self.assertIn(expected, text)
                self.assertNotIn("TOKEN", text)

    def test_sums_cover_every_archive_in_the_directory(self):
        """One SHA256SUMS, every asset, regardless of packaging order.

        `install.sh` looks its own asset up by filename, so a manifest holding
        only the last-packaged archive would leave the other host unable to
        verify anything.
        """
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = root / "torio"
            self._touch_binary(binary)
            license_path = root / "LICENSE"
            readme = root / "README.md"
            license_path.write_text("L\n", encoding="utf-8")
            readme.write_text("R\n", encoding="utf-8")
            out = root / "dist"

            names = []
            for platform in pr.SUPPORTED_PLATFORMS:
                archive, sums = pr.build_archive(
                    version="0.0.4",
                    platform=platform,
                    binary=binary,
                    license_path=license_path,
                    readme_path=readme,
                    out_dir=out,
                )
                names.append(archive.name)

            lines = sums.read_text(encoding="utf-8").splitlines()
            self.assertEqual(len(lines), len(pr.SUPPORTED_PLATFORMS))
            listed = [line.split("  ", 1)[1] for line in lines]
            self.assertEqual(listed, sorted(names))
            for line, name in zip(lines, sorted(names)):
                digest = hashlib.sha256((out / name).read_bytes()).hexdigest()
                self.assertEqual(line, f"{digest}  {name}")

    def test_rejects_unsupported_platform(self):
        with self.assertRaises(pr.PackageError):
            pr.asset_name("1.0.0", "linux/arm64")
        with self.assertRaises(pr.PackageError):
            pr.asset_name("1.0.0", "windows/amd64")


class TempReadmeCleanupTests(unittest.TestCase):
    def test_generated_readme_is_removed_when_packaging_fails(self):
        """Without --readme, main() writes a temp README; a failing
        build_archive must not leak it."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            created: list[Path] = []
            real_mkstemp = tempfile.mkstemp

            def recording_mkstemp(*args, **kwargs):
                kwargs["dir"] = str(root)
                fd, name = real_mkstemp(*args, **kwargs)
                created.append(Path(name))
                return fd, name

            stderr = io.StringIO()
            with mock.patch.object(pr.tempfile, "mkstemp", recording_mkstemp), \
                    contextlib.redirect_stderr(stderr):
                rc = pr.main(
                    [
                        "--version",
                        "1.0.0",
                        "--platform",
                        "darwin/arm64",
                        "--binary",
                        str(root / "missing-binary"),
                        "--out",
                        str(root / "dist"),
                    ]
                )
            self.assertEqual(rc, 2)
            self.assertEqual(len(created), 1)
            self.assertFalse(created[0].exists(), "temp release README leaked")


class SupportedPlatformsMatchTheProductTests(unittest.TestCase):
    """The packager and the CLI must agree on what a supported host is.

    They cannot share a definition across languages, so this reads the Go table
    instead. Without it, adding a host to one side produces either an archive
    that installs and then refuses every command, or a supported platform that
    has no archive to install -- both of which look fine until someone tries.
    """

    def test_python_list_equals_the_go_profile_table(self):
        source = (ROOT / "internal" / "lima" / "profile.go").read_text(encoding="utf-8")
        go_hosts = set(re.findall(r'"((?:darwin|linux|windows)/[a-z0-9]+)":\s*\{', source))
        self.assertTrue(go_hosts, "found no host keys in internal/lima/profile.go")
        self.assertEqual(go_hosts, set(pr.SUPPORTED_PLATFORMS))

    def test_every_supported_platform_has_a_release_label(self):
        self.assertEqual(set(pr.PLATFORM_LABELS), set(pr.SUPPORTED_PLATFORMS))


if __name__ == "__main__":
    unittest.main()
