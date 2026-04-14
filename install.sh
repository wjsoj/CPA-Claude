#!/usr/bin/env bash
#
# CPA-Claude installer
# -----------------------------------------------------------------------------
# Installs or upgrades the cpa-claude binary from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/wjsoj/CPA-Claude/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/wjsoj/CPA-Claude/main/install.sh | bash -s -- --version v0.1.0
#   curl -fsSL https://raw.githubusercontent.com/wjsoj/CPA-Claude/main/install.sh | bash -s -- --prefix ~/.local
#
# Options:
#   --version <tag>   Install this exact tag (default: latest release).
#   --prefix  <dir>   Install prefix; binary goes to <dir>/bin (default: /usr/local).
#   --bin-dir <dir>   Override binary directory directly.
#
# Environment variables (same as flags, flags win):
#   CPA_VERSION, CPA_PREFIX, CPA_BIN_DIR, GITHUB_TOKEN (for rate limits)
# -----------------------------------------------------------------------------

set -euo pipefail

REPO="wjsoj/CPA-Claude"
BIN_NAME="cpa-claude"

VERSION="${CPA_VERSION:-latest}"
PREFIX="${CPA_PREFIX:-/usr/local}"
BIN_DIR="${CPA_BIN_DIR:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --prefix)  PREFIX="$2";  shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    -h|--help)
      sed -n '3,20p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -z "$BIN_DIR" ] && BIN_DIR="${PREFIX%/}/bin"

msg()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || err "missing dependency: $1"; }
need curl
need tar

# ---- detect OS / ARCH ----
OS="$(uname -s)"
case "$OS" in
  Linux)  OS_TAG=linux ;;
  Darwin) OS_TAG=darwin ;;
  *) err "unsupported OS: $OS (use Windows zip manually from Releases)" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH_TAG=amd64 ;;
  aarch64|arm64) ARCH_TAG=arm64 ;;
  *) err "unsupported arch: $ARCH" ;;
esac

# ---- resolve tag ----
auth_header=()
[ -n "${GITHUB_TOKEN:-}" ] && auth_header=(-H "Authorization: Bearer $GITHUB_TOKEN")

if [ "$VERSION" = "latest" ]; then
  msg "resolving latest release..."
  TAG="$(curl -fsSL "${auth_header[@]}" \
    "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$TAG" ] || err "could not resolve latest tag"
else
  TAG="$VERSION"
fi
msg "installing $REPO@$TAG → $BIN_DIR/$BIN_NAME ($OS_TAG/$ARCH_TAG)"

# Tag is like "v0.1.0"; GoReleaser archive strips the leading "v".
TRIMMED="${TAG#v}"
ASSET="cpa-claude_${TRIMMED}_${OS_TAG}_${ARCH_TAG}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"
SUM_URL="https://github.com/${REPO}/releases/download/${TAG}/checksums.txt"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

msg "downloading $URL"
curl -fsSL "${auth_header[@]}" -o "$TMP/$ASSET" "$URL" \
  || err "download failed; check tag and asset name"

msg "verifying checksum"
if curl -fsSL "${auth_header[@]}" -o "$TMP/checksums.txt" "$SUM_URL"; then
  expected="$(grep " ${ASSET}\$" "$TMP/checksums.txt" | awk '{print $1}')"
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    else
      actual="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
    fi
    [ "$actual" = "$expected" ] || err "checksum mismatch ($actual != $expected)"
  else
    warn "no checksum entry for $ASSET — skipping verification"
  fi
else
  warn "could not fetch checksums.txt — skipping verification"
fi

msg "extracting"
tar -xzf "$TMP/$ASSET" -C "$TMP"
[ -f "$TMP/$BIN_NAME" ] || err "extracted archive does not contain $BIN_NAME"

# ---- install ----
install_cmd=(install -m 0755 "$TMP/$BIN_NAME" "$BIN_DIR/$BIN_NAME")
if [ -w "$BIN_DIR" ] || { [ ! -d "$BIN_DIR" ] && [ -w "$(dirname "$BIN_DIR")" ]; }; then
  mkdir -p "$BIN_DIR"
  "${install_cmd[@]}"
else
  msg "sudo needed for $BIN_DIR"
  sudo mkdir -p "$BIN_DIR"
  sudo "${install_cmd[@]}"
fi

msg "installed:"
"$BIN_DIR/$BIN_NAME" --version || true

cat <<EOF

Next steps:

  1. curl -fsSL https://raw.githubusercontent.com/${REPO}/${TAG}/config.example.yaml -o config.yaml
     (edit config.yaml — at minimum set access_tokens and add OAuth files to auth_dir)

  2. $BIN_NAME --config config.yaml

Upgrade later by re-running this installer (it overwrites the binary in place).
EOF
