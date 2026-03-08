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

# Ctrl-Space (prefix-less) → popup with active pane selected (zoom state from prefs)
tmux bind-key -n C-Space run-shell "$BINARY popup --select-active"

# Ctrl-Tab (prefix-less) → popup, skip current pane, rotate to next YOUR TURN
tmux bind-key -n C-Tab run-shell "$BINARY popup --rotate-next"
