# Copilot: A Persistent Companion Inside Mission Control

## Context

A copilot with soul — a persistent AI companion that lives inside cmc, aware of everything happening across Claude Code sessions. Inspired by OpenClaw's identity/memory architecture, adapted as a cmc feature.

**Key constraint:** NOT autonomous. Passively observes all events, responds only when user asks.

**What it is:** A chat panel in the TUI backed by an agentic LLM that can both observe and act on sessions — query state, send messages, groom backlogs, manage lifecycle — via MCP tools mapped to cmc's existing Lua scripting API.

**OpenClaw compatibility constraint:** The copilot's workspace MUST be a valid OpenClaw workspace, so the user can point OpenClaw to it (`openclaw setup --workspace <path>` or `agents.defaults.workspace` in config). This means the directory layout, file naming, and bootstrap file structure must match OpenClaw's expectations exactly.

---

## Workspace Layout (OpenClaw-Compatible)

The copilot workspace lives at `~/.cache/cmc/copilot/` and is a valid OpenClaw workspace:

```
~/.cache/cmc/copilot/
├── .openclaw/
│   └── workspace-state.json    # OpenClaw workspace marker (version + onboarding state)
├── CLAUDE.md                   # Derived from bootstrap files — Claude Code reads this on session start
├── SOUL.md                     # Copilot behavioral instructions (who you are, how you think)
├── IDENTITY.md                 # Copilot calling card (name, emoji, vibe — minimal)
├── USER.md                     # Who Huy is (timezone, preferences, context)
├── AGENTS.md                   # Operating instructions (session startup, memory rules)
├── TOOLS.md                    # Environment notes (cmc-specific awareness capabilities)
├── HEARTBEAT.md                # Empty (opt-in periodic behavior, unused for now)
├── MEMORY.md                   # Curated long-term facts (evergreen, never decays in search)
├── memory/                     # OpenClaw-standard memory directory
│   └── YYYY-MM-DD.md           # Daily narrative logs (temporal decay applies in search)
└── events/                     # cmc-specific event journal (ignored by OpenClaw, used by cmc)
    └── YYYY-MM-DD.ndjson       # Append-only NDJSON of all daemon events
```

### Why This Shape

> **OpenClaw source reference** (`workspace.ts:321-459`): `ensureAgentWorkspace()` checks if a directory is "brand new" by testing for the absence of ALL 7 core files + `memory/` + `MEMORY.md` + `.git/`. If any exist, it treats the workspace as already initialized and skips template seeding.

> **OpenClaw source reference** (`workspace.ts:162-252`): `workspace-state.json` with `onboardingCompletedAt` set marks the workspace as fully initialized. Without this, OpenClaw triggers its bootstrap ritual (interactive Q&A to seed files).

> **OpenClaw source reference** (`workspace.ts:498-555`): `loadWorkspaceBootstrapFiles()` loads files by exact uppercase name. Missing files get `missing: true` but don't fail. Case-sensitive on macOS/Linux. Only `MEMORY.md` has a lowercase `memory.md` fallback.

> **OpenClaw source reference** (`internal.ts:74-183`): Memory discovery scans `memory/` recursively for any `.md` files. Dated files (`memory/YYYY-MM-DD.md`) get temporal decay; non-dated files are evergreen. Extra directories like `events/` are completely ignored.

### File Purposes (OpenClaw Semantics vs cmc Usage)

| File | OpenClaw Purpose | cmc Copilot Usage |
|------|-----------------|-------------------|
| `CLAUDE.md` | *(no equivalent — cmc-specific)* | Derived system prompt for Claude Code, auto-generated from bootstrap files. Claude Code reads this on ACP session start. |
| `SOUL.md` | Internal values, behavioral rules | Copilot personality, response style, cmc-awareness instructions |
| `IDENTITY.md` | External presentation (name, emoji, vibe) | Copilot name + emoji (minimal, ~400 chars) |
| `USER.md` | Who the human is | Huy's preferences, timezone, working style |
| `AGENTS.md` | Operating instructions, memory rules | Session startup rules, memory hygiene, cmc-specific protocols |
| `TOOLS.md` | Environment-specific setup notes | What the copilot can see (live sessions, events, digest) |
| `HEARTBEAT.md` | Periodic task checklist | Empty (autonomous actions off the table for now) |
| `MEMORY.md` | Curated long-term knowledge | Persistent facts Huy asked the copilot to remember |
| `memory/*.md` | Daily logs, topic files | Daily narrative logs of cmc activity |
| `events/*.ndjson` | *(no equivalent)* | cmc daemon event stream (copilot's sensory input) |

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Daemon                                                                    │
│  ┌────────────┐  ┌──────────────┐  ┌──────────────────────────────────┐   │
│  │ Event      │  │ Workspace    │  │ Copilot RPC Handler              │   │
│  │ Journal    │  │ (Bootstrap   │  │ ┌──────────────────────────────┐ │   │
│  │ (NDJSON)   │  │  + Memory)   │  │ │ ACP Client (Go)             │ │   │
│  └─────┬──────┘  └──────┬───────┘  │ │  ├─ ClientSideConnection   │ │   │
│        │                │          │ │  ├─ session/prompt           │ │   │
│        │                │          │ │  ├─ session/update callback  │ │   │
│        │                │          │ │  └─ permission callback      │ │   │
│        │                │          │ └──────────────────────────────┘ │   │
│  patchSession()   LoadWorkspace()  │            │ stdio pipes         │   │
│  poll()           ReadMemory()     │            ▼                     │   │
│  autoSynthesize() AppendMemory()   │  ┌────────────────────────┐     │   │
│                                    │  │ Claude Code ACP Bridge │     │   │
│                                    │  │ (subprocess)           │     │   │
│                                    │  └───────────┬────────────┘     │   │
│                                    │              │ spawns            │   │
│                                    │              ▼                   │   │
│                                    │  ┌────────────────────────┐     │   │
│                                    │  │ cmc mcp-serve          │     │   │
│                                    │  │ (stdio MCP server)     │     │   │
│                                    │  │ connects to daemon.sock│     │   │
│                                    │  └────────────────────────┘     │   │
│                                    └──────────────┬──────────────────┘   │
└────────┼────────────────┼─────────────────────────┼──────────────────────┘
         │                │                         │
         │         ┌──────┴─────────────────────────┴──┐
         │         │            TUI                      │
         │         │  ┌──────────────────────────────┐   │
         │         │  │ Copilot Panel                │   │
         │         │  │ (replaces detail panel)      │   │
         │         │  │ - scrollable chat history    │   │
         │         │  │ - streaming text chunks      │   │
         │         │  │ - tool call indicators       │   │
         │         │  │ - text input at bottom       │   │
         │         │  └──────────────────────────────┘   │
         │         └─────────────────────────────────────┘
```

### Agentic Loop (ACP — Agent Client Protocol)

The copilot uses the **Agent Client Protocol** (ACP) — a JSON-RPC 2.0 over stdio protocol for editor↔agent communication — to run Claude Code as an agentic subprocess. ACP is the standardized protocol that Claude Code natively supports, used by Zed, JetBrains, and Neovim for AI agent integration.

```
User message
  → Daemon builds context preamble (live sessions + events + digest)
  → conn.Prompt(ctx, {sessionId, preamble + message}) via ACP
    → Claude Code reasons about the request
    → Claude Code calls MCP tools via cmc mcp-serve subprocess
    → cmc mcp-serve connects to daemon socket, executes command
    → Tool result fed back to Claude Code
    → Claude Code calls more tools or produces final text
  → Client.SessionUpdate() fires for each streaming chunk
  → Daemon pushes chunks to TUI via subscribe stream
  → TUI renders incrementally (text chunks + tool call indicators)
  → conn.Prompt() returns with StopReason when turn completes
```

Each tool call is a round-trip through the LLM. A multi-tool request like "groom my backlogs" may take 5+ tool calls × 2-3s each. Streaming mitigates perceived latency — the user sees progress as it happens via the `SessionUpdate` callback.

**ACP dependency:** [`github.com/coder/acp-go-sdk`](https://github.com/coder/acp-go-sdk) — mature Go ACP client SDK by Coder. Requires Claude Code ACP bridge ([`@zed-industries/claude-code-acp`](https://github.com/zed-industries/claude-code-acp), spawned via `npx`). No direct Anthropic API key needed — Claude Code handles auth via existing CLI credentials.

**Why ACP over the Claude Agent SDK Go port:** ACP is a published, multi-implementor standard (Zed, JetBrains, Coder) with a stable Go SDK. The community Claude Agent SDK Go port (`schlunsen/claude-agent-sdk-go`) is single-maintainer and lags behind upstream. ACP gives us streaming, permission handling, tool call visibility, plans, and usage tracking out of the box — all as first-class protocol features rather than SDK-specific abstractions.

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

### Workspace Initialization (`internal/copilot/`)

First-run setup that creates a valid OpenClaw workspace with cmc-specific defaults. Runs once; subsequent starts load existing files.

> **OpenClaw source reference** (`workspace.ts:321-459`): `ensureAgentWorkspace()` determines "brand new" by checking absence of ALL 7 core template files + `memory/` + `MEMORY.md` + `.git/`. If any exist, skips seeding. Template files are created from embedded defaults on first run only.

> **OpenClaw source reference** (`workspace.ts:162-252`): `.openclaw/workspace-state.json` with `onboardingCompletedAt` set prevents the bootstrap ritual (interactive Q&A). Without it, OpenClaw would try to interactively initialize the workspace.

> **OpenClaw source reference** (`workspace.ts:498-555`): `loadWorkspaceBootstrapFiles()` loads by exact uppercase filename. Missing files get `missing: true` but don't fail. Each file capped at 2MB. Frontmatter (YAML `---` headers) is stripped before injection.

**Workspace API:**
```go
type Workspace struct {
    Dir string // ~/.cache/cmc/copilot/
}

func NewWorkspace() *Workspace
func (w *Workspace) EnsureInitialized() error  // creates default files if brand-new
func (w *Workspace) LoadBootstrapFiles() (*BootstrapFiles, error)

type BootstrapFiles struct {
    Soul      string // SOUL.md content
    Identity  string // IDENTITY.md content
    User      string // USER.md content
    Agents    string // AGENTS.md content
    Tools     string // TOOLS.md content
    Heartbeat string // HEARTBEAT.md content (empty for now)
}
```

**`EnsureInitialized()` creates these files if missing:**

#### `.openclaw/workspace-state.json`
```json
{
  "version": 1,
  "onboardingCompletedAt": "2026-03-13T10:00:00Z"
}
```
Set `onboardingCompletedAt` immediately — cmc handles its own initialization, no need for OpenClaw's interactive bootstrap.

#### `SOUL.md` — Behavioral instructions (the "who you are")
```markdown
# SOUL.md — Who You Are

You are the copilot for mission control — a persistent companion aware of all
Claude Code sessions running across tmux panes.

## Core Truths
- You observe everything and act only when spoken to.
- When asked to do something, USE YOUR TOOLS. Don't describe — execute.
- You have genuine opinions about what's happening across sessions.
- Be concise and technical. Reference projects, headlines, and events by name.
- You are NOT a chatbot. You're an operator with full situational awareness.

## What You See
- Live session states (status, project, headline, git branch, file overlaps)
- Today's event journal (every hook event, tool call, commit, compaction)
- Your long-term memory (facts you've been asked to remember)
- The workspace digest (cross-session summary)

## Style
Direct. Smartly humorous. Sarcasm welcome. No filler, no fluff.
When you see something interesting across sessions, say so — don't wait to be
asked about it specifically.
```

#### `IDENTITY.md` — External presentation (the calling card)
```markdown
# Who Am I?

- **Name:** Copilot
- **Creature:** mission control intelligence — sees all sessions, forgets nothing
- **Vibe:** sharp, observant, dry wit
- **Emoji:** 🛰️
```

#### `USER.md` — Who Huy is
```markdown
# USER.md

- **Name:** Huy
- **Timezone:** Asia/Saigon (GMT+7)
- **Preferences:** Direct communication, no sugar-coating. Smartly humorous.
- **Context:** Software engineer running multiple Claude Code sessions simultaneously via tmux + cmc (claude-mission-control).
```

#### `AGENTS.md` — Operating instructions
```markdown
# AGENTS.md — Your Workspace

## Every Interaction
1. Read SOUL.md (who you are)
2. Read USER.md (who Huy is)
3. Check today's event journal (what happened)
4. Check MEMORY.md (what you've been told to remember)

## Memory
- TEXT > BRAIN — if it matters, write it to memory.
- Daily logs (`memory/YYYY-MM-DD.md`) are raw session activity.
- `MEMORY.md` is curated long-term knowledge.
- When asked to remember something, use the memory_append tool.

## Actions
- You have MCP tools to interact with sessions, backlogs, and memory.
- Use tools to act, not just describe. If asked to "groom backlogs", actually
  read them, reorganize, and update — don't just suggest changes.
- Dangerous actions (send_message, kill, spawn) will require user confirmation.
```

#### `TOOLS.md` — Environment-specific notes
```markdown
# TOOLS.md

## Mission Control (cmc)
- TUI monitoring tool for Claude Code sessions across tmux
- Daemon polls sessions every ~1s, pushes updates via Unix socket
- Hook events: UserPromptSubmit, PreToolUse, PostToolUse, Stop, SubagentStop
- Synthesis: auto-summarizes sessions via Haiku when they go idle
- Digest: cross-session summary regenerated after synthesis

## Your MCP Tools (mcp__cmc__*)
You have tools to interact with cmc. Use them — don't just describe what you'd do.

**Read-only (always available):**
sessions_list, session_get, transcript, raw_transcript, diff_stats, diff_hunks,
summary, hook_events, backlog_list, memory_read, memory_search, daily_log_read

**Write (always available):**
memory_append, backlog_create, backlog_update, backlog_delete, synthesize,
synthesize_all, bookmark, queue_message

**Actions (require user confirmation):**
send_message, kill_session, spawn_session, commit, commit_done, bookmark_kill
```

#### `HEARTBEAT.md` — Empty (opt-in, unused)
```markdown
# HEARTBEAT.md

# Keep this file empty to skip heartbeat API calls.
# Add tasks below when you want the copilot to check something periodically.
```

#### `MEMORY.md` — Starts empty
```markdown
# Long-Term Memory

<!-- Facts the copilot has been asked to remember. Append-only. -->
```

**Files to create:** `internal/copilot/workspace.go`

---

### Memory (`internal/copilot/`)

The copilot's persistence across sessions. Two tiers: evergreen long-term facts and dated daily logs.

> **OpenClaw source reference** (`memory/temporal-decay.ts:17-34`): Two memory categories with different durability. `MEMORY.md` and non-dated files under `memory/` are **evergreen** — never decay in search ranking. Dated logs (`memory/YYYY-MM-DD.md`) decay exponentially with 30-day half-life. Evergreen detection via `isEvergreenMemoryPath()` — checks if file is `MEMORY.md` or non-dated file under `memory/`.

> **OpenClaw source reference** (`internal.ts:74-183`): Memory discovery scans `memory/` recursively for any `.md` files. No date-based naming requirement — any `.md` in `memory/` is indexed. Temporal decay is applied during search ranking, not during loading.

> **Goclaw reference** (`agent/memory.go`): Three-layer MemoryStore: `ReadToday()`/`AppendToday()` for daily logs, `ReadLongTerm()`/`AppendLongTerm()` for MEMORY.md, `GetMemoryContext()` assembles both into formatted context.

**Storage layout** (subset of workspace — see Workspace Layout above):
```
~/.cache/cmc/copilot/
├── MEMORY.md         # curated long-term facts (evergreen — never decays)
└── memory/           # OpenClaw-standard memory directory
    └── YYYY-MM-DD.md # daily narrative logs (temporal decay applies in search)
```

**Memory API:**
```go
type Memory struct{ baseDir string }  // baseDir = ~/.cache/cmc/copilot/
func NewMemory() *Memory
func (m *Memory) ReadLongTerm() (string, error)            // reads MEMORY.md (uppercase)
func (m *Memory) AppendLongTerm(fact string) error          // appends to MEMORY.md with timestamp
func (m *Memory) ReadDailyLog(date string) (string, error)  // reads memory/YYYY-MM-DD.md
func (m *Memory) WriteDailyLog(date, content string) error  // writes memory/YYYY-MM-DD.md
func (m *Memory) Search(query string) ([]SearchResult, error)
```

> **OpenClaw compatibility note**: `AppendLongTerm()` writes to `MEMORY.md` (root-level, uppercase). `ReadDailyLog()`/`WriteDailyLog()` use `memory/YYYY-MM-DD.md` (not `daily/`). This matches OpenClaw's `isEvergreenMemoryPath()` — root `MEMORY.md` is evergreen; `memory/YYYY-MM-DD.md` files decay temporally. Append format: double newline separator (`"\n\n"`) between entries, matching goclaw's convention.

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

### Prompt Architecture (`internal/copilot/`)

With ACP, the prompt is split into two layers: **static identity** (read once per session by Claude Code from the workspace) and **dynamic context** (injected per prompt as a preamble).

#### Static Identity — `CLAUDE.md` in Copilot Workspace

Claude Code reads `CLAUDE.md` from the session's `cwd` at startup. The copilot workspace's `CLAUDE.md` is synthesized from the OpenClaw-compatible bootstrap files during `EnsureInitialized()`:

```markdown
# Copilot — Mission Control Intelligence

{AGENTS.md content}    ← operating instructions
{SOUL.md content}      ← behavioral identity
{TOOLS.md content}     ← MCP tool awareness
{IDENTITY.md content}  ← calling card (~400 chars)
{USER.md content}      ← user context

## Behavior
- Use your MCP tools to take actions. Don't just describe what you would do — do it.
- When asked to remember something, use the memory_append tool.
- Reference sessions by name/project. Be specific about times and events.
- You act ONLY when asked. You do not take autonomous actions.
```

This file is regenerated from bootstrap files on each daemon start (if any bootstrap file is newer than `CLAUDE.md`). OpenClaw-compatible files remain the source of truth; `CLAUDE.md` is a derived artifact.

> **OpenClaw source reference — load order** (`workspace.ts:505-535`): AGENTS → SOUL → TOOLS → IDENTITY → USER → HEARTBEAT → BOOTSTRAP → MEMORY → memory/*. Design insight: **identity first, memory last.** Memory is the overflow buffer.

> **OpenClaw source reference — subagent filtering** (`workspace.ts:565-573`): Subagents/cron sessions skip HEARTBEAT.md, BOOTSTRAP.md, and MEMORY.md. Only identity + operating files loaded.

#### Dynamic Context — Prompt Preamble

Before each `session/prompt`, the daemon builds a context preamble prepended to the user's message. This gives the agent instant situational awareness without requiring a tool call round-trip:

**Preamble construction:**
```go
func BuildContextPreamble(
    longTermMemory string,          // MEMORY.md content
    recentEvents []CopilotEvent,    // today's event journal
    sessions []claude.ClaudeSession,
    digest *claude.WorkspaceDigest,
) string
```

**Preamble structure:**
```
<context>
## Live Sessions
{formatted table: project | status | headline | branch | flags}

## Workspace Digest
{cached digest summary}

## Your Memory (MEMORY.md)                         ← truncate: head 70% + tail 20%
{MEMORY.md content}

## Recent Activity (last ~50 events)               ← truncate: most recent wins
{timeline of today's events from events/*.ndjson}
</context>
```

Each section has a character budget. If total exceeds cap, events and memory truncate first (they're reconstructable); live sessions never truncate (they're the copilot's core awareness).

**Why this split:** Static identity in `CLAUDE.md` is loaded once per ACP session and persists across all prompts in the session's context. Dynamic context changes every prompt (sessions come and go, events accumulate). The preamble ensures the agent always has fresh state without requiring tool calls for basic awareness, while MCP tools provide on-demand deep dives.

**Files to create:** `internal/copilot/prompt.go`, `internal/copilot/claudemd.go`

---

### MCP Tool Surface (`internal/copilot/`)

The copilot's ability to act. An in-process MCP server that maps cmc's 40 Lua scripting functions to MCP tools, giving the agentic LLM the same capabilities as `cmc eval`.

**Tool categories and mappings (Lua function → MCP tool):**

| Category | MCP Tool | Lua Origin | Description |
|----------|----------|------------|-------------|
| **Session awareness** | `sessions_list` | `sessions()` | List active sessions with optional status filter |
| | `session_get` | `session()` | Get single session by ID |
| | `session_selected` | `selected()` | Currently selected session in TUI |
| | `session_wait` | `wait()` | Block until session reaches target status |
| **Communication** | `send_message` | `send()` | Send message to session's tmux pane |
| | `queue_message` | `queue()` | Queue message for delivery when session idles |
| | `cancel_queue` | `cancel_queue()` | Cancel a queued message |
| **Lifecycle** | `spawn_session` | `spawn()` | Start new Claude session in directory |
| | `kill_session` | `kill()` | Terminate session (SIGTERM) |
| **Introspection** | `transcript` | `transcript()` | Get user messages from session |
| | `raw_transcript` | `raw_transcript()` | Full transcript with metadata |
| | `diff_stats` | `diff_stats()` | Per-file diff statistics |
| | `diff_hunks` | `diff_hunks()` | Individual diff hunks |
| | `summary` | `summary()` | Cached synthesis headline |
| | `hook_events` | `hook_events()` | Hook events for session |
| **Backlog** | `backlog_list` | `backlog_list()` | List backlog items for directory |
| | `backlog_create` | `backlog_create()` | Create backlog item |
| | `backlog_update` | `backlog_update()` | Update backlog item body |
| | `backlog_delete` | `backlog_delete()` | Delete backlog item |
| **Workflow** | `synthesize` | `synthesize()` | Generate LLM summary for session |
| | `synthesize_all` | `synthesize_all()` | Summarize all sessions |
| | `commit` | `commit()` | Send /commit to session |
| | `commit_done` | `commit_done()` | Commit and auto-kill on completion |
| | `bookmark` | `later()` | Bookmark session for later |
| | `bookmark_kill` | `later_kill()` | Bookmark and kill |
| **Memory** | `memory_read` | — | Read MEMORY.md content |
| | `memory_append` | — | Append fact to MEMORY.md |
| | `memory_search` | — | Search across all memory files |
| | `daily_log_read` | — | Read specific day's narrative log |

Not all 40 Lua functions are exposed. Excluded: `sleep()` (no purpose for LLM), `log()`/`flash()`/`toast()` (TUI-specific), `register_orchestrator()`/`unregister_orchestrator()` (orchestrator-only), `cancel_commit_done()` (edge case).

**MCP server implementation — `cmc mcp-serve` subcommand:**

The MCP server runs as a **separate stdio subprocess**, spawned by Claude Code when the ACP session is created. It connects to the daemon's Unix socket as a client (same pattern as `cmc eval`).

```go
// cmd/cmc/mcp_serve.go — new subcommand
func runMCPServe() {
    // 1. Connect to daemon socket
    client, err := daemon.NewClient()
    // 2. Load copilot memory
    memory := copilot.NewMemory()
    journal := copilot.NewJournal()
    // 3. Create MCP stdio server (using go-mcp or similar pure-Go MCP lib)
    server := mcp.NewStdioServer("cmc", "1.0")

    server.AddTool("sessions_list", sessionsListSchema, func(params map[string]any) (string, error) {
        resp, err := client.Sessions()
        // ... marshal to JSON
    })

    server.AddTool("backlog_list", backlogListSchema, func(params map[string]any) (string, error) {
        cwd := params["cwd"].(string)
        resp, err := client.BacklogList(cwd)
        // ...
    })

    server.AddTool("send_message", sendMessageSchema, func(params map[string]any) (string, error) {
        sid := params["session_id"].(string)
        msg := params["message"].(string)
        // ... resolve pane, send via client
    })

    server.AddTool("memory_append", memoryAppendSchema, func(params map[string]any) (string, error) {
        fact := params["fact"].(string)
        return "", memory.AppendLongTerm(fact)
    })

    // ... remaining tools
    // 4. Run stdio loop (blocks until stdin closes)
    server.Serve(os.Stdin, os.Stdout)
}
```

**Key design: `cmc mcp-serve` is independently testable.** It's a standard MCP server — any MCP client can connect to it. Debug with `echo '{"jsonrpc":"2.0",...}' | cmc mcp-serve`.

**Tool handlers call daemon client methods via the Unix socket** — same RPC backend as `cmc eval` and the TUI. The Lua API and MCP tools share the same daemon RPC protocol.

**Permission handling — ACP-native, not MCP-level:**

With ACP, permission prompts are handled by the protocol itself, not by MCP tool policies. Claude Code sends `session/request_permission` to the ACP client (the daemon) when it encounters a tool that requires approval. The daemon routes this to the TUI.

However, Claude Code's built-in permission model governs which tools trigger permission requests. To ensure dangerous tools always prompt:

| Tier | Tools | Claude Code Permission |
|------|-------|----------------------|
| **Read-only** (auto-approve) | `sessions_list`, `session_get`, `transcript`, `diff_stats`, `summary`, `backlog_list`, `memory_read`, `memory_search`, `daily_log_read`, `hook_events` | Allowed by default (read tools) |
| **Write** (auto-approve) | `memory_append`, `backlog_create`, `backlog_update`, `backlog_delete`, `synthesize`, `bookmark`, `queue_message` | Allowed — low risk, reversible |
| **Action** (require confirmation) | `send_message`, `kill_session`, `spawn_session`, `commit`, `commit_done`, `bookmark_kill` | Claude Code triggers `session/request_permission` → daemon routes to TUI → user sees `⚠ send_message(...) — allow? [y/n]` |

The daemon's ACP `RequestPermission` callback:
```go
func (c *copilotClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
    // Push permission prompt to TUI via subscribe stream
    c.pushMsg(CopilotStreamMsg{
        Type:    "confirm",
        Content: params.ToolCall.Title,
        ToolID:  string(params.ToolCall.ToolCallId),
    })
    // Block until user responds (via ReqCopilotConfirm RPC from TUI)
    decision := <-c.permissionCh
    if decision.Allowed {
        // Find the allow_once option
        for _, o := range params.Options {
            if o.Kind == acp.PermissionOptionKindAllowOnce {
                return acp.RequestPermissionResponse{
                    Outcome: acp.RequestPermissionOutcome{
                        Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId},
                    },
                }, nil
            }
        }
    }
    return acp.RequestPermissionResponse{
        Outcome: acp.RequestPermissionOutcome{
            Cancelled: &acp.RequestPermissionOutcomeCancelled{},
        },
    }, nil
}
```

**Files to create:** `cmd/cmc/mcp_serve.go`, `internal/copilot/mcp_tools.go` (tool definitions and schemas)

---

### Daemon Endpoints & ACP Client (`internal/daemon/`, `internal/copilot/`)

RPC handlers connecting the TUI to Claude Code via the Agent Client Protocol.

**Protocol additions (daemon ↔ TUI):**
```go
const (
    ReqCopilotChat    = "copilot_chat"    // send prompt, stream results
    ReqCopilotStatus  = "copilot_status"  // identity + stats
    ReqCopilotConfirm = "copilot_confirm" // user response to tool permission prompt
    ReqCopilotCancel  = "copilot_cancel"  // cancel in-flight prompt
)

type CopilotChatData struct {
    Message string `json:"message"`
}

// Streaming response — multiple messages pushed over subscribe stream
type CopilotStreamMsg struct {
    Type    string `json:"type"`    // "text_delta", "thought", "tool_call", "tool_update", "plan", "usage", "done", "error", "confirm"
    Content string `json:"content"` // text chunk, tool title, error message
    ToolID  string `json:"tool_id,omitempty"`   // for tool_call/tool_update correlation
    Status  string `json:"status,omitempty"`    // tool status: pending, in_progress, completed, failed
    Kind    string `json:"kind,omitempty"`      // tool kind: read, edit, execute, etc.
}

type CopilotStatusData struct {
    IdentityName string `json:"identityName"`
    EventsToday  int    `json:"eventsToday"`
    MemoryBytes  int    `json:"memoryBytes"`
}

type CopilotConfirmData struct {
    ToolID  string `json:"tool_id"`
    Allowed bool   `json:"allowed"`
}
```

**ACP Client Implementation:**

The daemon implements the `acp.Client` interface to handle callbacks from Claude Code:

```go
// internal/copilot/acp_client.go
type copilotClient struct {
    pushMsg      func(CopilotStreamMsg)    // push to TUI via subscribe stream
    permissionCh chan CopilotConfirmData    // blocks until user responds
}

func (c *copilotClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
    u := params.Update
    switch {
    case u.AgentMessageChunk != nil:
        if u.AgentMessageChunk.Content.Text != nil {
            c.pushMsg(CopilotStreamMsg{Type: "text_delta", Content: u.AgentMessageChunk.Content.Text.Text})
        }
    case u.AgentThoughtChunk != nil:
        if u.AgentThoughtChunk.Content.Text != nil {
            c.pushMsg(CopilotStreamMsg{Type: "thought", Content: u.AgentThoughtChunk.Content.Text.Text})
        }
    case u.ToolCall != nil:
        c.pushMsg(CopilotStreamMsg{
            Type:    "tool_call",
            Content: u.ToolCall.Title,
            ToolID:  string(u.ToolCall.ToolCallId),
            Status:  string(u.ToolCall.Status),
            Kind:    string(u.ToolCall.Kind),
        })
    case u.ToolCallUpdate != nil:
        msg := CopilotStreamMsg{Type: "tool_update", ToolID: string(u.ToolCallUpdate.ToolCallId)}
        if u.ToolCallUpdate.Status != nil { msg.Status = string(*u.ToolCallUpdate.Status) }
        if u.ToolCallUpdate.Title != nil { msg.Content = *u.ToolCallUpdate.Title }
        c.pushMsg(msg)
    case u.Plan != nil:
        // Serialize plan entries for TUI display
        var lines []string
        for _, e := range u.Plan.Entries {
            lines = append(lines, fmt.Sprintf("[%s] %s", e.Status, e.Content))
        }
        c.pushMsg(CopilotStreamMsg{Type: "plan", Content: strings.Join(lines, "\n")})
    case u.UsageUpdate != nil:
        c.pushMsg(CopilotStreamMsg{
            Type:    "usage",
            Content: fmt.Sprintf("%d/%d tokens", u.UsageUpdate.Used, u.UsageUpdate.Size),
        })
    }
    return nil
}

func (c *copilotClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
    // Push confirm prompt to TUI, block until user responds
    // (see MCP Tool Surface section for full implementation)
}

func (c *copilotClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
    content, err := os.ReadFile(params.Path)
    return acp.ReadTextFileResponse{Content: string(content)}, err
}

func (c *copilotClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
    return acp.WriteTextFileResponse{}, os.WriteFile(params.Path, []byte(params.Content), 0644)
}

// Terminal methods — no-ops for copilot (Claude Code uses cmc mcp-serve for execution)
func (c *copilotClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
    return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal/create")
}
// ... remaining terminal no-ops
```

**ACP Connection Lifecycle (lazy, long-lived):**

```go
// internal/copilot/connection.go
type CopilotConnection struct {
    mu        sync.Mutex
    conn      *acp.ClientSideConnection
    sessionID acp.SessionId
    cmd       *exec.Cmd
    client    *copilotClient
}

func (cc *CopilotConnection) EnsureReady(ctx context.Context, workspaceDir string) error {
    cc.mu.Lock()
    defer cc.mu.Unlock()
    if cc.conn != nil {
        select {
        case <-cc.conn.Done():
            // Connection died — need to restart
        default:
            return nil // already connected
        }
    }

    // 1. Spawn Claude Code ACP bridge
    cc.cmd = exec.CommandContext(ctx, "npx", "-y", "@zed-industries/claude-code-acp@latest")
    cc.cmd.Stderr = os.Stderr // bridge logs to stderr
    stdin, _ := cc.cmd.StdinPipe()
    stdout, _ := cc.cmd.StdoutPipe()
    cc.cmd.Start()

    // 2. Create ACP connection
    cc.conn = acp.NewClientSideConnection(cc.client, stdin, stdout)

    // 3. Initialize
    _, err := cc.conn.Initialize(ctx, acp.InitializeRequest{
        ProtocolVersion: acp.ProtocolVersionNumber,
        ClientCapabilities: acp.ClientCapabilities{
            Fs: acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true},
        },
        ClientInfo: &acp.Implementation{Name: "cmc-copilot", Version: "1.0"},
    })
    if err != nil { return err }

    // 4. Create session with cmc MCP server
    resp, err := cc.conn.NewSession(ctx, acp.NewSessionRequest{
        Cwd: workspaceDir,
        McpServers: []acp.McpServer{{
            Stdio: &acp.McpServerStdio{
                Name:    "cmc",
                Command: "cmc",
                Args:    []string{"mcp-serve"},
            },
        }},
    })
    if err != nil { return err }
    cc.sessionID = resp.SessionId

    // 5. Monitor for disconnect
    go func() {
        <-cc.conn.Done()
        cc.mu.Lock()
        cc.conn = nil
        cc.cmd = nil
        cc.mu.Unlock()
    }()

    return nil
}
```

**Handler flow (non-blocking, streaming):**
```go
func (d *Daemon) handleCopilotChat(data json.RawMessage, pushMsg func(CopilotStreamMsg)) {
    var req CopilotChatData
    json.Unmarshal(data, &req)

    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
        defer cancel()

        // Set the push callback for this interaction
        d.copilot.client.pushMsg = pushMsg
        d.copilot.client.permissionCh = make(chan CopilotConfirmData, 1)

        // Ensure ACP connection is ready (lazy init)
        if err := d.copilot.EnsureReady(ctx, d.workspace.Dir); err != nil {
            pushMsg(CopilotStreamMsg{Type: "error", Content: err.Error()})
            return
        }

        // Build context preamble with live state
        preamble := BuildContextPreamble(
            d.memory.ReadLongTermOrEmpty(),
            d.journal.RecentEventsOrEmpty(50),
            d.currentSessions(),
            d.currentDigest(),
        )

        // Send prompt via ACP — blocks until turn completes
        // SessionUpdate callback fires during this call, pushing chunks to TUI
        promptResp, err := d.copilot.conn.Prompt(ctx, acp.PromptRequest{
            SessionId: d.copilot.sessionID,
            Prompt: []acp.ContentBlock{
                acp.TextBlock(preamble),
                acp.TextBlock(req.Message),
            },
        })
        if err != nil {
            pushMsg(CopilotStreamMsg{Type: "error", Content: err.Error()})
            return
        }

        pushMsg(CopilotStreamMsg{Type: "done", Content: string(promptResp.StopReason)})
    }()
}

func (d *Daemon) handleCopilotCancel() {
    if d.copilot.conn != nil && d.copilot.sessionID != "" {
        d.copilot.conn.Cancel(context.Background(), acp.CancelNotification{
            SessionId: d.copilot.sessionID,
        })
    }
}
```

The handler is **non-blocking** — it spawns a goroutine. During the blocked `Prompt()` call, the `SessionUpdate` callback fires for each streaming chunk, pushing messages to the TUI via the subscribe connection. The TUI renders incrementally as chunks arrive.

**Multi-turn context:** Because the ACP session is long-lived (persists across prompts), Claude Code maintains conversation history. Follow-up questions like "tell me more about that session" work naturally without re-injecting full context. The context preamble ensures fresh state, while conversation history provides continuity.

**Cost control:** Model and effort can be configured via `session/set_config_option` after session creation. Max turns are governed by Claude Code's built-in limits. The `UsageUpdate` session update reports token usage in real time.

**Files to create:** `internal/copilot/acp_client.go`, `internal/copilot/connection.go`, `internal/daemon/server_copilot.go`
**Files to modify:** `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`, `internal/daemon/daemon.go` (add `copilot *copilot.CopilotConnection` + `journal` + `workspace` + `memory` fields)

---

### TUI Panel (`internal/ui/`, `internal/app/`)

Chat interface replacing the detail panel when active. Supports streaming text and tool call visibility.

No external reference — pure cmc/Bubble Tea. Follows existing patterns: backlog preview already replaces detail panel; relay model already handles text input.

**Model additions:**
```go
StateCopilot        // new app state
StateCopilotConfirm // awaiting tool permission confirmation

// On Model struct:
copilot             ui.CopilotModel
copilotMessages     []CopilotMessage   // ephemeral, per TUI session
copilotInput        ui.RelayModel      // text input (reuses existing component)
copilotStreaming     bool               // agentic loop in flight
copilotPendingTool  *CopilotToolConfirm // tool awaiting user confirmation
```

**Chat message types:**
```go
type CopilotMessage struct {
    Role    string    // "user", "copilot", "tool_call", "tool_result"
    Content string
    ToolID  string    // for tool_call/tool_result correlation
    Time    time.Time
}

type CopilotToolConfirm struct {
    ToolID   string
    ToolName string
    Args     string // human-readable args
}
```

**Streaming integration:**

The copilot chat is **streaming**, not request/response. The daemon pushes `CopilotStreamMsg` over the subscribe connection as ACP `SessionUpdate` callbacks fire during the blocked `Prompt()` call. The TUI receives these as Bubble Tea messages:

```go
type CopilotStreamChunkMsg struct {
    Msg CopilotStreamMsg // from daemon protocol
}
```

The `Update()` handler processes each message type:
- `text_delta` → append text to current copilot message (with streaming cursor `▌`)
- `thought` → append to collapsed thought block
- `tool_call` → insert new tool call entry with title, kind, and status
- `tool_update` → update existing tool call entry's status (pending → in_progress → completed/failed)
- `plan` → render plan entries with status indicators
- `usage` → update token usage display
- `confirm` → enter `StateCopilotConfirm` for permission prompt
- `done` → clear streaming cursor, show stop reason
- `error` → display error message

**View rendering:**

Messages are rendered with role-specific styling:
- **user:** dim, right-aligned or prefixed with `>`
- **copilot:** normal text, streaming cursor `▌` while `copilotStreaming`
- **tool_call:** compact, dimmed: `⚙ sessions_list()` or `⚙ backlog_create(cwd="/project")`
- **tool_result:** collapsed by default, expandable
- **confirm prompt:** highlighted bar: `⚠ send_message("session-123", "run tests") — allow? [y/n]`

**View integration** (follows backlog preview pattern):
```go
} else if m.state == StateCopilot || m.state == StateCopilotConfirm {
    detailContent = ui.RenderCopilotChat(m.copilotMessages, detailWidth, detailH,
        m.copilot.Scroll(), m.copilotStreaming, m.copilotPendingTool)
```

**State handlers:**

`StateCopilot`:
- `Esc` → return to `StateNormal` (conversation preserved)
- `Enter` → submit message, set `copilotStreaming = true`, dispatch RPC
- `ctrl+c` → cancel in-flight agentic loop
- `ctrl+d / ctrl+u` → scroll conversation
- Other keys → forward to text input

`StateCopilotConfirm`:
- `y` → send `ReqCopilotConfirm{Allowed: true}`, return to `StateCopilot`
- `n` → send `ReqCopilotConfirm{Allowed: false}`, return to `StateCopilot`
- `Esc` → deny (same as `n`)

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

### ACP Fallback: Direct CLI Invocation

If the ACP bridge (`@zed-industries/claude-code-acp`) proves unreliable or `npx` is unavailable, the fallback is:
1. Use the same `cmc mcp-serve` subcommand (already the primary tool surface)
2. Invoke `claude -p --mcp-config '{"mcpServers":{"cmc":{"command":"cmc","args":["mcp-serve"]}}}' --allowedTools "mcp__cmc__*"` directly
3. Claude CLI handles the agentic loop internally
4. Trade-off: no streaming to TUI during tool calls (output arrives all at once), no programmatic permission callback (relies on Claude CLI's built-in permission model), no multi-turn context

### Multi-turn Conversation Context

ACP sessions are inherently multi-turn — the Claude Code subprocess maintains conversation history across prompts. V1 already benefits from this: follow-ups like "tell me more about that session" work because the ACP session persists. Future enhancement: expose session management in the TUI (clear conversation, fork session, resume previous session via `session/load`).

### Autonomous Triggers (deferred)

Currently off the table per Huy's directive. When enabled, the copilot would watch the event stream and fire on patterns:
- Session stops with `max_tokens` → note context limit
- CompactCount 3+ → archive key context
- File overlap detected → warn about collision
- All sessions idle → generate end-of-work digest
These would use the same MCP tools but triggered by event rules rather than user input.

---

## Team Formation for Parallel Execution

```
Phase 1 (parallel):   [Team A: Events]  [Team B: Workspace+Memory+Search]  [Team D: TUI scaffold]
                              │                    │                                │
Phase 2 (parallel):           │            [Team C: MCP Tools + SDK + Prompt]       │
                              │                    │                                │
Phase 3 (integration):        └───────┬────────────┘                               │
                                      │                                            │
                              [Team E: Daemon wiring + streaming protocol]          │
                                      │                                            │
Phase 4 (integration):                └────────────────────────────────────────────┘
                                      [Team D: wire real streaming client]
```

### Team A: Event Journal
**Files to create:** `internal/copilot/events.go`, `internal/copilot/journal.go`
**Files to modify:** `internal/daemon/daemon.go`, `internal/daemon/daemon_poll.go`, `internal/daemon/daemon_synthesis.go`
**Boundary:** Exports `NewJournal()`, `Append()`, `RecentEvents()`, `ReadForSession()` for Team C/E.

### Team B: Workspace, Memory & Search
**Files to create:** `internal/copilot/workspace.go`, `internal/copilot/memory.go`, `internal/copilot/search/temporal_decay.go`, `internal/copilot/search/mmr.go`, `internal/copilot/search/keywords.go`, `internal/copilot/search/chunk.go`
**Port from goclaw:** `memory/temporal_decay.go` → adapt types. `memory/mmr.go` → adapt types. `memory/hybrid.go` → cherry-pick `ExtractKeywords`/`NormalizeScores`/stop words. `memory/vector.go` → cherry-pick `ChunkText` + add 80-token overlap per OpenClaw spec.
**Files to modify:** None (pure library).
**Boundary:** Exports `NewWorkspace()`, `EnsureInitialized()`, `LoadBootstrapFiles()`, `NewMemory()`, `ReadLongTerm()`, `AppendLongTerm()`, `Search()` for Team C/E.
**OpenClaw compatibility responsibility:** This team owns file naming (UPPERCASE.md), directory layout (`memory/` not `daily/`), and `workspace-state.json` creation.

### Team C: ACP Client, MCP Server & Prompt Builder
**Files to create:** `internal/copilot/acp_client.go`, `internal/copilot/connection.go`, `internal/copilot/prompt.go`, `internal/copilot/claudemd.go`, `cmd/cmc/mcp_serve.go`, `internal/copilot/mcp_tools.go`
**New dependency:** `github.com/coder/acp-go-sdk` (ACP client), a pure-Go MCP server library (e.g., `github.com/mark3labs/mcp-go` or hand-rolled stdio JSON-RPC)
**Runtime dependency:** `npx` + `@zed-industries/claude-code-acp` (Claude Code ACP bridge, spawned as subprocess)
**Depends on:** Team B (memory/workspace types for MCP tool handlers and CLAUDE.md generation).
**Scope:** Implement `copilotClient` (the `acp.Client` interface — handles `SessionUpdate`, `RequestPermission`, file ops), implement `CopilotConnection` (lazy ACP lifecycle management), implement `BuildContextPreamble()`, implement `cmc mcp-serve` subcommand (stdio MCP server exposing all cmc tools), generate `CLAUDE.md` from bootstrap files.
**Boundary:** Exports `CopilotConnection` with `EnsureReady()` for Team E, exports `cmc mcp-serve` as independently runnable subcommand.
**Critical task:** Verify `@zed-industries/claude-code-acp` bridge works with the Go ACP SDK. Test: spawn bridge, initialize connection, create session, send prompt, verify `SessionUpdate` callbacks fire. If the npm bridge is problematic, check if `claude --acp` native flag exists.

### Team D: TUI Panel
**Files to create:** `internal/ui/copilot.go`, `internal/ui/copilot_view.go`, `internal/app/update_copilot.go`
**Files to modify:** `internal/app/model.go`, `internal/app/messages.go`, `internal/app/keymap.go`, `internal/app/update.go`, `internal/app/update_normal.go`, `internal/app/commands.go`, `internal/app/view.go`, `internal/app/view_footer.go`
**Phase 1:** Build with mock streaming — simulate `CopilotStreamMsg` sequence (text chunks + tool calls) via `time.Ticker`. This tests the full rendering pipeline without the SDK.
**Phase 4:** Replace mock with real subscribe stream messages from daemon.
**New vs old plan:** Must handle `CopilotStreamChunkMsg` incrementally (append text to current message, insert tool call entries). Must implement `StateCopilotConfirm` for tool permission prompts (`y`/`n` keys).

### Team E: Daemon Wiring & Streaming Protocol
**Files to create:** `internal/daemon/server_copilot.go`
**Files to modify:** `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`, `internal/daemon/daemon.go`
**Depends on:** Team A (journal), Team B (workspace/memory), Team C (ACP client + connection).
**Scope:** Wire `handleCopilotChat` into the daemon server dispatch, implement streaming push of `CopilotStreamMsg` over the subscribe connection, implement `handleCopilotConfirm` to unblock ACP `RequestPermission` callback, implement `handleCopilotCancel` to send ACP `Cancel` notification.
**Boundary:** The client exposes `CopilotChat(message)` (initiates stream), `CopilotConfirm(toolID, allowed)`, and `CopilotCancel()` for Team D.

---

## OpenClaw Compatibility Checklist

Validated against OpenClaw source (`workspace.ts`, `internal.ts`, `temporal-decay.ts`):

| Requirement | Status | Notes |
|-------------|--------|-------|
| `.openclaw/workspace-state.json` exists | Planned | Created by `EnsureInitialized()` with `onboardingCompletedAt` set |
| Bootstrap files are UPPERCASE.md | Planned | `SOUL.md`, `IDENTITY.md`, `USER.md`, `AGENTS.md`, `TOOLS.md`, `HEARTBEAT.md` |
| `MEMORY.md` at root (uppercase preferred) | Planned | Uppercase; OpenClaw has `memory.md` lowercase fallback but we use canonical |
| Daily logs at `memory/YYYY-MM-DD.md` | Planned | Not `daily/` — matches OpenClaw's `isEvergreenMemoryPath()` regex |
| Extra dirs ignored by OpenClaw | Verified | `events/` dir is safe — OpenClaw only scans recognized filenames |
| Missing bootstrap files non-fatal | Verified | OpenClaw marks missing files as `missing: true`, doesn't fail |
| Frontmatter stripped | Verified | If we add YAML frontmatter to any file, OpenClaw strips it automatically |
| 2MB per-file limit | Verified | Bootstrap files capped at `MAX_WORKSPACE_BOOTSTRAP_FILE_BYTES` |
| Can point OpenClaw to workspace | Verified | `openclaw setup --workspace ~/.cache/cmc/copilot/` or config `agents.defaults.workspace` |
| No hooks/skills required | Verified | Zero hooks and zero skills is valid |
| Workspace not "brand new" detection | Planned | Presence of SOUL.md + AGENTS.md etc. prevents template re-seeding |

### Potential Conflicts

**1. Concurrent writes to `MEMORY.md`:** If OpenClaw and cmc's copilot both run against this workspace, both could append to `MEMORY.md` simultaneously. Mitigation: file-level advisory locks on write, or accept last-writer-wins (append-only makes this safe — worst case is interleaved lines, not data loss).

**2. Daily log format divergence:** cmc's copilot writes structured event summaries to `memory/YYYY-MM-DD.md`. OpenClaw's agent writes free-form session notes. If both write to the same day's file, the content will be mixed. Mitigation: use clear section headers (e.g., `## cmc Events` vs OpenClaw's natural prose) so both are readable.

**3. `SOUL.md` / `USER.md` ownership:** If Huy edits these files via OpenClaw (the agent can update `USER.md`), cmc should reload them on each interaction (already planned — `LoadBootstrapFiles()` reads fresh every time). No caching.

**4. OpenClaw memory search indexing `events/`:** OpenClaw only indexes `memory/` and root `MEMORY.md`. The `events/` directory contains NDJSON (not Markdown), so even if OpenClaw somehow scanned it, the `.ndjson` extension wouldn't match `.md` discovery. No conflict.

**5. `HEARTBEAT.md` activation:** If Huy adds heartbeat tasks while using the workspace via OpenClaw, those tasks run in OpenClaw's context (not cmc). cmc ignores `HEARTBEAT.md` content entirely — it's loaded into the prompt for completeness but the copilot has no autonomous execution. No conflict.

---

## Verification

1. `make build` succeeds with no errors
2. Start daemon (`bin/cmc daemon`), verify `~/.cache/cmc/copilot/events/{today}.ndjson` populates as sessions run
3. Verify `~/.cache/cmc/copilot/` auto-created as valid OpenClaw workspace on first copilot interaction:
   - `.openclaw/workspace-state.json` exists with `onboardingCompletedAt`
   - `CLAUDE.md` exists (derived from bootstrap files)
   - `SOUL.md`, `IDENTITY.md`, `USER.md`, `AGENTS.md`, `TOOLS.md`, `HEARTBEAT.md` exist (uppercase)
   - `MEMORY.md` exists at root
   - `memory/` directory exists for daily logs
4. **MCP server standalone test:** `echo '{"jsonrpc":"2.0","method":"tools/list","id":1}' | cmc mcp-serve` returns tool list JSON
5. Open TUI, press `@`, verify copilot panel replaces detail with input line + footer hints
6. **ACP connection:** First prompt triggers lazy ACP init (spawns Claude Code bridge, creates session). Verify bridge process running.
7. **Read-only query:** Type "What sessions are active?" + Enter → streaming text appears with actual session data (copilot calls `sessions_list` tool via `cmc mcp-serve` internally)
8. **Streaming visible:** Text chunks appear incrementally as ACP `SessionUpdate` callbacks fire
9. **Tool use visible:** Observe `⚙ sessions_list()` indicator in chat during the query, with status transitions (pending → in_progress → completed)
10. **Memory tool:** Type "Remember that the auth module needs refactoring" → copilot calls `memory_append` tool → verify `MEMORY.md` updated
11. **Backlog grooming:** Type "List my backlogs" → copilot calls `backlog_list` tool for each project → returns formatted list
12. **Action confirmation:** Type "Send 'run the tests' to session X" → ACP `session/request_permission` triggers `⚠ send_message(...) — allow? [y/n]` prompt → press `y` → message sent
13. **Action denial:** Same as above, press `n` → copilot acknowledges denial gracefully
14. **Multi-turn:** Ask a follow-up question about a previous answer → copilot responds with context from the same ACP session
15. **Usage tracking:** Token usage updates displayed during interaction (via ACP `UsageUpdate`)
16. Press Esc → normal view, conversation history preserved
17. Press `@` again → previous conversation still visible
18. Press `;` → command palette → "Copilot" entry present and functional
19. **ACP resilience:** Kill the bridge process, send another prompt → daemon detects disconnect, re-initializes ACP connection
20. **OpenClaw compatibility test:** Run `openclaw setup --workspace ~/.cache/cmc/copilot/` — verify OpenClaw recognizes it as existing workspace (no bootstrap ritual triggered), loads SOUL.md/IDENTITY.md/etc., and can read MEMORY.md + memory/*.md
