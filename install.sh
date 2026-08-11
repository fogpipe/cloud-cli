#!/bin/sh
# fpcloud installer.
#
#   curl -fsSL https://raw.githubusercontent.com/fogpipe/cloud-cli/main/install.sh | sh
#
# Env overrides:
#   FPCLOUD_VERSION      version to install (default: latest release), e.g. v0.57.0
#   FPCLOUD_INSTALL_DIR  install directory (default: /usr/local/bin, else ~/.local/bin)
#   GITHUB_TOKEN         token for downloading while the repo is private
set -eu

REPO="fogpipe/cloud-cli"

err() { printf 'error: %s\n' "$1" >&2; exit 1; }

# curl with optional auth (needed only while the repo/release assets are private).
fetch() {
  # $1 = url, $2 = output file ("-" for stdout)
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    set -- "$1" "$2" -H "Authorization: Bearer $GITHUB_TOKEN"
    url=$1; out=$2; shift 2
    if [ "$out" = "-" ]; then curl -fsSL "$@" "$url"; else curl -fsSL "$@" -o "$out" "$url"; fi
  else
    if [ "$2" = "-" ]; then curl -fsSL "$1"; else curl -fsSL -o "$2" "$1"; fi
  fi
}

# --- detect platform ---
os=$(uname -s)
case "$os" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) err "unsupported OS: $os (fpcloud ships macOS and Linux binaries)" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) err "unsupported architecture: $arch" ;;
esac
asset="fpcloud-${os}-${arch}"

# --- resolve version ---
version="${FPCLOUD_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  version=$(fetch "https://api.github.com/repos/${REPO}/releases/latest" - \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$version" ] || err "could not resolve the latest release (private repo? set GITHUB_TOKEN)"
fi
base="https://github.com/${REPO}/releases/download/${version}"

# --- download + verify ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf 'Downloading fpcloud %s (%s/%s)...\n' "$version" "$os" "$arch"
fetch "${base}/${asset}" "${tmp}/${asset}" || err "download failed for ${asset} at ${version}"
if fetch "${base}/${asset}.sha256" "${tmp}/${asset}.sha256" 2>/dev/null; then
  ( cd "$tmp" && if command -v sha256sum >/dev/null 2>&1; then sha256sum -c "${asset}.sha256"; \
    else shasum -a 256 -c "${asset}.sha256"; fi ) >/dev/null || err "checksum verification failed"
  printf 'Checksum verified.\n'
else
  printf 'warning: no checksum published for this release; skipping verification.\n' >&2
fi
chmod +x "${tmp}/${asset}"

# --- install ---
dir="${FPCLOUD_INSTALL_DIR:-/usr/local/bin}"
if [ ! -d "$dir" ] || [ ! -w "$dir" ]; then
  if [ -w "$(dirname "$dir")" ] 2>/dev/null; then :; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir" 2>/dev/null || true
if [ -w "$dir" ]; then
  mv "${tmp}/${asset}" "${dir}/fpcloud"
else
  printf 'Installing to %s requires sudo...\n' "$dir"
  sudo mv "${tmp}/${asset}" "${dir}/fpcloud"
fi

printf '\n\342\234\223 Installed fpcloud %s to %s/fpcloud\n' "$version" "$dir"
case ":$PATH:" in
  *":$dir:"*) : ;;
  *) printf '  Add it to your PATH:  export PATH="%s:$PATH"\n' "$dir" ;;
esac
printf '  Next:  fpcloud login   # then: fpcloud org use / project use / app deploy\n'
