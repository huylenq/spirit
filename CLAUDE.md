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

## Claude Code hooks

`cmc setup` patches `~/.claude/settings.json` to call `cmc _hook <type>` for each hook event.
The hook handler is built into the binary — no external shell script needed.
Hooks are identified by the `#cmc-hook` marker in the command string for migration.
