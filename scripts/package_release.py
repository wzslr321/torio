#!/usr/bin/env python3
#
# AI-Provenance:
#   model: Cursor Grok 4.5
#   harness: Cursor
#
"""Package a Torio macOS arm64 release archive and SHA256SUMS.

Usage:
    python3 scripts/package_release.py --version 1.0.0 --binary ./torio --out dist/

Produces:
    torio_<version>_darwin_arm64.tar.gz
    SHA256SUMS

The archive contains only: torio (binary), LICENSE, README.md (release notes).
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import sys
import tarfile
import tempfile
from pathlib import Path

VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$")
ASSET_NAME_FMT = "torio_{version}_darwin_arm64.tar.gz"
REQUIRED_MEMBERS = ("torio", "LICENSE", "README.md")


class PackageError(Exception):
    """Fail-closed packaging error."""


def asset_name(version: str) -> str:
    if not VERSION_RE.match(version):
        raise PackageError(f"invalid version {version!r}: want semver-like MAJOR.MINOR.PATCH")
    return ASSET_NAME_FMT.format(version=version)


def _require_file(path: Path, label: str) -> None:
    if not path.is_file():
        raise PackageError(f"missing {label}: {path}")


def _assert_no_secrets(path: Path) -> None:
    # Bounded filename checks only — never scan binary payload as text.
    name = path.name.lower()
    banned = (".env", "credentials", "id_rsa", "id_ed25519", ".pem", "secret")
    if any(b in name for b in banned):
        raise PackageError(f"refusing to package secret-shaped path: {path.name}")


def build_archive(
    *,
    version: str,
    binary: Path,
    license_path: Path,
    readme_path: Path,
    out_dir: Path,
) -> tuple[Path, Path]:
    """Create the tarball and SHA256SUMS. Returns (archive_path, sums_path)."""
    name = asset_name(version)
    _require_file(binary, "binary")
    _require_file(license_path, "LICENSE")
    _require_file(readme_path, "release README")
    for p in (binary, license_path, readme_path):
        _assert_no_secrets(p)

    out_dir.mkdir(parents=True, exist_ok=True)
    archive_path = out_dir / name
    if archive_path.exists():
        raise PackageError(f"refusing to overwrite existing archive: {archive_path}")

    with tempfile.TemporaryDirectory(prefix="torio-release-") as tmp:
        stage = Path(tmp)
        staged_bin = stage / "torio"
        shutil.copy2(binary, staged_bin)
        os.chmod(staged_bin, 0o755)
        staged_license = stage / "LICENSE"
        staged_readme = stage / "README.md"
        shutil.copy2(license_path, staged_license)
        shutil.copy2(readme_path, staged_readme)
        os.chmod(staged_license, 0o644)
        os.chmod(staged_readme, 0o644)

        with tarfile.open(archive_path, "w:gz") as tf:
            for member in REQUIRED_MEMBERS:
                tf.add(stage / member, arcname=member)

    digest = sha256_file(archive_path)
    sums_path = out_dir / "SHA256SUMS"
    # Deterministic single-line manifest: "<digest>  <filename>\n"
    sums_path.write_text(f"{digest}  {name}\n", encoding="utf-8")
    return archive_path, sums_path


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def archive_members(archive: Path) -> list[str]:
    with tarfile.open(archive, "r:gz") as tf:
        return sorted(m.name for m in tf.getmembers())


def default_release_readme(version: str) -> str:
    return (
        f"# Torio {version}\n\n"
        "macOS Apple Silicon (darwin/arm64) release.\n\n"
        "## Install\n\n"
        "Verify `SHA256SUMS`, extract `torio` onto your `PATH` "
        "(for example `~/.local/bin`), then run `torio version --json`.\n\n"
        "Supported host: macOS on Apple Silicon only.\n"
    )


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--version", required=True, help="Release version without leading v")
    p.add_argument("--binary", required=True, type=Path, help="Path to built torio binary")
    p.add_argument("--license", type=Path, default=Path("LICENSE"), help="LICENSE file")
    p.add_argument(
        "--readme",
        type=Path,
        default=None,
        help="Release README (default: generate minimal notes)",
    )
    p.add_argument("--out", type=Path, default=Path("dist"), help="Output directory")
    args = p.parse_args(argv)

    try:
        readme = args.readme
        tmp_readme: Path | None = None
        if readme is None:
            fd, tmp_name = tempfile.mkstemp(prefix="torio-release-readme-", suffix=".md")
            os.close(fd)
            tmp_readme = Path(tmp_name)
            tmp_readme.write_text(default_release_readme(args.version), encoding="utf-8")
            readme = tmp_readme
        archive, sums = build_archive(
            version=args.version,
            binary=args.binary,
            license_path=args.license,
            readme_path=readme,
            out_dir=args.out,
        )
        if tmp_readme is not None:
            tmp_readme.unlink(missing_ok=True)
    except PackageError as exc:
        print(f"package_release: {exc}", file=sys.stderr)
        return 2

    print(f"wrote {archive}")
    print(f"wrote {sums}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
