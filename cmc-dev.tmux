#!/usr/bin/env bash
# Local dev entry point — builds from source, no download.
# Usage: add to ~/.tmux.conf:
#   run-shell ~/src/claude-mission-control/cmc-dev.tmux

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${CURRENT_DIR}/bin/cmc"

# Always rebuild if any Go source is newer than the binary
if [ ! -f "$BINARY" ] || [ -n "$(find "$CURRENT_DIR" -name '*.go' -newer "$BINARY" 2>/dev/null | head -1)" ]; then
    cd "$CURRENT_DIR" && make build 2>/dev/null || true
fi

# Auto-install Claude Code hooks (idempotent)
"$BINARY" setup 2>/dev/null || true

# <prefix> C-Space → fullscreen popup
tmux set -g popup-border-lines rounded
tmux set -g popup-border-style "fg=#555555"

tmux bind-key C-Space display-popup -E -w 100% -h 100% -e CLAUDE_TUI_FULLSCREEN=1 "$BINARY"

# Ctrl-Tab (prefix-less, tmux 3.5+) → normal popup
tmux bind-key -n C-Tab display-popup -E -w 80% -h 70% "$BINARY"
