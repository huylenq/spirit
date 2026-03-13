# Copilot Brainstorm Transcript

*Date: 2026-03-12*

---

## Starting Point: OpenClaw Research

Huy had just finished writing `researches/openclaw.md` — a detailed research document on OpenClaw (formerly Clawdbot/Moltbot), the open-source autonomous AI agent by Peter Steinberger that became the fastest-growing GitHub repo in history (250K+ stars, surpassing React in ~4 months).

Huy's opening question:

> "What is the capabilities of OpenClaw would be a great fit to enhance our synthesis / synthesizer with meta ability, cross-session awareness and orchestration?"

---

## Phase 1: Mapping OpenClaw onto the Synthesizer

### First: Understanding What cmc's Synthesizer Actually Does

Before mapping OpenClaw onto it, we needed to audit what the synthesizer currently is. Reading through `synthesize.go`, `digest.go`, `daemon_synthesis.go`, `synthstats.go`, and `server_synthesis.go` revealed a four-layer system:

| Layer | What it does |
|-------|-------------|
| **Session Summary** (`synthesize.go`) | Takes user messages from a single session transcript, sends them to Haiku via `newLightweightClaude()`, gets back structured JSON: `{objective, status, problem_type, headline}`. Cached to disk as `.summary` files. |
| **Workspace Digest** (`digest.go`) | Meta-summary across all sessions. Collects headlines from all session summaries, sends to Haiku for a 2-3 sentence overall workspace activity summary. |
| **Auto-synth** (`daemon_synthesis.go`) | Automatically triggers synthesis when a session transitions from agent-turn to user-turn (idle). Debounced at 30s. After synthesis, triggers digest regeneration (debounced at 60s). |
| **Stats** (`synthstats.go`) | Tracks synthesis calls (word counts, call counts) over rolling time windows (day/week/month). NDJSON usage log with background pruning of entries older than 90 days. |

The diagnosis: **purely reactive, stateless across restarts, and has zero historical recall.** Each synthesis is a one-shot prompt — no memory of past syntheses, no cross-session reasoning, no orchestration. cmc already generates all the data for richer intelligence; it just throws it away after display.

### OpenClaw Capabilities Worth Stealing

Six capabilities mapped directly:

**1. Daily Logs — the biggest missing piece.**
OpenClaw writes `memory/YYYY-MM-DD.md` as append-only running context. cmc could do the same automatically — after each synthesis, append `{timestamp, sessionID, headline, status, problem_type}` to today's daily log. On daemon start, load today + yesterday for continuity. Enables: "what was I working on this morning?" and persistent context across daemon restarts. cmc already generates all the data — it just throws it away after display. This is pure waste. Maybe 50 lines of Go.

**2. RAG-lite Search over Session History (hybrid vector + BM25 on SQLite).**
OpenClaw's `memory_search` + `memory_get` over indexed Markdown is exactly what cmc lacks for historical recall. The formula `0.7×vector + 0.3×BM25` with union ranking (not intersection — a chunk scoring high on either dimension gets included) is solid. This turns cmc from "what's happening now" into "what's happened ever."

**3. Auto-preservation Before Context Compaction.**
cmc already tracks `CompactCount` per session. OpenClaw's trick: trigger a silent memory-write turn before the context gets compacted. cmc could: when a `PreCompact` hook fires, auto-synthesize that session immediately (not waiting for idle) and persist to the daily log. This prevents the "session got compacted and lost nuance" problem.

**4. Cross-Session Awareness via Digest Enhancement.**
Current digest is just headline aggregation. OpenClaw's model suggests a richer cross-session layer:
- **Overlap detection → conflict narration**: cmc already has `HasOverlap`. Instead of just flagging it, synthesize *what* the conflict is: "Session A is refactoring auth middleware while Session B is adding new auth routes — these will collide."
- **Dependency inference**: "Session A created a new API endpoint that Session B's frontend work will need."
- **Workspace narrative**: Not just "what's happening" but "what's the story arc" across sessions.
This means the digest prompt needs to receive not just headlines but also `DiffStats` (which files each session touches) and the overlap graph.

**5. Long-term Memory (MEMORY.md equivalent).**
A curated file of persistent project facts that survive across daemon restarts. Things like "This project uses conventional commits", "Auth module was last refactored 2 weeks ago across 3 sessions", "Session X was abandoned because approach was wrong — don't retry." The synthesizer could write to this automatically when it detects significant events.

**6. Heartbeat / Proactive Behaviors (HEARTBEAT.md).**
OpenClaw's scheduled behavior config could translate to periodic memory consolidation, stale session detection, end-of-day digest notifications.

### What I'd Skip

- **Personality system (SOUL.md/IDENTITY.md)** — cmc is a monitoring tool, not a conversational agent. Not worth the complexity.
- **Messaging platform connectors** — cmc's UI is the TUI itself. Adding WhatsApp/Slack as UI frontends is a different product.
- **Plugin architecture** — cmc's daemon + hooks architecture already covers this.

### Suggested Priority Order

1. **Daily logs** — trivially easy, highest ROI. Just append structured NDJSON after each synthesis.
2. **Pre-compaction auto-synth** — cmc already has the hook. Wire `PreCompact` → immediate synthesis + daily log write.
3. **Enhanced digest with overlap narration** — enrich the existing digest prompt with file-level cross-session data.
4. **SQLite search index** — medium effort, but transforms cmc into something with genuine memory.
5. **Long-term MEMORY.md** — needs the search index first to be useful.

---

## Phase 2: The Pivot — "A Copilot with Soul"

Huy's response was a complete redirection:

> "I diverge, I want to escape the boundary of synthesizer (in fact, let's keep the synthesizer simple, directed, as it is, I changed my mind on this). What I wanted is a copilot with soul, with identity, learning and adapt with me. He is an alive individual, long lived, surpass any project or chat session scope. In case you need more exact consultation on what exactly OpenClaw, what is in its DNA maked me interested, go to ~/src/openclaw and find out."

This shifted the entire conversation from "improve the synthesizer" to "build a persistent AI companion." The synthesizer stays as-is — simple, directed. The new thing is something else entirely.

### Deep Dive into OpenClaw's Soul Architecture

Explored the actual OpenClaw source at `~/src/openclaw`. This wasn't just reading docs — we went through the actual TypeScript implementation to understand what makes it feel "alive." Found 8 layers of persistence that compound into the illusion of continuity:

#### Layer 1: Identity Bootstrapping (First-Run Ritual)

On first run, OpenClaw performs a bootstrap ritual — one-question-at-a-time interactive Q&A that seeds the workspace:

```markdown
# IDENTITY.md - Who Am I?
- **Name:** (pick something you like)
- **Creature:** (AI? robot? familiar? ghost in the machine?)
- **Vibe:** (sharp? warm? chaotic? calm?)
- **Emoji:** (your signature)
- **Avatar:** (workspace-relative path, URL, or data URI)
```

Seeds `AGENTS.md`, `SOUL.md`, `IDENTITY.md`, `USER.md` as templates, runs the Q&A, fills in actual values, then deletes `BOOTSTRAP.md` (one-time-only). Files live at `~/.openclaw/workspace/`.

#### Layer 2: Soul & Character (Session Load Time)

Every session, these files are loaded into bootstrap context:

| File | What It Is | Loaded When |
|------|-----------|------------|
| `SOUL.md` | Persona, tone, boundaries | Every session (read-only guidance) |
| `USER.md` | Who the human is, pronouns, timezone | Every session |
| `IDENTITY.md` | Name, emoji, vibe, avatar | Created/updated during bootstrap |
| `AGENTS.md` | Operating instructions, memory rules | Every session |
| `TOOLS.md` | Notes about local tools | Every session |

All just Markdown. Content gets injected as context at session start. Critical design choice: files are truncated per session — default max per file: 20,000 chars, default total budget: 150,000 chars. Large files get head (70%) + tail (20%), marked with `[...truncated...]`.

#### Layer 3: Memory System (Continuity Across Sessions)

OpenClaw's memory is plain Markdown, disk-backed. Two layers:

**Daily Logs** (`memory/YYYY-MM-DD.md`): Append-only per day. Automatically loaded: today + yesterday at session start. Can grow very large; OpenClaw applies temporal decay to older entries in search.

**Curated Long-Term Memory** (`MEMORY.md`): Optional. Only loaded in main/private sessions (never in group/multi-agent contexts). Source of truth for durable facts, decisions, preferences.

#### Layer 4: Memory Search Engine (RAG-lite)

Backend: SQLite. Indexing: Markdown chunks (~400 token target, 80-token overlap). Two tools for recall: `memory_search` (semantic) + `memory_get` (file-specific read).

Hybrid search combines:
- **Vector similarity (70% weight)** — semantic match: "Mac Studio gateway" ≈ "machine running gateway"
- **BM25 keyword (30% weight)** — exact tokens: IDs, code symbols, error strings
- **MMR re-ranking** — diversity: avoid near-duplicate results
- **Temporal decay** — recency boost: 30-day half-life

Scoring uses **union** (not intersection) — a chunk scoring high on either dimension gets included.

#### Layer 5: Auto-Preservation (Pre-Compaction Memory Flush)

This is the living heartbeat of continuity. As session context grows and approaches the compaction boundary:

1. **Threshold**: When token count reaches `(contextWindow - reserveTokens - softThresholdTokens)`
2. **Silent turn**: Background agentic turn to write durable memory
3. **Target**: Appends to `memory/YYYY-MM-DD.md`
4. **Reply**: Agent responds `NO_REPLY` (silent, user never sees it)
5. **Tracked**: Marked in session metadata to prevent duplicate flushes per compaction cycle

Safety hints: only write to `memory/YYYY-MM-DD.md` (specific date format), append-only, treat bootstrap files as read-only, don't create variant filenames.

#### Layer 6: Compaction

When sessions grow too large, OpenClaw summarizes context intelligently: splits messages by token share (default 2 parts), generates partial summaries via LLM, merges with identifier preservation. Critical instruction: "Preserve all opaque identifiers exactly as written: UUIDs, hashes, IDs, tokens, API keys, hostnames, IPs, ports, URLs, and file names." This prevents hallucination of made-up IDs when compacting.

#### Layer 7: Proactive Behavior (Heartbeats & Boot Hooks)

`HEARTBEAT.md`: optional periodic checklist, empty by default (opt-in). `BOOT.md`: runs once on gateway restart. Four bundled hooks:

| Hook | Event | What It Does |
|------|-------|------------|
| `session-memory` | `command:new`, `command:reset` | Saves previous session to `memory/YYYY-MM-DD-slug.md` |
| `command-logger` | All commands | Logs to commands.log |
| `boot-md` | `gateway:startup` | Runs BOOT.md on restart |
| `bootstrap-extra-files` | `agent:bootstrap` | Injects extra files during first-run |

The session-memory hook is particularly clever: on `/new` or `/reset`, captures last N messages, uses LLM to generate a descriptive slug (e.g., `2026-03-12-vendor-pitch.md`), saves to memory. Context isn't lost when you reset.

#### Layer 8: Session Startup (Ordered Load Sequence)

Bootstrap files loaded in order: AGENTS.md → SOUL.md → USER.md → IDENTITY.md → TOOLS.md → HEARTBEAT.md → today's daily log → yesterday's daily log → MEMORY.md. Then hooks can inject additional files. Private sessions get MEMORY.md; group sessions skip it. Files truncated to token budget. Loaded fresh every session — edit SOUL.md and the next session reads the new version.

### The Key Insight

> Identity is just Markdown files loaded at boot. There's no magic runtime. The "aliveness" comes from the *accumulation* — the agent reads its own past every time it wakes up, so it *becomes* its history.

### What Huy Already Has (and Didn't Realize)

Claude Code's `~/.claude/` + auto-memory system is actually layer 0 of this. Huy already has:
- `CLAUDE.md` → proto-SOUL (instructions, personality, values)
- `memory/MEMORY.md` → proto long-term memory
- `memory/*.md` → user/feedback/project memories

But it's missing the layers that create **felt continuity**: daily logs, self-updating identity, temporal search, proactive preservation, and the bootstrap ritual that makes the agent *read its own biography* at startup.

### The Real Question Posed

> Is Huy asking me to **design this as a new project**? Or to **build this as a layer on top of Claude Code's existing memory system** (which I'm already running inside)?
>
> Because the second option is actually tractable right now — most of the infrastructure exists. The gap is:
> 1. **No daily log** — sessions accumulate memories but there's no "what happened today" narrative
> 2. **No identity file** — Claude Code has instructions but the agent has no self-concept
> 3. **No temporal search** — memories exist but can't be queried by meaning
> 4. **No auto-preservation** — when context compacts, whatever wasn't saved is gone
> 5. **No session archival** — when a conversation ends, it's just... over

---

## Phase 3: Scoping — A cmc Feature, Not Claude Code Extension

Huy's answer clarified the scope:

> "To your real question: a new feature in this mission control project. Claude Code existing memory system is project-focused, and I'm not sure how it scales (does it use RAG or something, does it search semantically? etc.), it's probably not what I'm aiming for."

So: a new feature in cmc. Not a Claude Code extension, not a standalone project. The copilot lives inside mission control.

Huy also asked for more reconnaissance:

> "Continue on our current reconnaissance, how does OpenClaw manage skills, MCPs, tool calls, etc.?"

### OpenClaw's Tool Architecture — Not What You'd Expect

**OpenClaw does NOT use MCP.** Its tool system is entirely its own.

#### Three-Layer Separation

```
SKILL.md (Markdown)     →  "what's available" (metadata, docs, requirements)
Tool (TypeScript code)  →  "how it runs" (actual implementation)
Policy (config.json)    →  "who can use what" (allow/deny, profiles, sandboxing)
```

#### Skills (~54 bundled)

Each skill is a directory with a `SKILL.md` containing YAML frontmatter: name, description, emoji, required binaries/env vars, installation methods (brew/node/go/uv/download), platform restrictions, invocation policy. Skills are **not code** — they're declarations. Discovery is hierarchical with override precedence: `bundled < managed < personal < project < workspace`. Token-budgeted: max 150 skills, 30KB in prompt, binary-search truncation if over budget.

#### Tools (native TypeScript)

Four profiles: `minimal` (read-only), `coding` (exec, read, write, web), `messaging` (sessions, channel tools), `full` (everything). Policy resolution: `agent-specific > provider-specific > global`, deny always wins. Dangerous tools (`exec`, `sessions_spawn`, `gateway`) require explicit approval in ACP. Owner-only gates prevent non-owner access.

#### Plugins

Dynamically loaded from `extensions/discord/`, `extensions/slack/`, etc. Plugin slots are singleton: one memory backend, one context engine. Plugins hook into lifecycle events (before_prompt, before_tool_call).

### The Honest Assessment

> Here's the thing, Huy — **OpenClaw's skill/tool/MCP architecture is largely irrelevant to what you described wanting.**
>
> The soul, the identity, the learning, the cross-session awareness — that's all in the **memory + bootstrap + preservation layers**, not in the tool system. OpenClaw's tool architecture solves a different problem: "how do I let an always-on agent control WhatsApp, email, calendars, and smart home devices safely." That's multi-channel I/O orchestration.
>
> What Huy described — a copilot with soul that transcends projects and sessions — the relevant OpenClaw DNA is:
> 1. **Bootstrap ritual** (identity files loaded fresh every session)
> 2. **Daily logs** (append-only diary, today+yesterday at boot)
> 3. **Memory search** (SQLite + vector + BM25 + temporal decay)
> 4. **Pre-compaction flush** (silent turn to save thoughts before context dies)
> 5. **Session archival hook** (LLM-slugged archive on reset/new)
>
> These five mechanisms are what create the "alive" feeling. The tool system is just plumbing.

---

## Phase 4: How to Integrate the Copilot into Mission Control

Huy's next question:

> "Let's morph the idea a bit more, how to tightly incorporate, make our copilot being a tightly incorporated part into mission control, how he would aware, be notified about what's going on in mission control?"

### cmc is Already the Nervous System

The key realization: cmc is already an omniscient observer. The daemon already sees everything. The copilot doesn't need new infrastructure for awareness — it needs **a subscriber seat at the existing table.**

#### What the Daemon Already Knows

**Real-time events (via 9 hook types, no polling needed):**
Every user prompt submitted (with content), every tool call (pre and post, with tool name + input), every agent stop (with reason), every permission prompt popup, every context compaction, every git commit, every skill invocation.

**Derived state (computed each 1s poll cycle):**
Which files each session is editing + line counts, file overlap between sessions, worktree isolation topology, git branches per session, session age and idle duration, synthesis headlines + workspace digest.

**Lifecycle events (state transitions in `patchSession`):**
Session born / session died, `agent-turn → user-turn` (just finished working), `user-turn → agent-turn` (just got a new task), became waiting (permission prompt), compaction happened (context shrunk), commit detected (meaningful checkpoint).

#### Three Integration Points Proposed

**1. Event Stream (passive awareness):**
Every `patchSession` call that bumps `d.version` also feeds the copilot. But unlike the TUI (which only sees the current session list), the copilot also receives the delta — what changed and why.

**2. Trigger Rules (proactive reactions):**
The copilot watches the event stream and fires when patterns match:

| Signal | Copilot could... |
|--------|-----------------|
| Session stops with `max_tokens` | Note "session X hit context limit — consider splitting" |
| CompactCount hits 3+ | Archive key context before it decays further |
| File overlap detected | Warn: "Session A and B are both touching auth.go" |
| Session idle > 30min with uncommitted changes | Nudge: "Session X has uncommitted work on branch Y" |
| All sessions idle | Generate end-of-work digest |

**3. Query Interface (TUI asks the copilot):**
A new panel or command to talk to the copilot:
- "What's the status across all my sessions?"
- "What was I working on yesterday?"
- "Which session should I check on first?"
- "Summarize what happened today"

#### The Data Flow

```
Claude Code                      Daemon                        TUI/Copilot
  (binary)                    (long-lived)                   (client)
     |                            |                              |
     |--- Hook Events (JSON) --→  |                              |
     |  (UserPromptSubmit,        |  [1] handleHook()            |
     |   PostToolUse,             |      ├─ parse hookInput      |
     |   Stop, etc.)              |      ├─ write status files    |
     |                            |      └─ nudgeDaemon()        |
     |                            |              |                |
     |                            |  [2] patchSession()          |
     |                            |      ├─ update in-memory     |
     |                            |      ├─ check dedup          |
     |                            |      └─ notifySubscribers()  |
     |                            |                              |
     |                            |→→→ Subscribe stream → SessionsRefreshedMsg
     |                            |                              |
     |                            |→→→ Copilot Event Journal     |
     |                            |    (append to NDJSON)        |
```

### Huy's Course Correction

> "Your proposal is too aggressive in autonomousity. Focus on full environment awareness and interaction with user (me), actions should be triggered from users, let's leave autonomous actions off the table for now."

This was important. It eliminated trigger rules and proactive behaviors entirely. The copilot became: **passive observer + user-triggered query interface.** No autonomous nudges, no "hey you should check on session X." The copilot watches everything, remembers everything, but only speaks when spoken to.

Huy also asked to include a Claude team formation structure for parallel execution in the plan.

---

## Phase 5: Architecture Design

### Exploring cmc's TUI Architecture

Before designing the copilot panel, we needed to understand how the TUI is built:

**State machine:** 15 app states (`StateNormal`, `StateSearching`, `StatePalette`, `StatePromptRelay`, etc.). Each state owns its Update and View logic.

**Panel system:** Sidebar (session list, ~30% width) + Detail (session preview/diffs/hooks, ~70% width) + Footer (context-sensitive hints) + Minimap (pane visualization) + Usage bar (token animation). Overlays for help, command palette, preferences, macros.

**Key pattern discovered:** The detail panel already has a precedent for being replaced — when a backlog item is selected, the detail area shows the backlog preview instead of the session preview. The copilot can follow the same pattern.

**Relay model:** Text input for sending messages to sessions (`StatePromptRelay` with `ui.RelayModel`). The copilot input can reuse this same component.

### Exploring the Daemon Protocol

The daemon-TUI protocol is newline-delimited JSON over Unix socket. Two connections: subscribe stream (push) and RPC (request/response). ~26 request types, but only 4 response types: `pong`, `sessions`, `result`, `error`.

Adding new endpoints follows a clear 5-step pattern:
1. Define types in `protocol.go`
2. Add dispatch case in `server.go`
3. Implement handler in `server_*.go`
4. Add client method in `client.go`
5. Call from TUI

### The Four-Layer Design

**Layer 1: Event Journal** — NDJSON append-only log of all daemon events. Taps into `patchSession()`, `poll()`, and synthesis hooks. Storage: `~/.cache/cmc/copilot/events/YYYY-MM-DD.ndjson`. The copilot's sensory input.

**Layer 2: Identity & Memory** — Markdown-based persistence. `identity.md` (persona), `memory.md` (long-term facts), `daily/YYYY-MM-DD.md` (narrative logs). No SQLite — zero CGo dependencies in the codebase, simple substring search is sufficient for v1.

**Layer 3: Daemon Endpoints + LLM** — RPC handlers (`copilot_chat`, `copilot_status`) that build a system prompt from identity + memory + recent events + live sessions + digest, then invoke Sonnet via `newCopilotClaude()` (reusing the existing `claude` CLI pattern but with `--model sonnet --effort medium` instead of haiku/low).

**Layer 4: TUI Panel** — New `StateCopilot` state. Chat interface replaces the detail panel when active (toggled by `@` key or `gc` chord). Scrollable conversation history + text input at bottom + "thinking..." spinner during LLM calls. Conversation history is ephemeral (per TUI session).

### Key Design Decisions

**No SQLite.** The codebase has zero CGo dependencies and no database libraries. Adding SQLite would pull in CGo (or a pure-Go SQLite which is enormous), complicate cross-compilation, and introduce a new failure mode. Simple substring search over markdown files is sufficient for v1. If FTS becomes a bottleneck later, it can be added then.

**No streaming.** The existing `newLightweightClaude()` pattern collects the full output before returning. For a chat experience, this means the user stares at a "thinking..." spinner. Acceptable for v1; streaming is a follow-up.

**Copilot replaces detail panel.** Not an overlay, not a separate window. Follows the same pattern as backlog preview replacing the detail area. Press `@` to enter, `Esc` to return to normal view.

**Conversation is ephemeral.** Lives in TUI memory, not persisted. Each TUI session starts fresh. This avoids complex multi-turn state management. Each question is a one-shot prompt — the LLM gets full context (identity + memory + events + sessions) every time.

**Memory via `[REMEMBER: ...]` tags.** When the user says "remember X", the LLM includes `[REMEMBER: X]` in its response. The daemon handler extracts these tags and auto-appends to `memory.md`. No separate "remember" RPC needed. If the LLM doesn't follow the format, the memory write silently fails — acceptable.

**Sonnet, not Haiku.** The synthesizer uses Haiku for speed/cost. The copilot needs richer reasoning — understanding cross-session context, interpreting event timelines, formulating useful answers. Sonnet with `--effort medium` balances quality and latency.

### Team Formation for Parallel Execution

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

**Team A (Event Journal):** Pure library code — event types, NDJSON writer/reader. Then wires emit calls into `daemon.go`, `daemon_poll.go`, `daemon_synthesis.go`. No dependency on other teams.

**Team B (Identity & Memory):** Pure library code — identity loading, memory read/write/search. No daemon changes. No dependency on other teams.

**Team C (Daemon + LLM):** Creates the RPC handlers, prompt builder, LLM helper. Depends on A (journal reading) and B (identity/memory loading). The integration hub.

**Team D (TUI Panel):** Creates the chat model, view renderer, state handler, keybindings, command palette entry. Can start immediately with mock client methods returning hardcoded responses. After Team C completes, replaces stubs with real client calls.

---

## Open Questions (not yet resolved)

- Should the copilot have multi-turn conversation state in the LLM, or is each question truly one-shot? (One-shot is simpler but loses conversational flow; multi-turn requires persisting conversation context somewhere.)
- Should daily narrative logs be auto-generated (LLM summarizes events at end of day) or only written when user asks? (Auto-generation would be a proactive behavior, which Huy said to leave off the table for now.)
- What happens when `identity.md` is edited by the user — does the copilot acknowledge the change? Should it have a "who am I" self-awareness command?
- Should the copilot be aware of sessions across ALL projects (global), or scoped to the current workspace? (The daemon currently sees everything across all tmux panes regardless of project.)
- Memory search: is substring matching really sufficient for v1, or will Huy want semantic search quickly? (Depends on how much memory accumulates and how Huy uses recall.)
- LLM latency: `newCopilotClaude()` runs the `claude` CLI as a subprocess. Cold start could be 3-5 seconds. Is the "thinking..." spinner sufficient UX, or does streaming need to come sooner?
- System prompt size: with 50 events + full memory + session list, the prompt could get large. Need caps — memory at ~2000 words, events at ~50 — to keep input tokens reasonable.
