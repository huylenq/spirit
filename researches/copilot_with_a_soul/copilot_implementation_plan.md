# Copilot: A Persistent Companion Inside Mission Control

## Context

A copilot with soul — a persistent AI companion that lives inside cmc, aware of everything happening across Claude Code sessions. Inspired by OpenClaw's identity/memory architecture, adapted as a cmc feature.

**Key constraint:** NOT autonomous. Passively observes all events, responds only when user asks.

**What it is:** A chat panel in the TUI backed by an LLM with full context of live sessions, event history, and persistent memory.

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

## Implementation

### Event Journal (`internal/copilot/`)

The copilot's sensory input. An append-only NDJSON log of all daemon events, tapped into existing cmc internals.

No external reference — this is pure cmc plumbing with no OpenClaw equivalent. OpenClaw's awareness comes from being the agent itself; the copilot's awareness comes from observing other agents externally.

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

**Emit points in existing cmc code:**
- `daemon_poll.go:patchSession()` — status changes, git commits, compaction, skills, session removal
- `daemon_poll.go:poll()` — new session detection (compare prev vs curr session IDs)
- `daemon_synthesis.go:autoSynthesize()` — after successful synthesis
- `daemon_synthesis.go:triggerDigest()` — after digest generation
- `daemon.go` — add `journal *copilot.Journal` field, init in `Run()`

**Files to create:** `internal/copilot/events.go`, `internal/copilot/journal.go`
**Files to modify:** `internal/daemon/daemon.go`, `internal/daemon/daemon_poll.go`, `internal/daemon/daemon_synthesis.go`

---

### Identity (`internal/copilot/`)

The copilot's self-concept. A markdown file loaded fresh on every interaction.

> **OpenClaw reference** (`workspace.ts:498-555`, `bootstrap.ts:125-257`): Identity files loaded in strict order at session start. Created from embedded templates on first run via `ensureAgentWorkspace()`. Each file individually budget-capped at 20,000 chars; total budget 150,000 chars. Truncation preserves head (70%) + tail (20%) with `[...truncated...]` marker.

> **Goclaw reference** (`agent/memory.go:162-200`): `EnsureBootstrapFiles()` creates defaults on first run — IDENTITY.md, SOUL.md, USER.md, AGENTS.md. Pattern: check if file exists, write default if missing.

**Identity API:**
```go
type Identity struct {
    Raw string // full markdown content of identity.md
}
func LoadIdentity() (*Identity, error)    // reads identity.md, creates default if missing
func DefaultIdentityContent() string      // returns the default identity.md template
```

**Storage:** `~/.cache/cmc/copilot/identity.md`

Default `identity.md`:
```markdown
# Mission Control Copilot

You are the copilot for Huy's mission control — a persistent companion aware of
all Claude Code sessions running across tmux.

**Style:** Concise, technical, aware of specific session details. Reference
projects, headlines, and events by name. Be direct.

**You have access to:**
- Live session states (status, project, headline, git branch, overlaps)
- Today's event journal (every hook event, tool call, commit, compaction)
- Your long-term memory (facts Huy asked you to remember)
- The workspace digest (cross-session summary)
```

**Files to create:** `internal/copilot/identity.go`

---

### Memory (`internal/copilot/`)

The copilot's persistence across sessions. Two tiers: evergreen long-term facts and dated daily logs.

> **OpenClaw reference** (`memory/temporal-decay.ts:44-80`): Two memory categories with different durability. `MEMORY.md` and non-dated files under `memory/` are **evergreen** — never decay in search ranking. Dated logs (`memory/YYYY-MM-DD.md`) decay exponentially. All memory files loaded on session start (`manager-sync-ops.ts:672`), not just today+yesterday — temporal decay handles recency during ranking, not during loading.

> **Goclaw reference** (`agent/memory.go`): Three-layer MemoryStore: `ReadToday()`/`AppendToday()` for daily logs, `ReadLongTerm()`/`AppendLongTerm()` for MEMORY.md, `GetMemoryContext()` assembles both into formatted context. Faithful to OpenClaw's hierarchy.

**Storage layout:**
```
~/.cache/cmc/copilot/
├── identity.md       # persona (evergreen)
├── memory.md         # curated long-term facts (evergreen — never decays)
├── events/           # NDJSON event journals
│   └── YYYY-MM-DD.ndjson
└── daily/            # narrative daily logs (temporal decay applies)
    └── YYYY-MM-DD.md
```

**Memory API:**
```go
type Memory struct{ baseDir string }
func NewMemory() *Memory
func (m *Memory) ReadLongTerm() (string, error)            // reads memory.md
func (m *Memory) AppendLongTerm(fact string) error          // appends to memory.md with timestamp
func (m *Memory) ReadDailyLog(date string) (string, error)
func (m *Memory) WriteDailyLog(date, content string) error
func (m *Memory) Search(query string) ([]SearchResult, error)
```

**Files to create:** `internal/copilot/memory.go`

---

### Memory Search (`internal/copilot/search/`)

Ranked search across all memory files. V1 uses substring matching with OpenClaw's ranking algorithms; vector search is a future upgrade.

#### Search Pipeline

1. **Keyword extraction** — strip stop words, extract meaningful tokens
2. **Substring match** — scan memory markdown files, score by keyword density
3. **Temporal decay** — evergreen files (memory.md) score × 1.0; daily logs decay with 30-day half-life
4. **MMR re-ranking** — Jaccard-based diversity to avoid near-duplicate results
5. **Normalize + limit** — min-max normalize scores, return top N

This mirrors OpenClaw's pipeline (`vector + BM25 → temporal decay → MMR`), substituting substring matching for the vector component.

#### Ported Algorithms

**Temporal Decay** — port from `goclaw/memory/temporal_decay.go` (156 lines, zero deps)

> **OpenClaw reference** (`memory/temporal-decay.ts:17-34`): `multiplier = exp(-λ × age)` where `λ = ln(2) / halfLifeDays`. Validated: at 30 days, score × 0.5 exactly (`temporal-decay.test.ts:47`). Evergreen detection via `isEvergreenMemoryPath()` — checks if file is `MEMORY.md` or non-dated file under `memory/`.

> **Goclaw reference** (`memory/temporal_decay.go`): Faithful port. `toDecayLambda()`, `calculateTemporalDecayMultiplier()`, `isEvergreenMemoryPath()`, `parseMemoryDateFromPath()` all match OpenClaw behavior.

Port as `internal/copilot/search/temporal_decay.go`. Adapt to copilot's result types.

**MMR Re-ranking** — port from `goclaw/memory/mmr.go` (149 lines, zero deps)

> **OpenClaw reference** (`memory/mmr.ts:100-183`): `MMR = λ × relevance − (1−λ) × max_similarity`. Default λ=0.7. Jaccard similarity on tokenized content. Greedy iterative selection. Default **disabled** — opt-in (`memory-search.ts:106`).

> **Goclaw reference** (`memory/mmr.go`): Faithful port. Algorithm matches exactly.

Port as `internal/copilot/search/mmr.go`. Copy nearly as-is.

**Keyword Extraction** — cherry-pick from `goclaw/memory/hybrid.go`

> **OpenClaw reference** (`memory/hybrid.ts:57-155`): Weighted merge `score = 0.7 × vector + 0.3 × text`. Union ranking (not intersection). `BM25RankToScore()`: `1/(1+rank)`.

> **Goclaw reference** (`memory/hybrid.go`): `ExtractKeywords()` with stop word list, `NormalizeScores()` min-max normalization, `BM25RankToScore()` — all faithful.

> **Goclaw divergence**: `isInf`/`isNaN` reimplemented instead of using `math.IsInf`/`math.IsNaN`. Use stdlib.

Cherry-pick `ExtractKeywords()`, `NormalizeScores()`, `BM25RankToScore()` into `internal/copilot/search/keywords.go`.

**Text Chunking** — cherry-pick from `goclaw/memory/vector.go`

> **OpenClaw reference** (`memory/internal.ts:334-416`): `chunkMarkdown()` with 400-token target (~1600 chars at 4:1 ratio) and **80-token overlap** (~320 chars carry-over between chunks).

> **Goclaw divergence**: `ChunkText()` has **no overlap** — goclaw forgot to implement it. Must add overlap when porting:
> ```go
> func ChunkText(text string, maxTokens, overlapTokens int) []string
> ```

Port into `internal/copilot/search/chunk.go`. Skip vector math functions.

#### Defaults (OpenClaw ground truth)

From `agents/memory-search.ts:90-111`. **Use these values, not goclaw's divergent ones:**

| Parameter | OpenClaw | goclaw (divergent) | Notes |
|---|---|---|---|
| Max results | **6** | 10 | Tighter result set |
| Min score | **0.35** | 0.7 | goclaw's is too aggressive |
| Chunk tokens | **400** | 400 | Match |
| Chunk overlap | **80** | 0 (missing) | goclaw forgot overlap |
| MMR enabled | **`false`** | `true` | Opt-in. Enable for copilot since memory will accumulate. |
| MMR lambda | **0.7** | 0.7 | Match |
| Temporal decay enabled | **`false`** | `true` | Opt-in. Enable for copilot — recency-biased ranking is desired. |
| Temporal decay half-life | **30 days** | 30 days | Match |
| Candidate multiplier | **4** | — | Fetch 4× limit, then re-rank |

**No SQLite for v1.** Ported algorithms operate on in-memory result slices. Upgrade path: add `modernc.org/sqlite` (pure Go, zero CGo) with FTS5 when search volume warrants it — the schema from `goclaw/memory/store.go:initFTS()` is correct, only the query implementation needs writing (goclaw's `searchFTS` is a stub).

**Files to create:** `internal/copilot/search/temporal_decay.go`, `internal/copilot/search/mmr.go`, `internal/copilot/search/keywords.go`, `internal/copilot/search/chunk.go`

---

### Prompt Builder (`internal/copilot/`)

Assembles the system prompt from identity, memory, events, and live session state.

> **OpenClaw reference — token budgeting** (`bootstrap.ts:125-257`): Each context file individually capped at 20,000 chars. Total budget across all files: 150,000 chars. Files loaded in priority order; if budget exhausted, later files (memory) get cut — identity never does. Truncation: head 70% + `[...truncated...]` + tail 20%.

> **OpenClaw reference — load order** (`workspace.ts:505-535`): AGENTS → SOUL → TOOLS → IDENTITY → USER → HEARTBEAT → BOOTSTRAP → MEMORY → memory/*. Design insight: **identity first, memory last.** Memory is the overflow buffer.

**Prompt construction:**
```go
func BuildSystemPrompt(
    identity *Identity,
    longTermMemory string,
    recentEvents []CopilotEvent,
    sessions []claude.ClaudeSession,
    digest *claude.WorkspaceDigest,
) string
```

**System prompt structure** (ordered by priority — identity never truncated, events/memory are the overflow buffer):

```
{identity.md content}                              ← always loaded in full

## Live Sessions
{formatted table: project | status | headline | branch | flags}

## Workspace Digest
{cached digest summary}

## Your Memory                                     ← truncate: head 70% + tail 20%
{memory.md content}

## Recent Activity (last ~50 events)               ← truncate: most recent wins
{timeline of today's events}

## Behavior
- When asked to remember something, include [REMEMBER: fact] in your response.
- Reference sessions by name/project. Be specific about times and events.
- You are NOT autonomous. Only respond to what is asked.
```

Each section has a character budget. If total exceeds cap, events and memory truncate first (they're reconstructable); identity and live sessions never do (they're the copilot's core awareness).

**`[REMEMBER]` extraction:** After LLM response, `handleCopilotChat` scans for `[REMEMBER: ...]` tags and auto-appends to `memory.md`. Memory writes stay in-band — no separate "remember" RPC.

**Files to create:** `internal/copilot/prompt.go`

---

### Daemon Endpoints & LLM (`internal/daemon/`, `internal/copilot/`)

RPC handlers connecting the TUI to the copilot's brain.

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

**LLM helper:**
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

**Handler:**
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

Blocking RPC — the TUI dispatches in a `tea.Cmd` goroutine.

**Files to create:** `internal/daemon/server_copilot.go`
**Files to modify:** `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`, `internal/daemon/daemon.go` (add `journal` + `memory` fields), `internal/claude/synthesize.go`

---

### TUI Panel (`internal/ui/`, `internal/app/`)

Chat interface replacing the detail panel when active.

No external reference — pure cmc/Bubble Tea. Follows existing patterns: backlog preview already replaces detail panel; relay model already handles text input.

**Model additions:**
```go
StateCopilot  // new app state

// On Model struct:
copilot         ui.CopilotModel
copilotMessages []CopilotMessage  // ephemeral, per TUI session
copilotInput    ui.RelayModel     // text input (reuses existing component)
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

**View integration** (follows backlog preview pattern):
```go
} else if m.state == StateCopilot {
    detailContent = ui.RenderCopilotChat(m.copilotMessages, detailWidth, detailH,
        m.copilot.Scroll(), m.copilotThinking, m.spinner.View())
```

**State handler:**
- `Esc` → return to `StateNormal` (conversation preserved)
- `Enter` → submit, set `copilotThinking = true`, dispatch async LLM call
- `ctrl+d / ctrl+u` → scroll conversation
- Other keys → forward to text input

**Keybinding:** `@` key and `gc` chord both activate copilot.

**Files to create:** `internal/ui/copilot.go`, `internal/ui/copilot_view.go`, `internal/app/update_copilot.go`
**Files to modify:** `internal/app/model.go`, `internal/app/messages.go`, `internal/app/keymap.go`, `internal/app/update.go`, `internal/app/update_normal.go`, `internal/app/commands.go`, `internal/app/view.go`, `internal/app/view_footer.go`

---

## Goclaw Skip List

These goclaw files are divergent from OpenClaw or broken — do not port:

| goclaw code | Why skip |
|---|---|
| `memory/store.go` | Broken — `searchFTS()` is a stub returning empty. Vector search tries to load C extension (`sqlite-vec`) incompatible with pure-Go SQLite. `NewSQLiteStore` requires non-nil provider but builtin manager passes nil. |
| `memory/search_manager.go` | goclaw-specific dual-backend (Builtin vs QMD) with fallback logic. Not an OpenClaw pattern. |
| `memory/qmd/` | Entirely goclaw-specific external CLI tool. Not part of OpenClaw. |
| `memory/embeddings.go` | Hardcodes OpenAI. OpenClaw is model-agnostic. |
| `memory/lru_cache.go` | Generic utility, not OpenClaw-specific. |
| `memory/citations.go` | goclaw's citation formatting. Not core OpenClaw behavior. |
| `memory/types.go` | Coupled to broken SQLite store. Define copilot's own types. |

---

## Future Enhancements

Design patterns from OpenClaw that are out of scope for v1 but worth implementing later. Documented here so the v1 architecture doesn't accidentally preclude them.

### Pre-Compaction Memory Flush

> **OpenClaw reference** (`auto-reply/reply/memory-flush.ts`)

When a Claude Code session approaches context limits, auto-write durable memories before compaction destroys nuance. cmc already tracks `CompactCount` per session — when it increments, this could trigger.

**Threshold:** `totalTokens >= contextWindow - reserveTokensFloor(20K) - softThreshold(4K)`

**Prompt template** (from OpenClaw `memory-flush.ts:25-41`):
```
Pre-compaction memory flush.
Store durable memories only in memory/YYYY-MM-DD.md (create memory/ if needed).
Treat workspace bootstrap/reference files such as MEMORY.md, SOUL.md, TOOLS.md,
and AGENTS.md as read-only during this flush; never overwrite, replace, or edit them.
If memory/YYYY-MM-DD.md already exists, APPEND new content only and do not
overwrite existing entries.
Do NOT create timestamped variant files (e.g., YYYY-MM-DD-HHMM.md); always use
the canonical YYYY-MM-DD.md filename.
If nothing to store, reply with NO_REPLY.
```

**Safety rules:** append-only, one flush per compaction cycle, identity files read-only during flush, two triggers (token threshold OR transcript > 2MB).

### Session Death Archival

> **OpenClaw reference** (`hooks/bundled/session-memory/handler.ts:315`)

When cmc detects session death (disappears from poll), auto-archive: take the session's last synthesis headline + final status and append to the copilot's daily log. Use the existing headline as the slug — no LLM needed.

OpenClaw's full version: extracts last ~15 messages, LLM-generates a descriptive slug (e.g., `auth-middleware-refactor`), saves to `memory/YYYY-MM-DD-{slug}.md`. This is an opt-in bundled hook, not core behavior.

### Compaction with Identifier Preservation

> **OpenClaw reference** (`agents/compaction.ts:31-70`)

If copilot conversation history grows too long, compact with strict identifier preservation:
```
Preserve all opaque identifiers exactly as written (no shortening or
reconstruction), including UUIDs, hashes, IDs, tokens, API keys,
hostnames, IPs, ports, URLs, and file names.
```

Merge prioritizes: active tasks, batch progress, last user request, decisions/rationale, TODOs and constraints.

### FTS5 Search Upgrade

Replace substring matching with `modernc.org/sqlite` (pure Go, zero CGo) FTS5 full-text search. Schema from `goclaw/memory/store.go:initFTS()` is correct — just the query implementation needs writing. Enables proper BM25 scoring, which slots into the existing hybrid merge pipeline.

### Session Filtering by Context Type

> **OpenClaw reference** (`workspace.ts:565-573`)

If cmc gains multi-agent orchestration, the copilot's memory should NOT leak into orchestrated sessions. Design the prompt builder with a `sessionType` parameter from the start — subagents get reduced context (no MEMORY.md, no daily logs).

---

## Team Formation for Parallel Execution

```
Phase 1 (parallel):   [Team A: Events]  [Team B: Memory+Search]  [Team D: TUI scaffold]
                              │                │                          │
Phase 2 (sequential):         └───────┬────────┘                         │
                                      │                                  │
                              [Team C: Daemon+LLM+Prompt]                │
                                      │                                  │
Phase 3 (integration):                └──────────────────────────────────┘
                                      [Team D: wire real client]
```

### Team A: Event Journal
**Files to create:** `internal/copilot/events.go`, `internal/copilot/journal.go`
**Files to modify:** `internal/daemon/daemon.go`, `internal/daemon/daemon_poll.go`, `internal/daemon/daemon_synthesis.go`
**Boundary:** Exports `NewJournal()`, `Append()`, `RecentEvents()`, `ReadForSession()` for Team C.

### Team B: Identity, Memory & Search
**Files to create:** `internal/copilot/identity.go`, `internal/copilot/memory.go`, `internal/copilot/search/temporal_decay.go`, `internal/copilot/search/mmr.go`, `internal/copilot/search/keywords.go`, `internal/copilot/search/chunk.go`
**Port from goclaw:** `memory/temporal_decay.go` → adapt types. `memory/mmr.go` → adapt types. `memory/hybrid.go` → cherry-pick `ExtractKeywords`/`NormalizeScores`/stop words. `memory/vector.go` → cherry-pick `ChunkText` + add 80-token overlap per OpenClaw spec.
**Files to modify:** None (pure library).
**Boundary:** Exports `LoadIdentity()`, `NewMemory()`, `ReadLongTerm()`, `AppendLongTerm()`, `Search()` for Team C.

### Team C: Daemon Endpoints, Prompt Builder & LLM
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
2. Start daemon (`bin/cmc daemon`), verify `~/.cache/cmc/copilot/events/{today}.ndjson` populates as sessions run
3. Verify `~/.cache/cmc/copilot/identity.md` auto-created with default content on first copilot interaction
4. Open TUI, press `@`, verify copilot panel replaces detail with input line + footer hints
5. Type "What sessions are active?" + Enter → spinner → response with actual session data
6. Type "Remember that the auth module needs refactoring" → acknowledgment → verify `memory.md` updated
7. Press Esc → normal view, conversation history preserved
8. Press `@` again → previous conversation still visible
9. Press `;` → command palette → "Copilot" entry present and functional
