# OpenClaw

*Researched: 2026-03-12*

**Website:** https://openclaw.ai | **Creator:** Peter Steinberger (@steipete) | **First released:** Nov 2025 | **License:** Open source
**GitHub:** 250K+ stars (fastest-growing repo in GitHub history, surpassed React in ~4 months)

## Purpose

OpenClaw is an open-source, locally-run autonomous AI agent that turns your existing messaging apps into a unified interface for a personal digital assistant. The core idea: instead of switching between dozens of apps and dashboards, you message OpenClaw on WhatsApp/Telegram/Slack/etc. and it handles the rest — emails, calendars, web browsing, multi-step workflows — autonomously.

Unlike traditional chatbots that sit idle until prompted, OpenClaw is "always on." It's designed as a persistent digital employee that proactively manages tasks rather than reactively answering questions.

## Core Abilities

- **Autonomous task execution** — doesn't just answer questions; it acts. Clears inboxes, sends emails, books flights, manages calendars, checks in for flights, all without step-by-step prompting.
- **Browser control** — can navigate and interact with web pages to complete workflows that don't have APIs.
- **Multi-step workflows** — chains actions together (e.g., "find the cheapest flight to Berlin next Thursday, book it, add it to my calendar, and message my team on Slack").
- **Local execution** — runs on the user's own machine, connecting to LLMs (model-agnostic) for reasoning. Data stays local by default.
- **Plugin architecture** — extensible via connectors for new platforms and services.

## Feature Set

### Messaging Platforms (Primary UI)
WhatsApp, Telegram, Discord, Slack, Signal, Feishu, email — all via plugin connectors. Mobile access works through these existing apps, no dedicated OpenClaw app needed.

### Integrations (50+)
- **Productivity:** Email, calendar, task managers
- **Communication:** Chat platforms, SMS
- **AI models:** Model-agnostic — plugs into various LLM providers
- **Smart home:** Device control
- **Music/audio:** Streaming platforms
- **Automation:** Webhook and workflow tools

### Architecture
- Runs locally on user hardware
- Plugin-based extensibility
- Data-driven configuration
- Connects to external LLMs for reasoning (not bundled with a specific model)

## Memory Model

OpenClaw's memory is **plain Markdown files on the local filesystem** — no database, no cloud sync. The files *are* the source of truth; the agent only "remembers" what gets written to disk.

### Memory Hierarchy

| Layer | File(s) | Lifecycle | Purpose |
|-------|---------|-----------|---------|
| **Daily logs** | `memory/YYYY-MM-DD.md` | Append-only, per day | Running context, decisions, activities. Agent reads today + yesterday at session start. |
| **Long-term memory** | `MEMORY.md` | Curated, persists across months | High-level facts, preferences, project context that outlives any single day. |
| **User profile** | `USER.md` | Dynamic, agent-updated | Structured info about the user — preferences, projects, personal context. Agent updates this as it learns. |

At session start, the agent reads its identity files first (SOUL.md, IDENTITY.md), then loads today's + yesterday's daily log and MEMORY.md for context.

### Search & Retrieval (RAG-lite)

A background indexer chunks all Markdown memory files into a **local SQLite index** combining:
- **Vector search (70% weight)** — semantic similarity via embeddings
- **BM25 full-text search (30% weight)** — keyword matching

Scoring formula: `vectorWeight × vectorScore + textWeight × textScore`, using **union** (not intersection) — a chunk scoring high on either dimension gets included.

The agent has two tools for recall:
- `memory_search` — semantic search over indexed snippets
- `memory_get` — targeted read of a specific Markdown file

### Auto-preservation

When a session approaches context auto-compaction, OpenClaw triggers a **silent agentic turn** that reminds the model to write durable memory before the context is compacted. This prevents knowledge loss during long sessions.

### Third-party Memory Extensions

The memory system's simplicity (it's just Markdown + SQLite) has spawned a cottage industry of extensions:
- **memsearch** (by Milvus) — extracted and open-sourced OpenClaw's memory system as a standalone library
- **mem0** — adds persistent memory layer for OpenClaw agents
- **Adam** — 5-layer persistent memory and identity architecture (353 sessions of proof)
- **supermemory** — long-term memory and recall plugin
- Multi-layer community architectures with knowledge graphs, activation/decay systems, domain RAG

## Personality System

OpenClaw's personality is defined through a set of **plain-text configuration files** in the workspace. This is one of its most distinctive design choices — identity is data, not code.

### Core Identity Files

| File | Role |
|------|------|
| **SOUL.md** | The agent's fundamental principles, values, communication guidelines, behavioral rules, and decision-making principles. Loaded first at every session start — this is "who the agent is." |
| **IDENTITY.md** | How the agent presents itself externally — interaction style, tone, persona. SOUL.md is internal values; IDENTITY.md is the outward face. |
| **USER.md** | Structured info about the user. Enables the agent to adapt responses to match user expectations and working style. |
| **AGENTS.md** | Multi-agent configuration — defines available sub-agents and their roles. |
| **TOOLS.md** | Tool availability and usage rules. |
| **HEARTBEAT.md** | Scheduled/recurring behavior configuration. |

### Multiple Personas

Each OpenClaw workspace can have its own SOUL.md, so users can run **multiple agents with completely different personalities** for different purposes — e.g., a formal work assistant, a casual creative brainstorming partner, a technical coding agent.

### Personality Tooling

- **SoulCraft** — interactive tool that crafts agent personalities through guided conversation. Asks about desired behavior, communication style, and values, then generates a custom SOUL.md.
- **Personality Generator** — one-click generation of all identity files (IDENTITY.md, SOUL.md, USER.md, AGENTS.md, TOOLS.md, HEARTBEAT.md).
- Community-shared SOUL.md templates for common use cases.

### Design Philosophy

The key insight: personality configuration is **just Markdown**. No special syntax, no config language, no UI — write natural language describing who you want the agent to be, and the LLM follows it. This makes personality deeply customizable without requiring any programming knowledge, and version-controllable with git.

## Background

### Origin Story
Created by Austrian developer Peter Steinberger, previously known for PSPDFKit (PDF framework for iOS). Published Nov 2025 as **Clawdbot**, renamed to **Moltbot** (Jan 27, 2026) after Anthropic trademark complaints, then to **OpenClaw** three days later.

### Current Status (as of March 2026)
- [OpenClaw Official Site](https://openclaw.ai)
- [OpenClaw Wikipedia](https://en.wikipedia.org/wiki/OpenClaw)
- [TechCrunch: Steinberger joins OpenAI](https://techcrunch.com/2026/02/15/openclaw-creator-peter-steinberger-joins-openai/)
- [Star History: OpenClaw Surpasses React](https://www.star-history.com/blog/openclaw-surpasses-react-most-starred-software)
- [DigitalOcean: What is OpenClaw?](https://www.digitalocean.com/resources/articles/what-is-openclaw)
- [Fortune: Who is Peter Steinberger?](https://fortune.com/2026/02/19/openclaw-who-is-peter-steinberger-openai-sam-altman-anthropic-moltbook/)
- [The New Stack: Is it safe?](https://thenewstack.io/openclaw-github-stars-security/)
- [OpenClaw Docs: Memory](https://docs.openclaw.ai/concepts/memory)
- [OpenClaw Docs: SOUL.md Template](https://docs.openclaw.ai/reference/templates/SOUL)
- [Milvus: We Extracted OpenClaw's Memory System (memsearch)](https://milvus.io/blog/we-extracted-openclaws-memory-system-and-opensourced-it-memsearch.md)
- [Medium: OpenClaw Memory Architecture Explained](https://medium.com/@shivam.agarwal.in/agentic-ai-openclaw-moltbot-clawdbots-memory-architecture-explained-61c3b9697488)
- [Medium: How I Built Professional AI Personas (IDENTITY.md)](https://alirezarezvani.medium.com/openclaw-moltbot-identity-md-how-i-built-professional-ai-personas-that-actually-work-c964a44001ab)
- [VelvetShark: OpenClaw Memory Masterclass](https://velvetshark.com/openclaw-memory-masterclass)
- [LumaDock: How OpenClaw Memory Works](https://lumadock.com/tutorials/openclaw-memory-explained)
