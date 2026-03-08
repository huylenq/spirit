# claude-mission-control (cmc)

Go TUI for monitoring Claude Code sessions across tmux panes.

## Build

After editing Go source files, always rebuild the binary:

```sh
make build
```

Binary output: `bin/cmc`

## Project structure

- `cmd/cmc/` - main entrypoint
- `internal/ui/` - Bubble Tea TUI (list, preview, styles)
- `internal/claude/` - session discovery and parsing
- `internal/daemon/` - background daemon (polls sessions, serves clients)
- `internal/tmux/` - tmux API wrapper
- `internal/app/` - Bubble Tea app model (update, view, messages)
- `hooks/claude-status.sh` - Legacy hook script (kept for compatibility, replaced by `cmc _hook`)

## Troubleshooting TUI rendering

When debugging layout or rendering issues, capture a text screenshot of the TUI:

```sh
./bin/cmc capture              # auto-detect terminal size (200x50 default)
./bin/cmc capture 160x40       # render at specific COLSxROWS
```

This does a headless render using the same `View()` code as the live TUI, with ANSI stripped. Works outside tmux as long as the daemon is running. Use it to inspect exactly what the TUI would display at a given resolution without needing to open the popup.

## Claude Code hooks

`cmc setup` patches `~/.claude/settings.json` to call `cmc _hook <type>` for each hook event.
The hook handler is built into the binary — no external shell script needed.
Hooks are identified by the `#cmc-hook` marker in the command string for migration.
