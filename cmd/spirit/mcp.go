package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/huylenq/spirit/internal/mcpserver"
)

// runMcp runs the `spirit mcp` stdio MCP server. It is spawned by Hermes (registered
// via the ACP mcp_servers array at Lulu's session open) and speaks JSON-RPC 2.0 over
// stdin/stdout. stdout is the protocol channel — logs go to stderr only, which Hermes
// routes to its own log.
func runMcp() {
	log.SetOutput(os.Stderr)

	client := connectOrDie() // ConnectRPCOnly, auto-starts the daemon if needed
	defer client.Close()

	srv := mcpserver.New(client)
	if err := srv.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "spirit mcp: %v\n", err)
		os.Exit(1)
	}
}

// --- Hermes skill install ---

// hermesSkillDir is Spirit's skill directory under the Hermes user skills home
// (~/.hermes/skills/spirit). Installing here is Spirit-owned output; hermes-agent
// source is never touched.
func hermesSkillDir() string {
	home := os.Getenv("HERMES_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".hermes")
	}
	return filepath.Join(home, "skills", "spirit")
}

// installHermesSkill writes the generated SKILL.md into ~/.hermes/skills/spirit so
// Lulu's operation contract survives session resets and is versioned Hermes-side
// (spec Decision 3). It is idempotent: the file is only rewritten when its content
// changes (generated content, so overwrite is safe). Returns whether it changed.
func installHermesSkill() (bool, error) {
	dir := hermesSkillDir()
	path := filepath.Join(dir, "SKILL.md")
	want := genHermesSkillMD()

	if existing, err := os.ReadFile(path); err == nil {
		if sha256.Sum256(existing) == sha256.Sum256([]byte(want)) {
			return false, nil // already up to date
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// genHermesSkillMD generates the Hermes skill describing Spirit's MCP operation
// surface. The tool inventory is pulled from mcpserver.Tools() so the doc never
// drifts from the actual registered tools.
func genHermesSkillMD() string {
	var b strings.Builder

	b.WriteString(`---
name: spirit
description: "Operate the Spirit fleet: inspect and control Claude Code / Codex coding sessions across tmux panes via typed MCP tools. Use when asked to review, message, spawn, queue, tag, or kill a session, or to triage what is running."
version: 1.0.0
platforms: [linux, macos, windows]
metadata:
  hermes:
    tags: [spirit, sessions, claude-code, codex, fleet, orchestration, triage]
    related_skills: []
---

# Spirit

Spirit is a TUI + daemon that monitors and orchestrates coding sessions (Claude Code, Codex) running in tmux panes. Its operation surface is exposed to you as **typed MCP tools** registered under the ` + "`spirit`" + ` server at session open. The tool schemas are the contract — call them directly; do not shell out to ` + "`spirit agent`" + `.

**Every tool takes an explicit ` + "`session_id`" + `.** Get ids from ` + "`list_sessions`" + ` first. There is no implicit "selected session" yet — always name the target.

## Operation classes

- **Read-only** (inspect): execute freely, they return data.
- **Side-effect** (mutate the fleet): each returns an **ActionReceipt** — a structured record of ` + "`action_id`" + `, target (session id + how it was resolved), operation + params, ` + "`delivery_outcome`" + `, and ` + "`observed_state_after`" + ` (the session's status/queue captured just after the action). Use the receipt to reconcile: a clean call is not proof a coding agent consumed the instruction — check ` + "`observed_state_after`" + ` (e.g. a ` + "`send_message`" + ` should be followed by the target entering ` + "`agent-turn`" + `).

Follow proposal → approval → receipt → reconciliation: send/queue for a direct imperative, but propose before destructive actions (` + "`kill_session`" + `, ` + "`commit_session`" + ` with done, broad fan-out).

## Tools

`)

	var reads, effects []mcpserver.ToolInfo
	for _, t := range mcpserver.Tools() {
		if t.SideEffect {
			effects = append(effects, t)
		} else {
			reads = append(reads, t)
		}
	}

	b.WriteString("### Read-only\n\n")
	for _, t := range reads {
		fmt.Fprintf(&b, "- **`%s`** — %s\n", t.Name, t.Description)
	}
	b.WriteString("\n### Side-effect (return an ActionReceipt)\n\n")
	for _, t := range effects {
		fmt.Fprintf(&b, "- **`%s`** — %s\n", t.Name, t.Description)
	}

	b.WriteString(`
## Session status vocabulary

- ` + "`agent-turn`" + ` / "working" — the coding agent is actively working.
- ` + "`user-turn`" + ` / "idle" — waiting for you/the user; safe to ` + "`send_message`" + `.
- ` + "`is_waiting`" + ` — blocked on a permission or input prompt; never guess the answer, surface it.

## Notes

- ` + "`send_message`" + ` requires the target to be idle; use ` + "`queue_message`" + ` when it may be busy.
- The Spirit daemon must be running (it is, if these tools are registered).
- Session ids are UUIDs — always fetch them fresh from ` + "`list_sessions`" + `.
`)

	return b.String()
}
