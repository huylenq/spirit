# Development Guide

## Setup

```sh
git clone https://github.com/huylenq/claude-mission-control ~/src/claude-mission-control
cd ~/src/claude-mission-control
```

Add to `~/.tmux.conf`:

```bash
run-shell ~/src/claude-mission-control/cmc-dev.tmux
```

Then reload: `tmux source-file ~/.tmux.conf`

`cmc-dev.tmux` rebuilds the binary if any `.go` source is newer than the binary,
installs Claude Code hooks, and binds the keybindings below.

## Build

```sh
make        # build + restart daemon (default target)
make build  # build only
```

Binary output: `bin/cmc`

## Daemon isolation per worktree

Each git worktree runs its own daemon on an independent socket derived from the
repo root path:

```
/tmp/cmc-<sha256[:12] of repo root>.sock
/tmp/cmc-<sha256[:12] of repo root>.pid
```

This happens automatically — the binary detects its own location with
`os.Executable()`, resolves the git repo root, and hashes it. No configuration
needed. `make` in any worktree builds and restarts only that worktree's daemon.

Binaries installed globally (TPM, PATH) that are not inside a git repo fall back
to `~/.cache/cmc/daemon.sock`.

## Concurrent agent development

When multiple Claude Code agents work on different branches simultaneously, use
git worktrees so each agent gets an isolated build, daemon, and socket:

```sh
git worktree add .worktrees/feat-x -b feat-x
```

Each worktree:
- builds its own `bin/cmc` via `make`
- runs its own daemon on its own `/tmp/cmc-*.sock`
- can be launched independently without interfering with other worktrees

## Keybindings

`cmc-dev.tmux` registers four bindings:

| Binding | Action |
|---|---|
| `C-Space` | Open CMC popup, auto-select current pane |
| `C-Tab` | Open CMC popup, rotate to next YOUR TURN session |
| `<prefix> C-Space` | **Dev picker**: fzf over worktrees → launch chosen worktree's TUI |
| `<prefix> C-Tab` | **Dev picker**: same, rotate-next mode |

The dev picker (`<prefix>` variants) lists all git worktrees with daemon status:

```
main                             ●
feat-x                           ○
feat-macros                      ●  (no binary — run make)
```

`●` = daemon running, `○` = stopped (auto-starts on selection).

On selection the picker execs into the chosen worktree's `bin/cmc`, replacing
itself in the same popup window. The TUI takes over seamlessly.

The picker always appears, even with a single worktree — so Huy always knows which build he's launching against.

## TUI layout debugging

Capture a headless text render of the TUI at any resolution:

```sh
./bin/cmc capture          # 200×50 default
./bin/cmc capture 160x40   # specific size
```

Works outside tmux as long as the daemon is running. Useful for inspecting
layout without opening the popup.

## Daemon log

Always at `~/.cache/cmc/daemon.log` regardless of which worktree's daemon is running:

```sh
tail -f ~/.cache/cmc/daemon.log
```

## Project structure

```
cmd/cmc/            main entrypoint, CLI subcommands
internal/app/       Bubble Tea model, update, view, keymap
internal/claude/    session discovery, parsing, hooks, synthesis
internal/daemon/    background daemon, client, protocol, workdir socket logic
internal/tmux/      tmux API wrapper
internal/ui/        shared styles, list component, macro editor
internal/scripting/ Lua eval engine
docs/               documentation
hooks/              legacy hook script (kept for compatibility)
cmc.tmux            TPM entry point (production)
cmc-dev.tmux        local dev entry point
```

## Adding a new RPC command

1. Add a `Req*` constant to `internal/daemon/protocol.go`
2. Add the request/response data structs if needed (same file)
3. Handle the request in `internal/daemon/server.go` (`handleRPC` switch)
4. Add a client method to `internal/daemon/client.go`
5. Wire it into the app in `internal/app/update.go` or a new command file
