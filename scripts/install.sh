#!/usr/bin/env bash
# Verified Torio installer for the supported hosts: macOS on Apple Silicon and
# Linux on x86_64. The list is the same one internal/lima.profiles carries; a
# platform this installs for but the CLI has no pins for would produce a working
# binary that refuses every command.
# Downloads a release asset, verifies SHA256SUMS, then installs the host binary
# and its two Linux guest payloads into one prefix.
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
PREFIX=""
DRY_RUN=0
BASE_URL="" # override for tests, e.g. file:///tmp/assets

# Three streams of the same binary, installable at once.
#
#   stable  a release, installed as `torio`
#   dev     the build of the last commit on main, installed as `torio-dev`.
#           It is one release on a tag that moves, so there is no version to
#           name. It is published as a prerelease, which is what keeps
#           `releases/latest` (and therefore a stable install) pointing at the
#           last real release.
#   local   a build of somebody's working tree, installed as `torio-local`.
#           Nothing publishes it, so its archive is named with --base-url.
#
# Each gets its own prefix rather than sharing one. The two guest payloads are
# named by lima.Profile and cannot vary, so a second install in the same
# directory would overwrite the payloads the first put there and leave a host
# binary beside payloads built from another commit. Everything but stable is
# reached through a link, and `mcp install` resolves the link before taking the
# directory it copies payloads from, so a linked binary finds its own.
CHANNEL="stable"
DEV_TAG="dev"
LINK_DIR="$PREFIX_DEFAULT"
LINK=1

# The prefix and command name of a non-stable channel. Kept in one place: a
# channel whose prefix and link name disagreed would install one binary and
# expose another.
channel_prefix() {
  case "$1" in
    dev) printf '%s\n' "${HOME}/.local/share/torio-dev/bin" ;;
    local) printf '%s\n' "${HOME}/.local/share/torio-local/bin" ;;
    *) printf '%s\n' "$PREFIX_DEFAULT" ;;
  esac
}

channel_link_name() {
  case "$1" in
    dev) printf 'torio-dev\n' ;;
    local) printf 'torio-local\n' ;;
    *) printf '\n' ;;
  esac
}

usage() {
  cat <<'EOF'
Usage: install.sh [--channel stable|dev] [--version X.Y.Z] [--prefix DIR]
                  [--base-url URL] [--link-dir DIR] [--no-link] [--dry-run]

Installs Torio for this machine (Darwin/arm64 or Linux/x86_64) into a
user-writable prefix (default: ~/.local/bin).
Verifies SHA256SUMS before copying the binary. Does not use sudo and does not
modify shell startup files.

Options:
  --channel NAME    stable (default) installs a release as `torio`. dev installs
                    the build of the latest commit on main as `torio-dev`.
                    local installs an archive you built yourself, named with
                    --base-url, as `torio-local`. Each has its own prefix, so
                    all three can be installed at once. `make local` builds and
                    installs the local one for you.
  --version X.Y.Z   Install this version (without leading v). Default: latest stable release.
  --prefix DIR      Install directory (default: ~/.local/bin; other channels:
                    ~/.local/share/torio-<channel>/bin)
  --base-url URL    Read the assets and SHA256SUMS from here instead of the
                    release download URL. Accepts file:// for assets already on
                    disk. Requires --version.
  --link-dir DIR    Where a non-stable channel is linked (default: ~/.local/bin)
  --no-link         Install without linking the command
  --dry-run         Resolve, download to a temp dir, verify checksums; do not install
  -h, --help        Show help

A dev build is not a release. It carries whatever reached main, has passed the
pull-request gate and nothing more, and its checksums are verified the same way.

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

# PLATFORM is the "<goos>_<goarch>" fragment of the asset name, set by
# require_platform. It is derived from the running machine rather than accepted
# as a flag: an installer that let you ask for the wrong architecture would
# happily place a binary that cannot execute.
PLATFORM=""
GUEST_ARCH=""

require_platform() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  case "${os}/${arch}" in
    Darwin/arm64) PLATFORM="darwin_arm64"; GUEST_ARCH="arm64" ;;
    Linux/x86_64) PLATFORM="linux_amd64"; GUEST_ARCH="amd64" ;;
    *)
      die "unsupported platform ${os}/${arch}; Torio supports Darwin/arm64 and Linux/x86_64"
      ;;
  esac
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
      --channel)
        [[ $# -ge 2 ]] || die "--channel requires an argument"
        case "$2" in
          stable|dev|local) CHANNEL="$2" ;;
          *) die "unknown channel: $2 (want stable, dev or local)" ;;
        esac
        shift 2
        ;;
      --link-dir)
        [[ $# -ge 2 ]] || die "--link-dir requires an argument"
        LINK_DIR="$2"
        shift 2
        ;;
      --no-link)
        LINK=0
        shift
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

# The prefix and the link directory depend on the channel, so they are resolved
# after the arguments are parsed rather than at assignment: an explicit --prefix
# must win over the channel default, whatever order the two were given in.
apply_channel_defaults() {
  if [[ -z "$PREFIX" ]]; then
    PREFIX="$(channel_prefix "$CHANNEL")"
  fi
  if [[ "$CHANNEL" == "local" && -z "$BASE_URL" ]]; then
    die "--channel local installs an archive you built: name it with --base-url (and --version)"
  fi
}

resolve_version() {
  if [[ -n "$VERSION" ]]; then
    printf '%s\n' "$VERSION"
    return
  fi
  if [[ -n "$BASE_URL" ]]; then
    die "--version is required when using --base-url"
  fi
  if [[ "$CHANNEL" == "dev" ]]; then
    # The dev release is a prerelease, so `releases/latest` never names it and
    # the tag has to be read directly. Its version is not knowable from the tag
    # (the tag does not move with a version, the version moves with the commit),
    # so it is read back off the asset this host would install.
    local dev_payload dev_version
    dev_payload="$(curl -fsSL "https://api.github.com/repos/${REPO_DEFAULT}/releases/tags/${DEV_TAG}")" \
      || die "failed to fetch the ${DEV_TAG} release metadata for ${REPO_DEFAULT}; there may be no dev build published yet"
    dev_version="$(printf '%s\n' "$dev_payload" \
      | sed -n 's/^[[:space:]]*"name"[[:space:]]*:[[:space:]]*"torio_\(.*\)_'"${PLATFORM}"'\.tar\.gz".*/\1/p' \
      | head -n1)"
    [[ -n "$dev_version" ]] || die "the ${DEV_TAG} release has no ${PLATFORM} archive"
    printf '%s\n' "$dev_version"
    return
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
  local name="torio_${version}_${PLATFORM}.tar.gz"
  if [[ -n "$BASE_URL" ]]; then
    local base="${BASE_URL%/}"
    printf '%s\n' "${base}/${name}"
    printf '%s\n' "${base}/SHA256SUMS"
    return
  fi
  # Stable assets hang off the version tag they were cut from. Dev assets hang
  # off one tag that moves, so the version is in the file name only.
  local tag="v${version}"
  if [[ "$CHANNEL" == "dev" ]]; then
    tag="$DEV_TAG"
  fi
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
  local broker="torio-mcp-broker-linux-${GUEST_ARCH}"
  local relay="torio-mcp-connect-linux-${GUEST_ARCH}"
  # Validate the complete set before changing the prefix. die exits, which
  # skips the RETURN trap; remove the temp dir first.
  if [[ ! -f "${extract}/torio" || ! -f "${extract}/${broker}" || ! -f "${extract}/${relay}" ]]; then
    rm -rf "$tmp"
    die "archive missing torio binary or its Linux guest MCP payloads"
  fi
  mkdir -p "$prefix"
  local name staged
  # Publish torio last. A completed install therefore never exposes a new host
  # binary before both payloads it expects are present beside it.
  for name in "$broker" "$relay" torio; do
    staged="${prefix}/.${name}.new.$$"
    cp "${extract}/${name}" "$staged"
    chmod 755 "$staged"
    mv -f "$staged" "${prefix}/${name}"
  done
  log "installed ${prefix}/torio"
  log "installed ${prefix}/${broker}"
  log "installed ${prefix}/${relay}"
}

# Publish the build under the name an operator types. The link is replaced
# through a staged name, for the same reason the binaries are: a link that was
# removed and not yet recreated is a window in which the command does not
# resolve at all.
link_command() {
  local prefix="$1" dir="$2" name="$3"
  mkdir -p "$dir"
  local staged="${dir}/.${name}.new.$$"
  ln -sfn "${prefix}/torio" "$staged"
  mv -f "$staged" "${dir}/${name}"
  log "linked ${dir}/${name} -> ${prefix}/torio"
}

main() {
  parse_args "$@"
  apply_channel_defaults
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
  local path_dir="$PREFIX" link_name
  link_name="$(channel_link_name "$CHANNEL")"
  if [[ -n "$link_name" && "$LINK" -eq 1 ]]; then
    link_command "$PREFIX" "$LINK_DIR" "$link_name"
    path_dir="$LINK_DIR"
  fi
  printf '%s\n' "${PREFIX}/torio"
  log "Add to PATH if needed:"
  log "  export PATH=\"${path_dir}:\$PATH\""
}

# Allow sourcing for tests: TORIO_INSTALL_LIB=1 skips main.
if [[ "${TORIO_INSTALL_LIB:-0}" != "1" ]]; then
  main "$@"
fi
