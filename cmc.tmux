#!/usr/bin/env bash
# TPM entry point — downloads prebuilt binary from GitHub Releases, then binds keys.

set -euo pipefail

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${CURRENT_DIR}/bin/cmc"
REPO="huylenq/claude-mission-control"

get_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) echo "unsupported arch: $arch" >&2; return 1 ;;
    esac
    case "$os" in
        darwin|linux) ;;
        *) echo "unsupported os: $os" >&2; return 1 ;;
    esac
    echo "${os}_${arch}"
}

install_binary() {
    local platform tag url tmpdir
    platform="$(get_platform)" || return 1

    # Get latest release tag
    tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4)" || return 1
    [ -z "$tag" ] && return 1

    # Check if current binary matches this version
    if [ -f "$BINARY" ] && "$BINARY" --help 2>&1 | grep -qF "${tag#v}"; then
        return 0
    fi

    url="https://github.com/${REPO}/releases/download/${tag}/claude-mission-control_${tag#v}_${platform}.tar.gz"
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT

    curl -fsSL "$url" | tar xz -C "$tmpdir" || return 1
    mkdir -p "${CURRENT_DIR}/bin"
    mv "$tmpdir/cmc" "$BINARY"
    chmod +x "$BINARY"
}

# Install if binary missing or outdated
if [ ! -f "$BINARY" ]; then
    install_binary 2>/dev/null || true
fi

# Fallback: build from source if download failed and Go is available
if [ ! -f "$BINARY" ] && command -v go >/dev/null 2>&1; then
    cd "$CURRENT_DIR" && make build 2>/dev/null || true
fi

[ -f "$BINARY" ] || { echo "cmc: binary not available" >&2; exit 0; }

# Auto-install Claude Code hooks (idempotent — skips if already up to date)
"$BINARY" setup 2>/dev/null || true

# <prefix> C-Space → fullscreen popup (borderless — TUI draws its own frame)
tmux bind-key C-Space display-popup -B -E -w 100% -h 100% -e CLAUDE_TUI_FULLSCREEN=1 "$BINARY"

# Ctrl-Tab (prefix-less, tmux 3.5+) → normal popup
tmux bind-key -n C-Tab display-popup -B -E -w 80% -h 70% "$BINARY"
