#!/bin/sh
# Terminal installer: use bundled binaries offline, or a pinned GitHub release.
set -eu
buildheadway_repository=${BUILDHEADWAY_RELEASE_REPOSITORY:-${CODENTRAIL_RELEASE_REPOSITORY:-${FORGE_RELEASE_REPOSITORY:-@RELEASE_REPOSITORY@}}}
buildheadway_version=${BUILDHEADWAY_VERSION:-${CODENTRAIL_VERSION:-${FORGE_VERSION:-latest}}}
buildheadway_tmp=
trap 'if [ -n "$buildheadway_tmp" ]; then rm -rf -- "$buildheadway_tmp"; fi' EXIT HUP INT TERM
buildheadway_dir=
# A piped script has $0=sh; never trust arbitrary binaries in the caller's cwd.
case "$0" in
  */install.sh|install.sh) if [ -f "$0" ]; then buildheadway_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd); fi ;;
esac
case "$(uname -s)" in
  Darwin) buildheadway_os=darwin ;;
  Linux) buildheadway_os=linux ;;
  *) echo "Use install.ps1 from Windows PowerShell." >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) buildheadway_arch=amd64 ;;
  aarch64|arm64) buildheadway_arch=arm64 ;;
  *) echo "BuildHeadway requires x64 or ARM64." >&2; exit 2 ;;
esac
if [ "$buildheadway_os" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then buildheadway_arch=arm64; fi
buildheadway_name="buildheadway-$buildheadway_os-$buildheadway_arch"
buildheadway_relative="bin/$buildheadway_name"
if [ -z "$buildheadway_dir" ] || [ ! -f "$buildheadway_dir/SHA256SUMS" ]; then
  if ! printf '%s\n' "$buildheadway_repository" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$'; then
    echo "This installer has no published release repository yet. Use the downloaded release's install.sh, or set BUILDHEADWAY_RELEASE_REPOSITORY=OWNER/REPOSITORY after publication." >&2
    exit 2
  fi
  if ! command -v curl >/dev/null 2>&1; then echo "Install curl first, or use the offline release." >&2; exit 2; fi
  buildheadway_tmp=$(mktemp -d "${TMPDIR:-/tmp}/buildheadway-install.XXXXXXXX")
  buildheadway_dir=$buildheadway_tmp
  mkdir -p "$buildheadway_dir/bin"
  if [ "$buildheadway_version" = latest ]; then
    buildheadway_url=$(curl --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 120 -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$buildheadway_repository/releases/latest")
    buildheadway_version=${buildheadway_url##*/}
  fi
  if ! printf '%s\n' "$buildheadway_version" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$'; then echo "Invalid release version." >&2; exit 2; fi
  buildheadway_version="v${buildheadway_version#v}"
  buildheadway_base="https://github.com/$buildheadway_repository/releases/download/$buildheadway_version"
  printf 'Downloading BuildHeadway %s for %s/%s...\n' "$buildheadway_version" "$buildheadway_os" "$buildheadway_arch"
  curl --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 -fsSL "$buildheadway_base/SHA256SUMS" -o "$buildheadway_dir/SHA256SUMS"
  curl --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 180 -fsSL "$buildheadway_base/$buildheadway_name" -o "$buildheadway_dir/$buildheadway_relative"
  export BUILDHEADWAY_RELEASE_REPOSITORY="$buildheadway_repository"
fi
buildheadway_binary="$buildheadway_dir/$buildheadway_relative"
if [ ! -f "$buildheadway_binary" ]; then echo "The native BuildHeadway application is missing from this release." >&2; exit 2; fi
buildheadway_expected=$(awk -v file="$buildheadway_relative" '$2 == file {hash=$1; count++} END {if (count != 1) exit 1; print hash}' "$buildheadway_dir/SHA256SUMS") || { echo "BuildHeadway failed integrity verification: invalid manifest." >&2; exit 2; }
if command -v sha256sum >/dev/null 2>&1; then
  buildheadway_actual=$(sha256sum "$buildheadway_binary" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  buildheadway_actual=$(shasum -a 256 "$buildheadway_binary" | awk '{print $1}')
else
  echo "A SHA-256 verification tool is required (sha256sum or shasum)." >&2; exit 2
fi
if [ -z "$buildheadway_expected" ] || [ "$buildheadway_actual" != "$buildheadway_expected" ]; then echo "BuildHeadway failed integrity verification. Download the release again." >&2; exit 2; fi
chmod +x "$buildheadway_binary"
"$buildheadway_binary" install "$@"
