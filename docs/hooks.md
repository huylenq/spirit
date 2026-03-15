# Hook System & Session Lifecycle

## Architecture Overview

```mermaid
flowchart LR
    CC["Claude Code<br/>(hook event)"]
    Hook["cmc _hook &lt;type&gt;<br/>(subprocess)"]
    Files[("~/.cache/cmc/<br/>status files")]
    Sock["daemon.sock<br/>(Unix socket)"]
    Daemon["Daemon<br/>(patchSession)"]
    TUI["TUI<br/>(Bubble Tea)"]

    CC -- "stdin JSON" --> Hook
    Hook -- "write" --> Files
    Hook -- "nudge RPC<br/>(fire & forget)" --> Sock
    Sock --> Daemon
    Daemon -- "subscribe<br/>(push)" --> TUI
    Daemon -- "poll 1s" --> Files
```

The daemon has **two paths** to learn about state changes:

| Path | Latency | Reliability |
|------|---------|-------------|
| **Nudge** (fast path) | ~1ms | Best-effort; dropped if daemon is down |
| **Poll** (slow path) | up to 1s | Authoritative; reads status files from disk |

## Hook Registration

`cmc setup` writes hook commands into `~/.claude/settings.json`:

```mermaid
flowchart TD
    Setup["cmc setup"]
    Settings["~/.claude/settings.json"]
    Setup -- "upsertHookCmd()" --> Settings

    subgraph "settings.json → hooks"
        PT["PreToolUse<br/>matcher: (all)"]
        POT["PostToolUse<br/>matcher: Bash|Edit|Write"]
        UPS["UserPromptSubmit<br/>matcher: (all)"]
        Stop["Stop<br/>matcher: (all)"]
        Notif["Notification<br/>matcher: (all)"]
        SS["SessionStart<br/>matcher: (all)"]
        SE["SessionEnd<br/>matcher: (all)"]
        PC["PreCompact<br/>matcher: (all)"]
    end
```

Each hook command embeds `#cmc-hook` marker for future migration/deduplication.

## Session Lifecycle

### Current (incorrect semantics)

```mermaid
stateDiagram-v2
    [*] --> Working : SessionStart

    Working --> Done : Stop / SessionEnd
    Working --> Working : PreToolUse / UserPromptSubmit
    Working --> Waiting : Notification<br/>(permission_prompt /<br/>elicitation_dialog)

    Waiting --> Working : UserPromptSubmit / PreToolUse

    Done --> Working : UserPromptSubmit / SessionStart
    Done --> Later : user marks later (TUI)
    Done --> [*] : process exits + cleanup

    Later --> Working : user un-laters

    state Working {
        [*] --> Active
        Active --> Compacting : PreCompact
        Compacting --> Active
        Active --> ToolUse : PostToolUse
        ToolUse --> Active
    }

    note right of Waiting
        IsWaiting = true
        Static bell icon (no spinner)
        "Ball is in YOUR court"
    end note

    note right of Done
        StopReason persisted
        LastActionCommit checked
    end note
```

**Problems with current model:**
- `"stopped"` / `StatusDone` conflates two different things: "user needs to act" (Stop) and "session is dead" (SessionEnd)
- `Later` is treated as a runtime status (`StatusLater`) but it's really a UI grouping tag orthogonal to runtime state
- `SessionEnd → stopped` is wrong: user has nothing to do when a session ends

### New (correct semantics)

Two runtime statuses based on **whose turn it is**:

| Status value | Const | Meaning | Who acts next? |
|-------------|-------|---------|----------------|
| `agent-turn` | `StatusAgentTurn` | Claude is thinking / executing tools | Claude |
| `user-turn` | `StatusUserTurn` | Claude stopped, waiting for user decision | User |

A session is **gone** (no status file / cleaned up) when the process exits. `SessionEnd` does not set a status — it triggers cleanup.

`Later` is a **tag** (record), not a status. A session can be marked later regardless of runtime state. It only affects TUI grouping (Later-marked sessions appear in a separate "Later" group).

```mermaid
stateDiagram-v2
    [*] --> AgentTurn : SessionStart

    AgentTurn --> UserTurn : Stop
    AgentTurn --> AgentTurn : PreToolUse
    AgentTurn --> UserTurn : Notification<br/>(permission_prompt /<br/>elicitation_dialog)

    UserTurn --> AgentTurn : UserPromptSubmit / PreToolUse

    AgentTurn --> [*] : SessionEnd<br/>(cleanup, no status change)

    state AgentTurn {
        [*] --> Active
        Active --> Compacting : PreCompact
        Compacting --> Active
        Active --> ToolUse : PostToolUse
        ToolUse --> Active
    }

    note right of UserTurn
        Covers both:
        • Stop reason (task finished, error, etc.)
        • IsWaiting (permission prompt / elicitation)
        StopReason + IsWaiting distinguish the sub-cases
    end note

    note left of AgentTurn
        Spinner animates
        Claude is actively working
    end note
```

**Key differences from current:**

| Aspect | Current | New |
|--------|---------|-----|
| SessionEnd | → `stopped` (user-turn) | → cleanup/removal (no status) |
| Stop | → `stopped` | → `user-turn` |
| Notification (waiting) | overlay on `working` | → `user-turn` with `IsWaiting=true` |
| Later | `StatusLater` (a 3rd status) | Tag on session, orthogonal to status |
| Status file values | `working` / `stopped` / `later` | `agent-turn` / `user-turn` (no `later`) |
| UserPromptSubmit | → `working` | → `agent-turn` |

**Hook → status mapping (new):**

| Hook | Status change | Rationale |
|------|--------------|-----------|
| SessionStart | → `agent-turn` | Claude starts working |
| UserPromptSubmit | → `agent-turn` | User submitted, now it's Claude's turn |
| PreToolUse | → `agent-turn` | Claude is executing a tool |
| Stop | → `user-turn` | Claude stopped, user decides what's next |
| Notification | → `user-turn` | Claude needs user permission/input |
| PostToolUse | (no change) | Still agent's turn, just logging tool result |
| PreCompact | (no change) | Internal event, no turn change |
| SessionEnd | → **cleanup** | Process is gone, remove status files |

**Notification vs Stop — both `user-turn` but different UX:**

| Sub-case | `IsWaiting` | `StopReason` | TUI rendering |
|----------|-------------|-------------|---------------|
| Permission prompt | `true` | (empty) | Bell icon (magenta) — "approve this" |
| Task finished | `false` | e.g. `"end_turn"` | Age string (gray) — "done, review it" |
| Error | `false` | e.g. `"error"` | Age + reason badge — "something broke" |

### Crash Recovery

When the daemon polls and finds no Claude process but `.status` says `"agent-turn"`:

```mermaid
flowchart TD
    Poll["poll() → DiscoverSessions()"]
    Check{"Claude PID<br/>in process tree?"}
    Status{"status file<br/>says agent-turn?"}
    Clean["RemoveStatus()<br/>delete all files"]

    Poll --> Check
    Check -- "yes" --> Normal["Build session normally"]
    Check -- "no" --> Status
    Status -- "yes (stale)" --> Clean
    Status -- "no (user-turn)" --> Clean
```

Process gone = session gone. No intermediate "stopped" state for dead sessions — just clean up the files. This is the safety net for when `SessionEnd` hook doesn't fire (crash, SIGKILL, etc.).

## Hook Event Details

### HandleHook Switch

#### Current

```mermaid
flowchart TD
    Entry["HandleHook(hookType)"]
    Pane["resolveCurrentPane()<br/>walk process tree → tmux pane"]
    Stdin["Read stdin JSON<br/>→ hookInput struct"]
    Log["Append to .hooks log"]

    Entry --> Pane --> Stdin --> Log

    Log --> SW{hookType?}

    SW -- "UserPromptSubmit" --> UPS["status → working<br/>cache .lastmsg<br/>clear .waiting<br/>clear .stopreason"]
    SW -- "PreToolUse" --> PTU["status → working<br/>clear .waiting<br/>clear .stopreason"]
    SW -- "PostToolUse" --> POST["if Bash + git commit:<br/>  .lastaction = commit<br/>if Edit/Write:<br/>  .lastaction = edit"]
    SW -- "Stop" --> STOP["status → stopped<br/>write .stopreason"]
    SW -- "Notification" --> NOTIF["if permission_prompt<br/>or elicitation_dialog:<br/>  write .waiting"]
    SW -- "SessionStart" --> SS["status → working<br/>clear .stopreason"]
    SW -- "SessionEnd" --> SE["status → stopped<br/>clear .waiting"]
    SW -- "PreCompact" --> PC[".compactcount++"]

    UPS --> Nudge["nudgeDaemon(nd)"]
    PTU --> Nudge
    POST --> Nudge
    STOP --> Nudge
    NOTIF --> Nudge
    SS --> Nudge
    SE --> Nudge
    PC --> Nudge
```

#### New

```mermaid
flowchart TD
    Entry["HandleHook(hookType)"]
    Pane["resolveCurrentPane()<br/>walk process tree → tmux pane"]
    Stdin["Read stdin JSON<br/>→ hookInput struct"]
    Log["Append to .hooks log"]

    Entry --> Pane --> Stdin --> Log

    Log --> SW{hookType?}

    SW -- "UserPromptSubmit" --> UPS["status → agent-turn<br/>cache .lastmsg<br/>clear .waiting<br/>clear .stopreason"]
    SW -- "PreToolUse" --> PTU["status → agent-turn<br/>clear .waiting<br/>clear .stopreason"]
    SW -- "PostToolUse" --> POST["if Bash + git commit:<br/>  .lastaction = commit<br/>if Edit/Write:<br/>  .lastaction = edit"]
    SW -- "Stop" --> STOP["status → user-turn<br/>write .stopreason"]
    SW -- "Notification" --> NOTIF["status → user-turn<br/>write .waiting"]
    SW -- "SessionStart" --> SS["status → agent-turn<br/>clear .stopreason"]
    SW -- "SessionEnd" --> SE["RemoveStatus()<br/>(cleanup all files)"]
    SW -- "PreCompact" --> PC[".compactcount++"]

    UPS --> Nudge["nudgeDaemon(nd)"]
    PTU --> Nudge
    POST --> Nudge
    STOP --> Nudge
    NOTIF --> Nudge
    SS --> Nudge
    SE --> Cleanup["Session disappears from TUI"]
    PC --> Nudge
```

### What Each Hook Carries

#### Current

| Hook | Status Change | Key Fields Used | Nudge Fields Set |
|------|--------------|-----------------|------------------|
| UserPromptSubmit | → working | `prompt` | `Status`, `LastUserMessage`, `IsWaiting=false` |
| PreToolUse | → working | (none) | `Status`, `IsWaiting=false` |
| PostToolUse | (none) | `tool_name`, `tool_input` | `IsGitCommit` or `IsFileEdit` |
| Stop | → stopped | `reason` | `Status`, `StopReason` |
| Notification | (none) | `notification_type` | `IsWaiting=true` |
| SessionStart | → working | (none) | `Status` |
| SessionEnd | → stopped | (none) | `Status` |
| PreCompact | (none) | (none) | `Compacted=true` |

#### New

| Hook | Status Change | Key Fields Used | Nudge Fields Set |
|------|--------------|-----------------|------------------|
| UserPromptSubmit | → `agent-turn` | `prompt` | `Status`, `LastUserMessage`, `IsWaiting=false` |
| PreToolUse | → `agent-turn` | (none) | `Status`, `IsWaiting=false` |
| PostToolUse | (none) | `tool_name`, `tool_input` | `IsGitCommit` or `IsFileEdit` |
| Stop | → `user-turn` | `reason` | `Status`, `StopReason` |
| Notification | → `user-turn` | `notification_type` | `Status`, `IsWaiting=true` |
| SessionStart | → `agent-turn` | (none) | `Status` |
| SessionEnd | → **cleanup** | (none) | `RemoveSession` (session removed) |
| PreCompact | (none) | (none) | `Compacted=true` |

## Nudge Protocol

The hook subprocess sends a fire-and-forget JSON message to the daemon:

```mermaid
sequenceDiagram
    participant H as cmc _hook
    participant S as daemon.sock
    participant D as Daemon
    participant C as TUI Clients

    H->>S: {"type":"nudge","data":{...}}
    Note over H: 50ms timeout, then close
    S->>D: handleNudge()
    D->>D: patchSession(NudgeData)

    alt Session found in memory
        D->>D: Apply field updates
        D->>D: version++
        D->>C: Push updated sessions
    else Session not yet discovered
        D->>D: nudge() → trigger full poll
    end
```

### NudgeData Fields

#### Current

```
PaneID          string   ← which pane changed
Status          string   ← "agent-turn" or "user-turn" (empty = no status change)
LastUserMessage string   ← cached user prompt
StopReason      string   ← why session stopped
IsWaiting       *bool    ← nil=no change, true=waiting, false=not waiting
IsGitCommit     *bool    ← nil=no change, true=last action was git commit
IsFileEdit      *bool    ← nil=no change, true=last action was file edit
Compacted       bool     ← true=increment compact counter
```

#### New

```
PaneID          string   ← which pane changed
Status          string   ← "agent-turn" or "user-turn" (empty = no status change)
Remove          bool     ← true = session ended, remove from memory
LastUserMessage string   ← cached user prompt
StopReason      string   ← why it's user's turn (only meaningful when user-turn)
IsWaiting       *bool    ← nil=no change, true=permission/elicitation prompt
IsGitCommit     *bool    ← nil=no change, true=last action was git commit
IsFileEdit      *bool    ← nil=no change, true=last action was file edit
Compacted       bool     ← true=increment compact counter
```

`*bool` pointers distinguish "not set" (nil) from "explicitly set to false".

### Daemon patchSession Logic

#### Current

```mermaid
flowchart TD
    Recv["Receive NudgeData"]
    Find{"Find session<br/>by PaneID"}
    Find -- "not found" --> Poll["Trigger full poll"]
    Find -- "found" --> Apply

    subgraph Apply ["Apply Updates"]
        direction TB
        S["Set Status + LastChanged"]
        W{"Status == Working?"}
        W -- "yes" --> Clear["Clear StopReason<br/>Clear IsWaiting<br/>Reload PermissionMode"]
        W -- "no" --> Skip["Skip clearing"]
        Clear --> Fields
        Skip --> Fields
        Fields["Apply non-nil fields:<br/>StopReason, IsWaiting,<br/>IsGitCommit, IsFileEdit,<br/>CompactCount++"]
    end

    Apply --> Bump["version++"]
    Bump --> Notify["Notify all subscribers"]
```

#### New

```mermaid
flowchart TD
    Recv["Receive NudgeData"]
    Rm{"Remove?"}
    Rm -- "yes" --> Del["Remove session from memory<br/>+ RemoveStatus() on disk"]
    Rm -- "no" --> Find{"Find session<br/>by PaneID"}
    Find -- "not found" --> Poll["Trigger full poll"]
    Find -- "found" --> Apply

    subgraph Apply ["Apply Updates"]
        direction TB
        S["Set Status + LastChanged"]
        W{"Status == AgentTurn?"}
        W -- "yes" --> Clear["Clear StopReason<br/>Clear IsWaiting<br/>Reload PermissionMode"]
        W -- "no" --> Skip["Skip clearing"]
        Clear --> Fields
        Skip --> Fields
        Fields["Apply non-nil fields:<br/>StopReason, IsWaiting,<br/>IsGitCommit, IsFileEdit,<br/>CompactCount++"]
    end

    Apply --> Bump["version++"]
    Del --> Bump
    Bump --> Notify["Notify all subscribers"]
```

## Status Files

All stored in `~/.cache/cmc/`, keyed by tmux pane ID (e.g., `%1`).

#### Current

```mermaid
graph LR
    subgraph "Per-Pane Files"
        status[".status<br/>working | stopped | later"]
        session[".session<br/>UUID"]
        hooks[".hooks<br/>timestamped event log"]
        lastmsg[".lastmsg<br/>last user prompt"]
        stopreason[".stopreason<br/>stop reason string"]
        waiting[".waiting<br/>existence = waiting"]
        compactcount[".compactcount<br/>integer counter"]
        lastaction[".lastaction<br/>commit | edit"]
        queue[".queue<br/>message to send on Done"]
    end

    subgraph "Daemon Files"
        sock["daemon.sock"]
        pid["daemon.pid"]
        lock["daemon.sock.lock"]
    end

    subgraph "Later Records"
        later["later/*.json"]
    end
```

#### New

```mermaid
graph LR
    subgraph "Per-Pane Files"
        status[".status<br/>agent-turn | user-turn"]
        session[".session<br/>UUID"]
        hooks[".hooks<br/>timestamped event log"]
        lastmsg[".lastmsg<br/>last user prompt"]
        stopreason[".stopreason<br/>stop reason string"]
        waiting[".waiting<br/>existence = waiting"]
        compactcount[".compactcount<br/>integer counter"]
        lastaction[".lastaction<br/>commit | edit"]
        queue[".queue<br/>message to send on UserTurn"]
    end

    subgraph "Daemon Files"
        sock["daemon.sock"]
        pid["daemon.pid"]
        lock["daemon.sock.lock"]
    end

    subgraph "Later Records (tag, not status)"
        later["later/*.json<br/>orthogonal to runtime status"]
    end
```

**Key change:** `.status` no longer contains `"later"`. Later records are tracked independently in `later/*.json`. A session can be marked later while in any runtime state.

### File Lifecycle

| File | Created | Updated | Cleared |
|------|---------|---------|---------|
| `.status` | First hook event | Every status-changing hook | `RemoveStatus()` on cleanup |
| `.session` | First hook event (has session_id) | Never (stable per session) | `RemoveStatus()` |
| `.hooks` | First hook event | Every hook (append) | Trimmed at 60KB; `RemoveStatus()` |
| `.lastmsg` | UserPromptSubmit | UserPromptSubmit | `RemoveStatus()` |
| `.stopreason` | Stop hook | Stop hook | UserPromptSubmit / PreToolUse / SessionStart |
| `.waiting` | Notification hook | Notification hook | UserPromptSubmit / PreToolUse / SessionEnd |
| `.compactcount` | First PreCompact | PreCompact (increment) | `RemoveStatus()` (never reset during session) |
| `.lastaction` | PostToolUse | PostToolUse (overwrite) | `RemoveStatus()` |
| `.queue` | Queue request from TUI | Never | Delivered or session disappears |

## TUI Rendering

### Detail Column (right side of session row)

#### Current

```mermaid
flowchart TD
    Start["renderDetail()"]
    CDP{"CommitDone<br/>Pending?"}
    Wait{"IsWaiting?"}
    Stat{Status?}

    Start --> CDP
    CDP -- "yes" --> CDF["◐◓◑◒<br/>(animated, green)"]
    CDP -- "no" --> Wait
    Wait -- "yes" --> Bell["🔔 static bell<br/>(magenta, bold)"]
    Wait -- "no" --> Stat
    Stat -- "Done" --> Age["age (e.g. 3m)<br/>(gray)"]
    Stat -- "agent-turn" --> Plan{"plan mode?"}
    Stat -- "Later" --> LaterAge["age or 🔖 age<br/>(purple)"]
    Plan -- "yes" --> PlanSpin["spinner (teal)"]
    Plan -- "no" --> WorkSpin["spinner (amber)"]
```

#### New

```mermaid
flowchart TD
    Start["renderDetail()"]
    CDP{"CommitDone<br/>Pending?"}
    Stat{Status?}

    Start --> CDP
    CDP -- "yes" --> CDF["◐◓◑◒<br/>(animated, green)"]
    CDP -- "no" --> Stat
    Stat -- "UserTurn" --> Wait{"IsWaiting?"}
    Stat -- "AgentTurn" --> Plan{"plan mode?"}

    Wait -- "yes" --> Bell["🔔 static bell<br/>(magenta, bold)<br/>'approve this'"]
    Wait -- "no" --> Age["age (e.g. 3m)<br/>(gray)<br/>'done, review it'"]
    Plan -- "yes" --> PlanSpin["spinner (teal)"]
    Plan -- "no" --> WorkSpin["spinner (amber)"]
```

**Note:** `Later` no longer appears here — Later-marked sessions render with their actual runtime status plus a Later record indicator (🔖) in the list grouping, not as a separate status branch.

### Badges Line (below session name)

```mermaid
flowchart LR
    B["renderBadges()"]
    B --> C{"LastActionCommit<br/>AND UserTurn?"}
    C -- "yes" --> CB["✓ committed<br/>(green)"]
    B --> S{"StopReason != ''<br/>AND UserTurn?"}
    S -- "yes" --> SB["reason text<br/>(blue)"]
    B --> K{"CompactCount > 0?"}
    K -- "yes" --> KB["↻N<br/>(gray)"]
    B --> BM{"Marked later?"}
    BM -- "yes" --> BMB["🔖 later<br/>(purple)"]
```

### Hook Event Overlay Colors

In the debug overlay (`hookTypeStyled()`):

| Hook Type | Color | Style Variable |
|-----------|-------|----------------|
| PreToolUse | Amber | `StatWorkingStyle` |
| PostToolUse | Cyan | `StatPostToolStyle` |
| UserPromptSubmit | Green | `DiffAddedStyle` |
| Stop | Blue | `StatDoneStyle` |
| Notification | Magenta | `StatWaitingStyle` |
| SessionStart | Green | `DiffAddedStyle` |
| SessionEnd | Blue | `StatDoneStyle` |
| PreCompact | Purple | `StatLaterStyle` |

## Dual-Layer Design Philosophy

```
┌─────────────────────────────────────────────────┐
│  HOOKS (real-time optimization layer)            │
│  Fast, ephemeral, best-effort                    │
│  Nudge delivers state changes in ~1ms            │
│  If daemon is down → changes still on disk       │
├─────────────────────────────────────────────────┤
│  STATUS FILES + TRANSCRIPT (source of truth)     │
│  Survive daemon restarts, session resumption     │
│  Poll reads files every 1s as authoritative      │
│  Transcript scan = ultimate fallback for commits │
└─────────────────────────────────────────────────┘
```

Hooks are the **fast path** — they get changes to the TUI in milliseconds.
Status files are the **truth** — they survive crashes and daemon restarts.
Transcript scanning is the **last resort** — for sessions started before hooks existed.
