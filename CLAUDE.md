# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# claude-mission-control (cmc)

Go TUI for monitoring and orchestrating Claude Code sessions across tmux panes.

## Build & Run

```sh
make          # build AND restart daemon (default target)
make build    # build only → bin/cmc
make clean    # remove bin/
```

**Always run `make` (not `make build`) after code changes** — it builds AND restarts the daemon so changes take effect.

Binary output: `bin/cmc`

## Subcommands

```sh
cmc                    # Launch TUI (requires $TMUX; auto-starts daemon)
cmc popup              # Open TUI in tmux display-popup
cmc daemon             # Start background daemon
cmc daemon --check     # Exit 0 if daemon running
cmc daemon --stop      # Stop daemon
cmc setup              # Install Claude Code hooks into ~/.claude/settings.json
cmc _hook <type>       # Handle a hook event (called by Claude Code hooks)
cmc eval -e '<lua>'    # Evaluate inline Lua script against daemon
cmc eval <file.lua>    # Evaluate Lua file
cmc orchestrator register|unregister <id>
cmc capture [COLSxROWS]  # Headless TUI screenshot (for debugging layout)
cmc dev                # fzf worktree picker (dev workflow)
```

## Daemon Runtime Files

```
~/.cache/cmc/daemon.sock   Unix socket
~/.cache/cmc/daemon.pid    PID file
~/.cache/cmc/daemon.log    Log output
~/.cache/cmc/prefs         Key=value prefs (e.g. fullscreen=true)
~/.cache/cmc/copilot/      Copilot workspace (bootstrap files, events/, memory/, chat_history.json)
```

## Architecture

### Process Model

```
cmc (TUI client)  ←──Unix socket──→  cmc daemon  ←──polls──→  tmux / Claude session files
cmc _hook         ──nudge─────────→  cmc daemon
cmc eval          ──Lua RPC────────→  cmc daemon
```

The daemon is a long-lived process that polls Claude sessions every ~1s and pushes updates over a Unix socket to all connected TUI clients. It auto-shuts down after 10 minutes with no clients.

### Package Layout

- **`cmd/cmc/`** — Entrypoint. Switch on `os.Args[1]` to subcommands. All subcommand logic lives here (setup, popup, dev, eval, capture, orchestrator).
- **`internal/daemon/`** — Daemon process and client. `daemon.go` owns the `Daemon` struct with all goroutines. Split by concern: `daemon_poll.go`, `daemon_lifecycle.go`, `daemon_synthesis.go`, `daemon_resolve.go`. Server handlers split into `server_session.go`, `server_transcript.go`, `server_hooks.go`, etc. `protocol.go` defines all request/response JSON types and constants. `client.go` wraps the socket connection.
- **`internal/app/`** — Bubble Tea app model. `Model` (defined across multiple files) holds all TUI state. `update.go` is the main `Update()` dispatcher. Commands split by domain: `command_session.go`, `command_backlog.go`, `command_relay.go`, `command_view.go`, `command_prefs.go`, `command_eval.go`. Views: `view.go`, `view_panels.go`, `view_overlays.go`.
- **`internal/ui/`** — Reusable TUI components. `sidebar.go` + `sidebar_*.go` (nav, backlog, project, view). `detail.go` + `detail_*.go` (messages, hooks, scroll, view). `minimap.go` + `minimap_*.go`. `copilot.go` + `copilot_view.go` (floating chat overlay with streaming, tool confirmations, scroll). Standalone: `search.go`, `palette.go`, `overlay.go`, `highlight.go`, `usagebar.go`, `relay.go`, etc.
- **`internal/claude/`** — Session discovery and parsing. `discover.go` finds sessions from status files. `session.go` defines `ClaudeSession`. `transcript.go` parses JSONL transcripts. `hook.go` handles `cmc _hook` events. `status.go` manages status file I/O. `backlog.go`, `macros.go`, `usage.go`, `worktree.go`, `synthesize.go`, `digest.go`.
- **`internal/scripting/`** — Lua scripting via `gopher-lua`. `eval.go` is the entry point. API registered per domain: `api_sessions.go`, `api_send.go`, `api_lifecycle.go`, `api_features.go`, `api_orchestrator.go`, `api_util.go`, `api_context.go`. `sandbox.go` creates the restricted VM. `convert.go` handles Lua↔Go value conversion.
- **`internal/tmux/`** — tmux API wrapper (`api.go`).
- **`internal/copilot/`** — Copilot AI companion. `workspace.go` manages the `~/.cache/cmc/copilot/` workspace (bootstrap files, CLAUDE.md generation). `journal.go` is an append-only NDJSON event log. `memory.go` provides two-tier memory (long-term `MEMORY.md` + daily logs with keyword search + MMR reranking). `prompt.go` builds context preambles (sessions, events, memory, digest — capped at 12k chars). `events.go` defines event types.
- **`internal/spirit/`** — Spirit animal name generation for session avatars.

### Key Data Flow

1. **Hook events** (`cmc _hook <type>`): Claude Code calls this binary; it writes a status file to disk and sends a `nudge` over the socket to trigger an immediate daemon poll.
2. **Daemon poll**: Reads all status files → builds `[]ClaudeSession` → broadcasts to subscribers via the socket.
3. **TUI client**: Receives session list, renders sidebar + detail panel. Sends commands (send message, kill, synthesize, etc.) back to daemon via RPC requests.
4. **Lua eval** (`cmc eval`): Connects to daemon socket, executes sandboxed Lua with a Go-backed API that proxies requests to the daemon.

### Daemon–Client Protocol

Newline-delimited JSON over Unix socket. `protocol.go` defines all request types (`Req*` constants) and response types (`Resp*` constants) with their data payloads. The `subscribe` request initiates a push stream; all other requests are single request/response.

### App State Machine

`Model.state` in `internal/app/` controls which key handler is active. States include `StateNormal`, `StateSearching`, `StateKillConfirm`, `StatePromptRelay`, `StateQueueRelay`, `StatePalette`, `StateMacro`, `StateNoteEdit`, `StatePrefsEditor`, `StateMinimapSettings`, `StateCopilot`, `StateCopilotConfirm`, etc.

## Troubleshooting TUI Rendering

```sh
./bin/cmc capture              # auto-detect terminal size (200x50 default)
./bin/cmc capture 160x40       # render at specific COLSxROWS
```

Headless render using the same `View()` code, with ANSI stripped. Works outside tmux as long as the daemon is running.

## Claude Code Hooks

`cmc setup` patches `~/.claude/settings.json` to register `cmc _hook <type> #cmc-hook` for each event type. The `#cmc-hook` marker identifies cmc-managed hooks for future migration/updates without touching unrelated hooks.

## Copilot

Persistent AI companion inside mission control, toggled with `gc`. Renders as a floating indigo-bordered overlay in the bottom-right corner. The daemon runs a `claude` CLI subprocess per prompt with a 3-minute timeout.

**Key architecture:**
- **Context preamble** (`internal/copilot/prompt.go`): Every prompt is injected with live session states, recent events (last 50), workspace digest, and long-term memory — capped at 12k chars.
- **Event journal** (`~/.cache/cmc/copilot/events/YYYY-MM-DD.ndjson`): Append-only log of all daemon activity (session spawns, status changes, git commits, etc.). Feeds the copilot's situational awareness.
- **Two-tier memory**: Evergreen `MEMORY.md` + temporal-decayed `memory/YYYY-MM-DD.md` daily logs, with keyword search and MMR reranking.
- **History persistence**: In-memory `[]CopilotHistoryMsg` in daemon, serialized to `~/.cache/cmc/copilot/chat_history.json`. Survives TUI and daemon restarts. Last 200 messages kept.
- **Protocol**: `copilot_chat`, `copilot_cancel`, `copilot_status`, `copilot_history`, `copilot_clear_history` request types. Streaming responses via `copilot_stream` with chunk types: `text_delta`, `thought`, `tool_call`, `tool_update`, `plan`, `usage`, `done`, `error`, `confirm`.
- **Tool confirmations**: `StateCopilotConfirm` state — user approves (`y`) or rejects (`n`) tool calls before execution.

## Lua Scripting

The eval VM is sandboxed (base/table/string/math only — no os/io/debug). Scripts are stateless per invocation. The last expression is JSON-serialized to stdout. Use `cmc --agent-help` for the full Lua API reference.
