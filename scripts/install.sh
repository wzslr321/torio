#!/usr/bin/env bash
# AI-Provenance:
#   model: Cursor Grok 4.5
#   harness: Cursor
#
# Verified Torio installer for macOS Apple Silicon.
# Downloads a release asset, verifies SHA256SUMS, then installs the binary.
# Never executes the downloaded binary before checksum verification.
# Never modifies shell rc files; prints the exact PATH step instead.
set -euo pipefail

# The repository holding the release assets. Overridable because the slug is the
# one thing that changes when the repository moves to an organization, and a
# hardcoded one sends the installer to a repository that no longer has them.
#
# This is a location, not a credential. install.sh authenticates to nothing: on
# a private repository the asset URLs answer 404 and `gh release download` is
# the documented way in. Teaching it to carry a token would put Torio in the
# business of transporting credentials, which is the boundary the product is
# built around.
REPO_DEFAULT="${TORIO_REPO:-wzslr321/torio}"
PREFIX_DEFAULT="${HOME}/.local/bin"
VERSION=""
PREFIX="$PREFIX_DEFAULT"
DRY_RUN=0
BASE_URL="" # override for tests, e.g. file:///tmp/assets

usage() {
  cat <<'EOF'
Usage: install.sh [--version X.Y.Z] [--prefix DIR] [--base-url URL] [--dry-run]

Installs Torio for Darwin/arm64 into a user-writable prefix (default: ~/.local/bin).
Verifies SHA256SUMS before copying the binary. Does not use sudo and does not
modify shell startup files.

Options:
  --version X.Y.Z   Install this version (without leading v). Default: latest stable release.
  --prefix DIR      Install directory (default: ~/.local/bin)
  --base-url URL    Read the assets and SHA256SUMS from here instead of the
                    release download URL. Accepts file:// for assets already on
                    disk. Requires --version.
  --dry-run         Resolve, download to a temp dir, verify checksums; do not install
  -h, --help        Show help

Environment:
  TORIO_REPO        owner/name of the repository holding the assets.

This installer authenticates to nothing. Where the repository is not public,
fetch the assets with a tool that does hold your credentials and point the
installer at them:

  gh release download vX.Y.Z -D /tmp/torio-rel
  install.sh --version X.Y.Z --base-url file:///tmp/torio-rel

Checksum verification is identical on both paths.
EOF
}

log() { printf '%s\n' "$*" >&2; }
die() { log "install.sh: $*"; exit 1; }

require_platform() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  if [[ "$os" != "Darwin" || "$arch" != "arm64" ]]; then
    die "unsupported platform ${os}/${arch}; Torio supports only Darwin/arm64"
  fi
}

require_tools() {
  command -v curl >/dev/null 2>&1 || die "curl is required"
  command -v tar >/dev/null 2>&1 || die "tar is required"
  if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
    die "shasum or sha256sum is required"
  fi
}

sha256_file() {
  local file="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    sha256sum "$file" | awk '{print $1}'
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version)
        [[ $# -ge 2 ]] || die "--version requires an argument"
        VERSION="$2"
        shift 2
        ;;
      --prefix)
        [[ $# -ge 2 ]] || die "--prefix requires an argument"
        PREFIX="$2"
        shift 2
        ;;
      --dry-run)
        DRY_RUN=1
        shift
        ;;
      --base-url)
        # Directory or URL prefix containing the assets and SHA256SUMS. Used by
        # the tests, and by anyone installing from assets they already fetched
        # themselves — the supported route for a repository this installer
        # cannot read anonymously.
        [[ $# -ge 2 ]] || die "--base-url requires an argument"
        BASE_URL="$2"
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1"
        ;;
    esac
  done
}

resolve_version() {
  if [[ -n "$VERSION" ]]; then
    printf '%s\n' "$VERSION"
    return
  fi
  if [[ -n "$BASE_URL" ]]; then
    die "--version is required when using --base-url"
  fi
  # No python3 dependency: parse GitHub Releases JSON with sed.
  local payload tag
  payload="$(curl -fsSL "https://api.github.com/repos/${REPO_DEFAULT}/releases/latest")" \
    || die "failed to fetch latest release metadata for ${REPO_DEFAULT}; a repository this installer cannot read anonymously answers 404 here — fetch the assets yourself (gh release download) and pass --version with --base-url file://…"
  tag="$(printf '%s\n' "$payload" \
    | sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\(v[^"]*\)".*/\1/p' \
    | head -n1)"
  [[ -n "$tag" ]] || die "could not parse tag_name from latest release metadata"
  [[ "$tag" == v* ]] || die "latest release tag is not v*: ${tag}"
  printf '%s\n' "${tag#v}"
}

download() {
  # usage: download URL DEST
  local url="$1" dest="$2"
  if [[ "$url" == file://* ]]; then
    local src="${url#file://}"
    cp "$src" "$dest"
    return
  fi
  if [[ -f "$url" ]]; then
    cp "$url" "$dest"
    return
  fi
  curl -fsSL "$url" -o "$dest"
}

asset_urls() {
  local version="$1"
  local name="torio_${version}_darwin_arm64.tar.gz"
  if [[ -n "$BASE_URL" ]]; then
    local base="${BASE_URL%/}"
    printf '%s\n' "${base}/${name}"
    printf '%s\n' "${base}/SHA256SUMS"
    return
  fi
  local tag="v${version}"
  local base="https://github.com/${REPO_DEFAULT}/releases/download/${tag}"
  printf '%s\n' "${base}/${name}"
  printf '%s\n' "${base}/SHA256SUMS"
}

verify_sums() {
  local sums="$1" archive="$2"
  local want got name
  name="$(basename "$archive")"
  want="$(awk -v n="$name" '$2 == n {print $1; found=1} END{exit !found}' "$sums")" \
    || die "SHA256SUMS has no entry for ${name}"
  got="$(sha256_file "$archive")"
  if [[ "$want" != "$got" ]]; then
    die "checksum mismatch for ${name}: want ${want}, got ${got}"
  fi
  log "checksum ok: ${name}"
}

install_from_archive() {
  local archive="$1" prefix="$2"
  local tmp extract
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/torio-install.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN
  extract="${tmp}/extract"
  mkdir -p "$extract"
  tar -xzf "$archive" -C "$extract"
  [[ -f "${extract}/torio" ]] || die "archive missing torio binary"
  mkdir -p "$prefix"
  # Install by atomic rename after copy to temp in prefix.
  local staged="${prefix}/.torio.new.$$"
  cp "${extract}/torio" "$staged"
  chmod 755 "$staged"
  mv -f "$staged" "${prefix}/torio"
  log "installed ${prefix}/torio"
}

main() {
  parse_args "$@"
  require_platform
  require_tools

  local version archive_url sums_url
  version="$(resolve_version)"
  log "version=${version}"
  log "prefix=${PREFIX}"
  # do not log environment

  {
    read -r archive_url
    read -r sums_url
  } < <(asset_urls "$version")

  local work
  work="$(mktemp -d "${TMPDIR:-/tmp}/torio-dl.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$work'" EXIT

  local archive="${work}/$(basename "${archive_url}")"
  local sums="${work}/SHA256SUMS"
  download "$archive_url" "$archive"
  download "$sums_url" "$sums"
  verify_sums "$sums" "$archive"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "dry-run: checksum verified; not installing"
    printf '%s\n' "${PREFIX}/torio"
    exit 0
  fi

  install_from_archive "$archive" "$PREFIX"
  printf '%s\n' "${PREFIX}/torio"
  log "Add to PATH if needed:"
  log "  export PATH=\"${PREFIX}:\$PATH\""
}

# Allow sourcing for tests: TORIO_INSTALL_LIB=1 skips main.
if [[ "${TORIO_INSTALL_LIB:-0}" != "1" ]]; then
  main "$@"
fi
