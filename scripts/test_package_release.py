#!/usr/bin/env python3
"""Unit tests for scripts/package_release.py."""

from __future__ import annotations

import hashlib
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import package_release as pr  # noqa: E402


class AssetNameTests(unittest.TestCase):
    def test_semver_name(self):
        self.assertEqual(pr.asset_name("1.0.0"), "torio_1.0.0_darwin_arm64.tar.gz")

    def test_prerelease_allowed(self):
        self.assertEqual(
            pr.asset_name("1.0.0-rc.1"), "torio_1.0.0-rc.1_darwin_arm64.tar.gz"
        )

    def test_rejects_garbage(self):
        with self.assertRaises(pr.PackageError):
            pr.asset_name("../evil")


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
                binary=binary,
                license_path=license_path,
                readme_path=readme,
                out_dir=out,
            )

            self.assertEqual(archive.name, "torio_0.0.0_darwin_arm64.tar.gz")
            members = pr.archive_members(archive)
            self.assertEqual(members, ["LICENSE", "README.md", "torio"])

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
                binary=binary,
                license_path=license_path,
                readme_path=readme,
                out_dir=out,
            )
            with self.assertRaises(pr.PackageError):
                pr.build_archive(
                    version="0.0.1",
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
                    binary=binary,
                    license_path=bad,
                    readme_path=readme,
                    out_dir=root / "dist",
                )

    def test_deterministic_manifest_line(self):
        text = pr.default_release_readme("1.2.3")
        self.assertIn("1.2.3", text)
        self.assertIn("Apple Silicon", text)
        self.assertNotIn("TOKEN", text)


if __name__ == "__main__":
    unittest.main()
