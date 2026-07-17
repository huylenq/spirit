# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# spirit

Go TUI for monitoring Claude Code and Codex CLI sessions across tmux panes.

## Build & Run

```sh
make          # build AND restart daemon (default target)
make build    # build only → bin/spirit
make clean    # remove bin/
```

**Always run `make` (not `make build`) after code changes** — it builds AND restarts the daemon so changes take effect.

`make` output is short (≤4 lines on success). Do not pipe through `tail -N` for any N≥4 — the pipe blocks waiting for more lines that never arrive and the command hangs. Just run `make` plainly and read the full output.

Binary output: `bin/spirit`

## Subcommands

```sh
spirit                    # Launch TUI (requires $TMUX; auto-starts daemon)
spirit popup              # Open TUI in tmux display-popup
spirit daemon             # Start background daemon
spirit daemon --check     # Exit 0 if daemon running
spirit daemon --stop      # Stop daemon
spirit setup              # Install Claude Code and Codex hooks
spirit _hook <type>       # Handle a provider hook event
spirit eval -e '<lua>'    # Evaluate inline Lua script against daemon
spirit eval <file.lua>    # Evaluate Lua file
spirit mcp                # Stdio JSON-RPC MCP server over the daemon (for Hermes)
spirit mcp install        # Register mcp_servers.spirit globally in the Hermes config (--force replaces a differing entry)
spirit mcp status         # Report whether the Hermes config has the expected registration
spirit orchestrator register|unregister <id>
spirit capture [COLSxROWS]  # Headless TUI screenshot (for debugging layout)
spirit dev                # fzf worktree picker (dev workflow)
```

## Daemon Runtime Files

```
/tmp/spirit-<hash>.sock Unix socket (hash of the repo root the binary lives in; falls back to ~/.spirit/daemon.sock outside a git repo)
~/.spirit/daemon.pid    PID file
~/.spirit/daemon.log    Log output
~/.spirit/prefs         Key=value prefs (e.g. fullscreen=true)
~/.spirit/copilot/      Copilot workspace (bootstrap files, memory/, chat_history.json)
~/.spirit/ledger/       Perception ledger: signals-YYYY-MM-DD.ndjson day segments, attention.json, cursors.json, watches.json
~/.spirit/runbooks/     Named runbooks (<name>.lua with metadata header; builtins embedded in the binary)
```

## Architecture

### Process Model

```
spirit (TUI client)  ←──Unix socket──→  spirit daemon  ←──polls──→  tmux / Claude session files
spirit _hook         ──nudge─────────→  spirit daemon
spirit eval          ──Lua RPC────────→  spirit daemon
```

The daemon is a long-lived process that polls Claude sessions every ~1s and pushes updates over a Unix socket to all connected TUI clients. It auto-shuts down after 10 minutes with no clients.

### Package Layout

- **`cmd/spirit/`** — Entrypoint. Switch on `os.Args[1]` to subcommands. All subcommand logic lives here (setup, popup, dev, eval, capture, orchestrator).
- **`internal/daemon/`** — Daemon process and client. `daemon.go` owns the `Daemon` struct with all goroutines. Split by concern: `daemon_poll.go`, `daemon_lifecycle.go`, `daemon_synthesis.go`, `daemon_resolve.go`. Server handlers split into `server_session.go`, `server_transcript.go`, `server_hooks.go`, etc. `protocol.go` defines all request/response JSON types and constants. `client.go` wraps the socket connection.
- **`internal/app/`** — Bubble Tea app model. `Model` (defined across multiple files) holds all TUI state. `update.go` is the main `Update()` dispatcher. Commands split by domain: `command_session.go`, `command_backlog.go`, `command_relay.go`, `command_view.go`, `command_prefs.go`, `command_eval.go`. Views: `view.go`, `view_panels.go`, `view_overlays.go`.
- **`internal/ui/`** — Reusable TUI components. `sidebar.go` + `sidebar_*.go` (nav, backlog, project, view). `detail.go` + `detail_*.go` (messages, hooks, scroll, view). `minimap.go` + `minimap_*.go`. `copilot.go` + `copilot_view.go` (floating chat overlay with streaming, tool confirmations, scroll). Standalone: `search.go`, `palette.go`, `overlay.go`, `highlight.go`, `usagebar.go`, `relay.go`, etc.
- **`internal/claude/`** — Session discovery and parsing. `discover.go` finds sessions from status files. `session.go` defines `ClaudeSession`. `transcript.go` parses JSONL transcripts. `hook.go` handles `spirit _hook` events. `status.go` manages status file I/O. `backlog.go`, `macros.go`, `usage.go`, `worktree.go`, `synthesize.go`, `digest.go`.
- **`internal/scripting/`** — Lua scripting via `gopher-lua`. `eval.go` is the entry point. API registered per domain: `api_sessions.go`, `api_send.go`, `api_lifecycle.go`, `api_features.go`, `api_orchestrator.go`, `api_util.go`, `api_context.go`. `sandbox.go` creates the restricted VM. `convert.go` handles Lua↔Go value conversion.
- **`internal/tmux/`** — tmux API wrapper (`api.go`).
- **`internal/copilot/`** — Lulu companion helpers. `prompt.go` builds the `<selected-session>` dossier and the lightweight `<live-sessions>` preamble. The ACP transport, session lifecycle, and permission gating live in `internal/daemon/acp_client.go`.
- **`internal/ledger/`** — The perception ledger (spec Decision 10): durable, deduplicated signals + derived attention items + per-Hermes-session delivery cursors. Ingest is idempotent on `(kind, anchor)`; daemon ingest points live in `internal/daemon/daemon_ingest.go`; the away-delta consumed into Lulu's prompt is assembled in `delta.go`. `watch.go` adds reactive watches (W7): a persisted FSM (`active → triggered → processing → delivered → active | expired | cancelled | failed`) with expiry, cooldown, firing/LLM budgets, plus the per-item causal audit chain and explicit item resolution. The reactive engine that processes triggered watches is `internal/daemon/daemon_reactive.go`.
- **`internal/batch/`** — The single action schema every surface shares (W8): batch `Step`s (send/queue/tag/note/later/kill/commit/spawn/wait) with Decision 5 risk classes; `BuildPlan` (dry-run: fail-fast validation, targets resolved, approval points marked, executes nothing) and `Execute` (per-step `ActionReceipt`s stamped with the batch id; stop-on-failure returns skipped receipts + a resubmittable `remainder`, resume = resubmit with `resume_of`). Operations run through `batch.Ops` — `daemon.ClientOps` adapts `daemon.Client`, so batches ride the same per-op daemon paths as everything else. The daemon imports `batch` (permission rendering); `batch` never imports `daemon`.
- **`internal/runbook/`** — Named runbook store (W8): `~/.spirit/runbooks/<name>.lua` + embedded builtins (`broadcast`), metadata header (`-- description:`, `-- param: name[!] desc`, `-- actions: op, op`), required-param and declared-action enforcement. The build-phase Lua VM lives in `internal/scripting/runbook.go`: it registers ONLY snapshot-backed `sessions()`/`session()`, `params`, and `log` — structurally side-effect-free — and the emitted steps ride the batch pipeline (explain = metadata only; plan = dry-run; run = one approval, per-action receipts).
- **`internal/spirit/`** — Spirit animal name generation for session avatars.

### Key Data Flow

1. **Hook events** (`spirit _hook <type>`): Claude Code calls this binary; it writes a status file to disk and sends a `nudge` over the socket to trigger an immediate daemon poll.
2. **Daemon poll**: Reads all status files → builds `[]ClaudeSession` → broadcasts to subscribers via the socket.
3. **TUI client**: Receives session list, renders sidebar + detail panel. Sends commands (send message, kill, synthesize, etc.) back to daemon via RPC requests.
4. **Lua eval** (`spirit eval`): Connects to daemon socket, executes sandboxed Lua with a Go-backed API that proxies requests to the daemon.

### Daemon–Client Protocol

Newline-delimited JSON over Unix socket. `protocol.go` defines all request types (`Req*` constants) and response types (`Resp*` constants) with their data payloads. The `subscribe` request initiates a push stream; all other requests are single request/response.

### App State Machine

`Model.state` in `internal/app/` controls which key handler is active. States include `StateNormal`, `StateSearching`, `StateKillConfirm`, `StatePromptRelay`, `StateQueueRelay`, `StatePalette`, `StateMacro`, `StateNoteEdit`, `StatePrefsEditor`, `StateMinimapSettings`, `StateCopilot`, `StateCopilotConfirm`, `StateAdjustCopilot`, `StateAttentionInbox`, `StateWatchPicker`, `StateRunbookConfirm`, etc.

## Troubleshooting TUI Rendering

```sh
./bin/spirit capture              # auto-detect terminal size (200x50 default)
./bin/spirit capture 160x40       # render at specific COLSxROWS
```

Headless render using the same `View()` code, with ANSI stripped. Works outside tmux as long as the daemon is running.

## Claude Code Hooks

`spirit setup` patches `~/.claude/settings.json` to register `spirit _hook <type> #spirit-hook` for each event type. The `#spirit-hook` marker identifies spirit-managed hooks for future migration/updates without touching unrelated hooks.

## Copilot (Lulu)

Lulu is the persistent AI companion inside Spirit, toggled with `gc` or `shift+tab` and rendered as a floating indigo-bordered overlay (float mode) or a docked right-side column; `alt+'` switches mode. It is backed by a persistent **Hermes ACP session**, not a `claude` CLI subprocess per prompt.

**Key architecture:**
- **ACP transport** (`internal/daemon/acp_client.go`): the daemon runs one long-lived `hermes acp` subprocess speaking JSON-RPC 2.0 over newline-delimited stdio. It starts lazily on the first prompt: `initialize` → `session/load` (resume) or `session/new` (fresh) → `session/prompt` streams `session/update` notifications. A single subprocess serves the whole conversation; there is no per-prompt process spawn.
- **Session persistence**: the Hermes session UUID is written to `~/.spirit/copilot/hermes_session` and resumed via `session/load` on daemon restart, so the conversation survives restarts. `/new` (`copilot_clear_history`) kills the subprocess, forgets the UUID, and starts a fresh session.
- **Scope + context** (`internal/copilot/prompt.go`): each chat carries a `request_id`/`client_id` and a scope object — the selected session captured at the originating client and validated by the daemon (vanished session → eager failure, never silent retargeting). Prompts get a bounded `<selected-session>` dossier built from fleet truth, plus a `<live-sessions>` fleet snapshot injected only when it materially changed since Lulu last saw it (digest delta). `/preamble` toggles only the fleet snapshot; stream events are delivered to the originating client only.
- **MCP tools** (`internal/mcpserver`, `internal/receipt`): `spirit mcp` is injected via `mcp_servers` at session open — Hermes registers 28 typed `mcp__spirit__*` tools (list/get/transcript/diff/hooks/summary/wait/list_watches/list_attention/reactive_status/plan_actions/list_runbooks/explain_runbook/plan_runbook + send/queue/enable_remote_control/spawn/kill/tag/note/later/commit/create_watch/cancel_watch/resolve_attention/run_actions/run_runbook). Side-effect tools return an `ActionReceipt` (action_id, target, delivery outcome, observed post-action state); `wait_session` blocks until a session reaches idle/working/cycle for post-action reconciliation; `create_watch`'s receipt action_id becomes the watch's `created_by_request_id`. SKILL.md — including the W6 intent playbooks (review, triage, plan hygiene, reconciliation, correlation) and the W8 batch/runbook contract — is generated from the tool registry and installed to `~/.hermes/skills/spirit/` by `spirit setup`.
- **Plan awareness** (`internal/laxicon`): read-only, mtime-cached parser of per-repo `laxicon/plans/*.md` and `laxicon/specs/*.md` (tolerant frontmatter `status`, first-heading title, `- [ ]`/`- [x]`/`- [~]` tallies; fail-soft per file). Live sessions' project roots feed an `<active-plans>` block in the fleet snapshot (plan state joins the delta digest, so a plan edit re-injects it) and a plan section in the dossier — the session's `plan:<slug>` tag is the Lulu-maintained correlation; cwd/branch adjacency is surfaced only as a hint. Spirit never writes plan files.
- **Model & mode switching**: `/model [id]` maps to `session/set_model`; `/mode [id]` maps to `session/set_mode` (`default`/`accept_edits`/`dont_ask`), persists to `~/.spirit/copilot/mode`, and is re-applied on session open. Both are shown in the panel title.
- **Perception ledger + away-delta** (`~/.spirit/ledger/`, `internal/ledger`, ingest in `internal/daemon/daemon_ingest.go`): the daemon normalizes fleet transitions into durable signals (`turn_completed`, `waiting_input`, `overlap_detected`, `queue_delivered`/`queue_failed`, `session_started`/`session_ended`, `action_failed`, `later_woke`) with idempotent `(kind, anchor)` ingest — repeated idle polls never re-signal — and derives attention items (open → delivered → resolved/expired). Each user-initiated Lulu turn injects a coalesced `<away-delta>` block (severity-ordered, ~10 detailed + counted remainder) and advances a per-Hermes-session cursor; `/new` starts a fresh cursor whose first turn gets an open-item snapshot only. This subsumed the former W0-era event journal (`~/.spirit/copilot/events/`, deleted in W6).
- **Reactive attention (W7)** (`internal/daemon/daemon_reactive.go`, `internal/ledger/watch.go`): explicit watches (persisted in `~/.spirit/ledger/watches.json`, spec Decision 10 schema — scope, condition `completed_turn|waiting|overlap|action_reconciled`, response `inbox|notify|inspect_and_recommend`, mandatory expiry + cooldown + max_firings) fire on fresh ledger signals and are processed from the poll loop **only while a TUI client is subscribed** (Decision 11; the 10-min idle shutdown is never defeated). `inbox` = audit + inbox row; `notify` = at most one immediate notification (high-salience waits/failures, 30s global throttle), everything else coalesced into one triage digest; `inspect_and_recommend` = one bounded tool-less prompt in a **session/fork** of the Lulu session (capability-gated, single-flight, serialized behind user turns, 120s cap, per-watch LLM budget spent on attempt, exponential backoff, no retry) whose proposal lands on the attention item — the reactive path never sends prompts to coding sessions, never mutates the fleet or repos. Fork streams route to per-session sinks; permission requests from non-main sessions are auto-denied. Every step is recorded in the item's causal audit chain. TUI: chord `ga` = attention inbox (r resolve, c cancel watch), `gw` = watch selected session, `/watch` in Lulu input; broadcast `attention` chunks flash + count toward a ⚡N badge. Gate C: `spirit eval laxicon/gates/gate-c.lua`.
- **History persistence**: in-memory `[]CopilotHistoryMsg` in the daemon, serialized to `~/.spirit/copilot/chat_history.json`. Survives TUI and daemon restarts. Last 200 messages kept.
- **Protocol**: `copilot_chat`, `copilot_cancel`, `copilot_status`, `copilot_history`, `copilot_clear_history`, `copilot_toggle_preamble`, `copilot_set_model`, `copilot_set_mode`, `copilot_permission_answer`, plus `watch_create` (now with an optional `actionID` anchor), `watch_list`, `watch_cancel`, `attention_list`, `attention_resolve` request types; `queue` accepts an optional `actionID` and returns the durable queue item id. Streaming responses via `copilot_stream` with chunk types: `session`, `text_delta`, `thought`, `tool_call`, `tool_update`, `plan`, `usage`, `mode`, `permission_request`, `permission_resolved`, `attention`, `done`, `error` — all stamped with `request_id`/`client_id` and delivered to the originating client (`attention` chunks broadcast).
- **Permission flow** (`internal/daemon/acp_permission_flow.go`): every Hermes `session/request_permission` is forwarded to the human — the TUI enters `StateCopilotConfirm` and renders the typed payload (real diff for edits, `$ command` for executes, a legible step list for W8 batch calls — targets resolved to display names, per-step risk, `⚠ N destructive` header — never an opaque JSON blob, `⚠ sensitive` flag) with option accelerators (`y`/`a`/`n`/`N`, esc denies) and an auto-deny countdown (daemon resolves at 55s, under Hermes's 60s). Fail-safe deny when no client is attached, on client disconnect, and on prompt cancel/supersede.

## Runbooks & Batch Actions (W8)

One action schema across every surface (`internal/batch`): a validated batch of send/queue/tag/note/later/kill/commit/spawn/wait steps submitted as ONE unit. `plan` = dry-run (fail-fast validation — unknown session or capability-gated op rejects the whole batch — targets resolved against the live fleet, Decision 5 risk class per step, approval points marked, executes NOTHING); `action` = execute through the same per-op daemon paths, one `ActionReceipt` per step stamped with the `batch_id`. Partial failure: `on_error=stop` (default) gives skipped steps receipts and returns them verbatim as `remainder` — resume by resubmitting with `resume_of`; failed steps become `action_failed` ledger signals. Queue steps stamp their receipt `action_id` onto the durable queue item, so queued message → delivery → the turn it caused (`caused_by_action` evidence) → reconciliation is one traceable chain, and `action_reconciled` watches can anchor to a specific `action_id`.

**Runbooks** promote macros into named, parameterized definitions (`~/.spirit/runbooks/<name>.lua`, builtins embedded). The build phase is structurally side-effect-free Lua (only `sessions()`/`session()`/`params`/`log` exist in its VM) that EMITS a batch riding the same action pipeline — no second execution path. Every runbook supports explain (metadata + declared action classes, zero execution) and dry-run (the emitted batch as a plan) before execute (structured per-step receipts). Runbooks are human-triggered and human-approved; the reactive path cannot execute runbooks or batches.

Surfaces: MCP `plan_actions`/`run_actions`/`list_runbooks`/`explain_runbook`/`plan_runbook`/`run_runbook` (one `run_actions` call = one Hermes permission round-trip for the whole batch, rendered legibly in the confirm overlay); CLI `spirit agent plan|action '<json>'` and `spirit agent runbook list|explain|plan|run <name> [--param k=v]`; Lua `plan_actions`/`run_actions`/`runbooks`/`runbook_explain`/`runbook_plan`/`runbook_run`; TUI palette runbook entries → dry-run preview overlay (`StateRunbookConfirm`) → `y` executes the exact previewed steps. Gate D: `spirit eval laxicon/gates/gate-d.lua` (TUI attached).

## Durable Reactivity (W9)

The W7 reactive engine (`daemon_reactive.go`) processes triggered watches, but only while the reactive gate is satisfied. W9 makes that gate honest and lets it run headlessly under explicit, revocable enablement.

**The gate correction (§0).** The gate is `len(d.subscribers) > 0 || d.durableReactive` (`reactiveEligible`, `daemon_reactive_lease.go`) — a REAL push-stream subscriber (only `ReqSubscribe` adds one) or the durable lease. It deliberately does not key on `clientCount`: an RPC-only `spirit eval` bumps `clientCount` but is not a subscriber, so it no longer spuriously drives reactive processing. This is spec alignment with Decision 11's word "subscribed", and it is what makes Gate E's "no TUI attached" provable from an eval client.

**The lease + sidecar.** The reactive *brain* stays in the daemon (where the ledger and the single ACP subprocess live). A separately-supervised `spirit reactive run` worker holds a held-open `ReqReactiveLease`: for the connection's lifetime `d.durableReactive` is true, which both satisfies the reactive gate and adds a `!durableReactive` term to `idleWatcher` — the one sanctioned, explicit, revocable defeat of the 10-minute idle timeout. Drop the worker → the lease drops → autonomy stops and the daemon reverts to normal idle behavior. Supervision is a macOS launchd LaunchAgent (`~/Library/LaunchAgents/com.spirit.reactive.plist`, `KeepAlive`), installed/loaded by `spirit reactive enable` and removed by `disable`. `make` restarts only the daemon; the worker survives it and re-leases within a bounded backoff. Non-Darwin hosts refuse `enable` (launchd-only; cross-platform supervision is a non-goal).

**What it may do headless — unchanged and structural.** The durable path runs the *same* `reactiveTick`: inbox / notify / inspect_and_recommend only. It never prompts a coding session, never mutates the fleet or a repo, never executes a batch or runbook. Durable reactivity changes *when* `reactiveTick` runs, not *what* it may do. `inbox` is fully durable (item + audit written regardless of subscribers). `notify` high-salience firings additionally emit ONE OS notification (`osascript`; `terminal-notifier` if present) — suppressed during quiet hours, never the durable item; digest-class notifies are never OS-pushed. `inspect_and_recommend` runs one bounded, tool-less `session/fork` under the full rails: quiet-hours degrade-to-inbox, the global daily provider budget, the per-watch LLM budget, single-flight, 120s cap, exponential backoff, no retry.

**Budgets & quiet hours (shared path).** Enforced in the daemon's shared `runRecommend`/`deliverNotify` so TUI-active and durable modes obey identical rules. The global daily provider budget is a durable, date-keyed counter in `~/.spirit/ledger/budget.json` (`daemon_reactive_budget.go`) — spent on *attempt* (before the fork), rolled over on date change, reloaded at daemon start so a crash-loop cannot overspend. Cap = pref `reactive.daily_llm_calls` (default 20). Quiet hours = pref `reactive.quiet_hours` (`HH:MM-HH:MM`, local, midnight-wrap honored; `daemon_reactive_quiet.go`): OS notifications suppressed and recommend degrades to inbox inside the window. Optional daily digest time = pref `reactive.digest_at` (`HH:MM`); the worker-side scheduler nudges `ReqReactiveDigest` at that time. Later-timer reminders need no scheduler — they fire headlessly the moment the lease keeps the daemon awake.

**Enablement is a human act.** State lives in pref `reactive` (`off | on | paused`); runtime mirrors are `d.durableReactive`/`d.reactivePaused`. Pause keeps the lease (daemon awake, ingest continues, watches keep triggering) but claims/dispatches nothing — provably cannot touch queue/commit/Later/overlap mechanics, which live in `poll()`, not `reactiveTick`. Surfaces: **CLI** `spirit reactive enable|disable|pause|resume|status|run [--with-cron]`; **TUI** palette "Durable reactivity: enable/pause/resume/disable" + `gR` chord (cycles enable → pause ⇄ resume; disable is palette-only) + a statusline glyph (steady ⚡ / ⏸) and inbox-header line; **Lua** `reactive_status`/`reactive_pause`/`reactive_resume`; **MCP** read-only `reactive_status` only (tool count 26 → 27) — **there is deliberately no MCP enable/disable; the autonomy switch is human-only.** Protocol: `ReqReactiveLease` (held-open), `ReqReactiveControl` (pause/resume), `ReqReactiveStatus`, `ReqReactiveDigest`. Gate E: `spirit reactive enable`, close all TUIs, `spirit eval laxicon/gates/gate-e.lua` (arena `laxicon/gates/.gate-e-arena/`); `spirit reactive disable` when done.

**Optional Hermes cron** (`enable --with-cron`, `reactive_scheduler.go`) writes a best-effort `attach_to_session` job descriptor under `~/.hermes/cron/`; when the gateway is down it simply does not fire — the Spirit-owned scheduler is the reliable primary, never dependent on it.

## Lua Scripting

The eval VM is sandboxed (base/table/string/math only — no os/io/debug). Scripts are stateless per invocation. The last expression is JSON-serialized to stdout. Use `spirit --agent-help` for the full Lua API reference.
