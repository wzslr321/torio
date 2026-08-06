#!/usr/bin/env python3
"""Package a Torio release archive and SHA256SUMS for one supported host.

Usage:
    python3 scripts/package_release.py --version 1.0.0 --platform darwin/arm64 \
        --binary ./torio --out dist/

Produces:
    torio_<version>_<goos>_<goarch>.tar.gz
    SHA256SUMS   (rewritten from every archive present in --out)

The archive contains only: torio (binary), LICENSE, README.md (release notes).

Run once per supported host into the same --out. SHA256SUMS is regenerated from
the directory rather than appended to, so a rerun cannot leave a stale line for
an archive that was rebuilt, and the file is byte-identical whatever order the
platforms were packaged in.
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
ASSET_NAME_FMT = "torio_{version}_{goos}_{goarch}.tar.gz"
REQUIRED_MEMBERS = ("torio", "LICENSE", "README.md")

# The hosts Torio ships for. This list must equal the keys of
# internal/lima.profiles: an archive for a platform the CLI has no instance pins
# for installs cleanly and then refuses every command, and a supported platform
# with no archive cannot be installed at all. test_package_release.py reads the
# Go table and asserts the two agree, so the duplication cannot rot silently.
SUPPORTED_PLATFORMS = ("darwin/arm64", "linux/amd64")

# Human-facing description per host, for the generated release README.
PLATFORM_LABELS = {
    "darwin/arm64": "macOS on Apple Silicon",
    "linux/amd64": "Linux on x86_64",
}


class PackageError(Exception):
    """Fail-closed packaging error."""


def split_platform(platform: str) -> tuple[str, str]:
    if platform not in SUPPORTED_PLATFORMS:
        supported = ", ".join(SUPPORTED_PLATFORMS)
        raise PackageError(f"unsupported platform {platform!r}: want one of {supported}")
    goos, _, goarch = platform.partition("/")
    return goos, goarch


def asset_name(version: str, platform: str) -> str:
    if not VERSION_RE.match(version):
        raise PackageError(f"invalid version {version!r}: want semver-like MAJOR.MINOR.PATCH")
    goos, goarch = split_platform(platform)
    return ASSET_NAME_FMT.format(version=version, goos=goos, goarch=goarch)


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
    platform: str,
    binary: Path,
    license_path: Path,
    readme_path: Path,
    out_dir: Path,
) -> tuple[Path, Path]:
    """Create the tarball and SHA256SUMS. Returns (archive_path, sums_path)."""
    name = asset_name(version, platform)
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

    return archive_path, write_sums(out_dir)


def write_sums(out_dir: Path) -> Path:
    """Rewrite SHA256SUMS from every release archive in out_dir.

    Regenerated rather than appended to: appending would let a rebuilt archive
    keep its old line alongside the new one, and `install.sh` matches by
    filename, so it would verify against whichever came first. Sorting makes the
    file identical regardless of the order the platforms were packaged in.
    """
    lines = [
        f"{sha256_file(archive)}  {archive.name}\n"
        for archive in sorted(out_dir.glob("torio_*.tar.gz"))
    ]
    if not lines:
        raise PackageError(f"no release archives found in {out_dir}")
    sums_path = out_dir / "SHA256SUMS"
    sums_path.write_text("".join(lines), encoding="utf-8")
    return sums_path


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def default_release_readme(version: str, platform: str) -> str:
    label = PLATFORM_LABELS[platform]
    supported = ", ".join(PLATFORM_LABELS[p] for p in SUPPORTED_PLATFORMS)
    return (
        f"# Torio {version}\n\n"
        f"{label} ({platform}) release.\n\n"
        "## Install\n\n"
        "Verify `SHA256SUMS`, extract `torio` onto your `PATH` "
        "(for example `~/.local/bin`), then run `torio version --json`.\n\n"
        f"This archive runs on {label}. Supported hosts: {supported}.\n"
    )


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--version", required=True, help="Release version without leading v")
    p.add_argument(
        "--platform",
        required=True,
        choices=SUPPORTED_PLATFORMS,
        help="Host this binary was built for, as GOOS/GOARCH",
    )
    p.add_argument("--binary", required=True, type=Path, help="Path to built torio binary")
    p.add_argument("--license", type=Path, default=Path("LICENSE"), help="LICENSE file")
    # --readme exists as a deterministic test input; releases take the default.
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
        try:
            if readme is None:
                fd, tmp_name = tempfile.mkstemp(prefix="torio-release-readme-", suffix=".md")
                os.close(fd)
                tmp_readme = Path(tmp_name)
                tmp_readme.write_text(
                    default_release_readme(args.version, args.platform), encoding="utf-8"
                )
                readme = tmp_readme
            archive, sums = build_archive(
                version=args.version,
                platform=args.platform,
                binary=args.binary,
                license_path=args.license,
                readme_path=readme,
                out_dir=args.out,
            )
        finally:
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
