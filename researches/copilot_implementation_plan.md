# Copilot: A Persistent Companion Inside Mission Control

## Context

Huy wants a copilot with soul — a persistent AI companion that lives inside cmc, aware of everything happening across Claude Code sessions. Inspired by OpenClaw's identity/memory architecture (SOUL.md, daily logs, memory search, pre-compaction preservation), but adapted as a cmc feature.

**Key constraint:** NOT autonomous. The copilot passively observes all events and responds only when the user asks. Actions are user-triggered.

**What it is:** A chat panel in the TUI backed by an LLM that has full context of live sessions, event history, and persistent memory. It knows what happened yesterday, what's happening now, and remembers what Huy tells it.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Daemon                                                     │
│  ┌────────────┐  ┌──────────────┐  ┌─────────────────────┐ │
│  │ Event      │  │ Identity &   │  │ Copilot RPC Handler │ │
│  │ Journal    │  │ Memory       │  │ (prompt builder +   │ │
│  │ (NDJSON)   │  │ (Markdown)   │  │  LLM invocation)    │ │
│  └─────┬──────┘  └──────┬───────┘  └──────────┬──────────┘ │
│        │                │                      │            │
│  patchSession()    LoadIdentity()      handleCopilotChat()  │
│  poll()            ReadMemory()        BuildSystemPrompt()  │
│  autoSynthesize()  AppendMemory()      newCopilotClaude()   │
└────────┼────────────────┼──────────────────────┼────────────┘
         │                │                      │
         │         ┌──────┴──────────────────────┴──┐
         │         │            TUI                  │
         │         │  ┌──────────────────────────┐   │
         │         │  │ Copilot Panel            │   │
         │         │  │ (replaces detail panel)  │   │
         │         │  │ - scrollable chat history│   │
         │         │  │ - text input at bottom   │   │
         │         │  │ - "thinking..." spinner  │   │
         │         │  └──────────────────────────┘   │
         │         └─────────────────────────────────┘
```

---

## Implementation Plan

### Layer 1: Event Journal (`internal/copilot/`)

**New files:**
- `internal/copilot/events.go` — event types
- `internal/copilot/journal.go` — append-only NDJSON writer/reader

**`CopilotEvent` struct:**
```go
type CopilotEvent struct {
    Time      time.Time        `json:"time"`
    Type      CopilotEventType `json:"type"`
    SessionID string           `json:"sid,omitempty"`
    Project   string           `json:"project,omitempty"`
    Detail    string           `json:"detail,omitempty"`
}
```

**Event types:** `session_spawned`, `session_died`, `status_change`, `prompt_submitted`, `tool_used`, `agent_stopped`, `permission_wait`, `compacted`, `git_commit`, `file_overlap`, `synthesized`, `digest_generated`, `skill_invoked`, `session_bookmarked`

**Storage:** `~/.cache/cmc/copilot/events/YYYY-MM-DD.ndjson`

**Journal API:**
```go
func NewJournal() *Journal
func (j *Journal) Append(event CopilotEvent) error
func (j *Journal) ReadToday() ([]CopilotEvent, error)
func (j *Journal) ReadDate(date string) ([]CopilotEvent, error)
func (j *Journal) RecentEvents(n int) ([]CopilotEvent, error)
func (j *Journal) ReadForSession(sessionID string, n int) ([]CopilotEvent, error)
```

**Integration points in existing code (emit calls):**
- `daemon_poll.go:patchSession()` — status changes, git commits, compaction, skills, session removal
- `daemon_poll.go:poll()` — new session detection (compare prev vs curr session IDs)
- `daemon_synthesis.go:autoSynthesize()` — after successful synthesis
- `daemon_synthesis.go:triggerDigest()` — after digest generation
- `daemon.go` — add `journal *copilot.Journal` field, init in `Run()`

### Layer 2: Identity & Memory (`internal/copilot/`)

**New files:**
- `internal/copilot/identity.go` — personality persistence
- `internal/copilot/memory.go` — long-term memory + daily logs + search

**Storage layout:**
```
~/.cache/cmc/copilot/
├── identity.md       # persona (name, style, instructions)
├── memory.md         # curated long-term facts
├── events/           # NDJSON event journals (Layer 1)
│   └── YYYY-MM-DD.ndjson
└── daily/            # narrative daily logs
    └── YYYY-MM-DD.md
```

**Identity API:**
```go
type Identity struct {
    Raw string // full markdown content of identity.md
}
func LoadIdentity() (*Identity, error)    // reads identity.md, creates default if missing
func DefaultIdentityContent() string      // returns the default identity.md template
```

Default `identity.md`:
```markdown
# Mission Control Copilot

You are the copilot for Huy's mission control — a persistent companion aware of all Claude Code sessions running across tmux.

**Style:** Concise, technical, aware of specific session details. Reference projects, headlines, and events by name. Be direct.

**You have access to:**
- Live session states (status, project, headline, git branch, overlaps)
- Today's event journal (every hook event, tool call, commit, compaction)
- Your long-term memory (facts Huy asked you to remember)
- The workspace digest (cross-session summary)
```

**Memory API:**
```go
type Memory struct{ baseDir string }
func NewMemory() *Memory
func (m *Memory) ReadLongTerm() (string, error)          // reads memory.md
func (m *Memory) AppendLongTerm(fact string) error        // appends to memory.md with timestamp
func (m *Memory) ReadDailyLog(date string) (string, error)
func (m *Memory) WriteDailyLog(date, content string) error
func (m *Memory) Search(query string) ([]SearchResult, error)  // substring search across all .md
```

**No SQLite.** The codebase has zero CGo dependencies. Simple substring search over markdown files is sufficient for v1. Can add FTS later if needed.

### Layer 3: Daemon Endpoints & LLM (`internal/daemon/`, `internal/copilot/`)

**New files:**
- `internal/daemon/server_copilot.go` — RPC handlers
- `internal/copilot/prompt.go` — system prompt assembly

**Modified files:**
- `internal/daemon/protocol.go` — new request/response types
- `internal/daemon/server.go` — dispatch cases
- `internal/daemon/client.go` — client methods
- `internal/daemon/daemon.go` — `journal` + `memory` fields on Daemon struct
- `internal/claude/synthesize.go` — `newCopilotClaude()` helper

**Protocol additions:**
```go
const (
    ReqCopilotChat   = "copilot_chat"
    ReqCopilotStatus = "copilot_status"
)

type CopilotChatData struct {
    Message string `json:"message"`
}
type CopilotChatResultData struct {
    Response string `json:"response"`
}
type CopilotStatusData struct {
    IdentityName string `json:"identityName"`
    EventsToday  int    `json:"eventsToday"`
    MemoryBytes  int    `json:"memoryBytes"`
}
```

**Prompt construction (`copilot/prompt.go`):**
```go
func BuildSystemPrompt(
    identity *Identity,
    longTermMemory string,
    recentEvents []CopilotEvent,
    sessions []claude.ClaudeSession,
    digest *claude.WorkspaceDigest,
) string
```

System prompt structure:
```
{identity.md content}

## Live Sessions
{formatted table: project | status | headline | branch | flags}

## Recent Activity (last ~50 events)
{timeline of today's events}

## Workspace Digest
{cached digest summary}

## Your Memory
{memory.md content}

## Behavior
- When asked to remember something, include [REMEMBER: fact] in your response.
- Reference sessions by name/project. Be specific about times and events.
- You are NOT autonomous. Only respond to what is asked.
```

**Memory extraction:** After LLM response, `handleCopilotChat` scans for `[REMEMBER: ...]` tags and auto-appends to `memory.md`. This keeps memory writes in-band without a separate "remember" command.

**LLM helper (`synthesize.go`):**
```go
func newCopilotClaude(systemPrompt, input string) *exec.Cmd {
    return exec.Command("claude", "--model", "sonnet", "-p",
        "--no-session-persistence", "--tools", "", "--effort", "medium",
        "--setting-sources", "",
        "--system-prompt", systemPrompt,
        input)
}
```

Uses Sonnet (not Haiku) for richer reasoning. `--effort medium` balances quality/latency.

**Handler (`server_copilot.go`):**
```go
func (d *Daemon) handleCopilotChat(data json.RawMessage) *Response {
    // 1. Parse message
    // 2. Load identity, memory, recent events, current sessions, digest
    // 3. BuildSystemPrompt(...)
    // 4. newCopilotClaude(systemPrompt, message).Output()
    // 5. Extract [REMEMBER: ...] tags → append to memory.md
    // 6. Return response text
}
```

This is a blocking RPC call — the TUI dispatches it in a `tea.Cmd` goroutine so it doesn't freeze the UI.

### Layer 4: TUI Panel (`internal/ui/`, `internal/app/`)

**New files:**
- `internal/ui/copilot.go` — copilot model (scroll state, size)
- `internal/ui/copilot_view.go` — render chat history
- `internal/app/update_copilot.go` — state handler for `StateCopilot`

**Modified files:**
- `internal/app/model.go` — `StateCopilot` enum, copilot fields on Model
- `internal/app/messages.go` — `CopilotMessage`, `CopilotResponseMsg`
- `internal/app/keymap.go` — `@` keybinding + `gc` chord
- `internal/app/update.go` — route `StateCopilot` + handle `CopilotResponseMsg`
- `internal/app/update_normal.go` — `@` key handler
- `internal/app/commands.go` — palette entry "Copilot"
- `internal/app/view.go` — render copilot panel in detail area
- `internal/app/view_footer.go` — `StateCopilot` footer hints

**Model additions:**
```go
// In AppState enum:
StateCopilot  // copilot chat active

// On Model struct:
copilot         ui.CopilotModel
copilotMessages []CopilotMessage  // conversation history (ephemeral, per TUI session)
copilotInput    ui.RelayModel     // text input
copilotThinking bool              // LLM in flight
```

**Chat message type:**
```go
type CopilotMessage struct {
    Role    string    // "user" or "copilot"
    Content string
    Time    time.Time
}
```

**View integration (`view.go`, around line 101-113):**
The copilot panel replaces the detail panel when `StateCopilot` is active, following the existing pattern where backlog preview replaces detail:
```go
} else if m.state == StateCopilot {
    detailContent = ui.RenderCopilotChat(m.copilotMessages, detailWidth, detailH, m.copilot.Scroll(), m.copilotThinking, m.spinner.View())
```

**State handler (`update_copilot.go`):**
- `Esc` → return to `StateNormal` (conversation history preserved)
- `Enter` → submit input, set `copilotThinking = true`, dispatch async LLM call
- `ctrl+d / ctrl+u` → scroll conversation
- Other keys → forward to text input

**Keybinding:** `@` key and `gc` chord both activate copilot.

---

## Team Formation for Parallel Execution

```
Phase 1 (parallel):   [Team A: Events]  [Team B: Memory]  [Team D: TUI scaffold]
                              │                │                    │
Phase 2 (sequential):         └───────┬────────┘                   │
                                      │                            │
                              [Team C: Daemon + LLM]               │
                                      │                            │
Phase 3 (integration):                └────────────────────────────┘
                                      [Team D: wire real client]
```

### Team A: Event Journal
**Files to create:** `internal/copilot/events.go`, `internal/copilot/journal.go`
**Files to modify:** `internal/daemon/daemon.go` (add journal field + init), `internal/daemon/daemon_poll.go` (emit events from patchSession + poll), `internal/daemon/daemon_synthesis.go` (emit after synth/digest)
**Boundary:** Exports `NewJournal()`, `Append()`, `RecentEvents()`, `ReadForSession()` for Team C.

### Team B: Identity & Memory
**Files to create:** `internal/copilot/identity.go`, `internal/copilot/memory.go`
**Files to modify:** None (pure library).
**Boundary:** Exports `LoadIdentity()`, `NewMemory()`, `ReadLongTerm()`, `AppendLongTerm()`, `Search()` for Team C.

### Team C: Daemon Endpoints & LLM
**Files to create:** `internal/copilot/prompt.go`, `internal/daemon/server_copilot.go`
**Files to modify:** `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`, `internal/claude/synthesize.go`
**Depends on:** Team A (journal reading) and Team B (identity/memory loading).
**Boundary:** Exports client methods `CopilotChat()`, `CopilotStatus()` for Team D.

### Team D: TUI Panel
**Files to create:** `internal/ui/copilot.go`, `internal/ui/copilot_view.go`, `internal/app/update_copilot.go`
**Files to modify:** `internal/app/model.go`, `internal/app/messages.go`, `internal/app/keymap.go`, `internal/app/update.go`, `internal/app/update_normal.go`, `internal/app/commands.go`, `internal/app/view.go`, `internal/app/view_footer.go`
**Phase 1:** Build with stub `sendCopilotChat` returning fake response after 1s delay.
**Phase 3:** Replace stub with real `m.client.CopilotChat()`.

---

## Verification

1. `make build` succeeds with no errors
2. Start daemon (`bin/cmc daemon`), verify `~/.cache/cmc/copilot/events/{today}.ndjson` gets populated as Claude Code sessions run
3. Verify `~/.cache/cmc/copilot/identity.md` gets auto-created with default content on first copilot interaction
4. Open TUI, press `@`, verify copilot panel replaces detail with input line + footer hints
5. Type "What sessions are active?" + Enter → spinner appears → response renders with actual session data
6. Type "Remember that the auth module needs refactoring" → response includes acknowledgment → verify `memory.md` updated
7. Press Esc → return to normal view, conversation history preserved
8. Press `@` again → previous conversation still visible
9. Press `;` → command palette → "Copilot" entry present and functional
