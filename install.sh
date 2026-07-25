#!/bin/sh
set -eu

REPO="thedavidweng/tg-drive-cli"
BINARY="td"
CASK="thedavidweng/tap/tg-drive-cli"

step() { printf '==> %s
' "$1"; }
die() { printf 'ERROR: %s
' "$1" >&2; exit 1; }

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Darwin) platform="darwin" ;;
  Linux) platform="linux" ;;
  *) die "Unsupported OS: $os. Use install.ps1 on Windows." ;;
esac

case "$arch" in
  x86_64|amd64) goarch="x86_64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) die "Unsupported architecture: $arch" ;;
esac

resolve_version() {
  curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/'
}

install_binary() {
  version="$1"
  case "$platform" in
    darwin) asset="${BINARY}_darwin_universal.tar.gz" ;;
    linux) asset="${BINARY}_linux_${goarch}.tar.gz" ;;
  esac
  url="https://github.com/$REPO/releases/download/$version/$asset"
  bin_dir="${TD_INSTALL_DIR:-$HOME/.local/bin}"
  mkdir -p "$bin_dir"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT INT TERM
  step "Downloading $asset"
  curl -fsSL "$url" -o "$tmp_dir/$asset"
  tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
  chmod +x "$tmp_dir/$BINARY"
  mv -f "$tmp_dir/$BINARY" "$bin_dir/$BINARY"
  step "Installed $($bin_dir/$BINARY version 2>/dev/null || echo $version)"
}

case "${1:-}" in
  uninstall)
    rm -f "${TD_INSTALL_DIR:-$HOME/.local/bin}/$BINARY"
    step "Uninstalled. Config remains under ~/.config/tg-drive-cli."
    exit 0
    ;;
  --help|-h)
    echo "Usage: install.sh [uninstall]"
    exit 0
    ;;
esac

if command -v brew >/dev/null 2>&1; then
  step "Installing via Homebrew Cask"
  brew tap thedavidweng/tap 2>/dev/null || true
  brew install --cask "$CASK"
else
  version="$(resolve_version)"
  [ -n "$version" ] || die "Could not resolve latest version."
  install_binary "$version"
fi

step "Run 'td auth login' to get started."
