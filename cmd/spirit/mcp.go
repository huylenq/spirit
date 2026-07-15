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
## Batches and runbooks (W8)

- **One batch = one decision.** ` + "`run_actions`" + ` executes a validated batch of steps as a single tool call, so the human approves the WHOLE batch in one permission round-trip (the approval overlay renders every step with its target and risk class). Prefer one batch over N separate side-effect calls when the steps belong to one intent.
- **Plan before destructive batches.** A batch containing ` + "`kill`" + `, ` + "`later`" + ` with kill, or ` + "`commit`" + ` with done follows the approval table: show the ` + "`plan_actions`" + ` preview and get explicit approval unless the user gave the exact imperative and target.
- **Partial failure is structured.** Default ` + "`on_error: stop`" + `: steps after a failure are skipped (receipts say so) and returned in ` + "`remainder`" + ` — resume by resubmitting the remainder with ` + "`resume_of`" + ` set to the failed batch's ` + "`batch_id`" + `. Never re-run already-executed steps.
- **Reconcile queued steps by action id.** Each queued step's receipt ` + "`action_id`" + ` is stamped onto the queue item; create an ` + "`action_reconciled`" + ` watch with that ` + "`action_id`" + ` to be told when exactly that instruction is delivered or fails.
- **Runbooks are named batch emitters.** ` + "`list_runbooks`" + ` → ` + "`explain_runbook`" + ` (metadata, zero execution) → ` + "`plan_runbook`" + ` (dry-run; the build phase is structurally side-effect-free) → ` + "`run_runbook`" + ` (the emitted batch rides the run_actions pipeline). Always explain/plan a runbook that declares destructive action classes before running it.

## Session status vocabulary

- ` + "`agent-turn`" + ` / "working" — the coding agent is actively working.
- ` + "`user-turn`" + ` / "idle" — waiting for you/the user; safe to ` + "`send_message`" + `.
- ` + "`is_waiting`" + ` — blocked on a permission or input prompt; never guess the answer, surface it.

## Plan awareness

Spirit reads each project's ` + "`laxicon/plans/*.md`" + ` and ` + "`laxicon/specs/*.md`" + ` (frontmatter status, checkbox progress) and surfaces them in your context — plans say what the work is *for*; sessions are its execution:

- The fleet snapshot carries an ` + "`<active-plans>`" + ` block: per project root, the live plans with their status and checkbox tallies, plus spec names as held truth.
- The selected-session dossier carries a plan section: a ` + "`plan: <slug>`" + ` line when the session has a ` + "`plan:<slug>`" + ` tag (that tag is the correlation of record — you maintain it), or a ` + "`plan-hint:`" + ` line listing the project's active plans as cwd/branch adjacency only, never asserted truth.

Spirit never writes plan files. You do, with your own file-edit tools — every such edit goes through the human approval flow, which is expected and correct.

## Intent playbooks

How to execute the recurring intents. These are behaviors, not commands — a bare "anything need me?" should work because you already know the fleet.

### Review — verification brokering

You are the broker, not the reviewer-of-record. Collect the claim (` + "`get_session`" + `, ` + "`get_summary`" + `, the dossier), then delegate the actual check — use ` + "`delegate_task`" + ` for an independent read of the diff/tests, or send a bounded verification request to the session itself — and relay the verdict with a recommendation (accept / fix / needs human). Never inline heavy transcripts or diffs into your own context; ` + "`get_transcript`" + `/` + "`get_diff`" + ` are for targeted excerpts, and bulk evidence belongs in a delegated task whose internals never enter this conversation.

### Triage — plan-grounded standup

Run a standup over dossiers and digests, not raw transcripts. For each session that needs attention: which plan item does it serve (its ` + "`plan:<slug>`" + ` tag, or the project's active plans as a hint)? Classify it — **unblock** (needs a human decision or input), **delegate** (a bounded corrective prompt suffices), **verify** (claims done, needs review), **park** (` + "`later_session`" + `), **discard** (` + "`kill_session`" + `) — then propose the smallest useful batch, not a status narration.

### Plan hygiene — you are the PM of the board

Keep plan files true: tick checkboxes (` + "`- [ ]`" + ` → ` + "`- [x]`" + `), update frontmatter ` + "`status:`" + `, add as-built notes — with your own file-edit tools, riding the human approval flow per edit. Specs are held truth: propose amendments in prose and let the human decide; never edit a spec unprompted.

### Reconciliation — a receipt is not proof

After any side-effect tool call, confirm the target actually reacted: ` + "`wait_session`" + ` for the expected phase (a ` + "`send_message`" + ` should be followed by the target entering working; a ` + "`kill_session`" + ` by the session vanishing), or ` + "`get_session`" + ` to compare observed state against intent. Report what was observed, not what was requested.

### Correlation — record what you infer

When you infer or are told which plan a session serves, record it: ` + "`set_tags`" + ` with ` + "`plan:<slug>`" + ` (preserve the session's other tags — set_tags replaces the whole list). That tag is the durable session↔plan link Spirit surfaces back to you.

## Notes

- ` + "`send_message`" + ` requires the target to be idle; use ` + "`queue_message`" + ` when it may be busy.
- The Spirit daemon must be running (it is, if these tools are registered).
- Session ids are UUIDs — always fetch them fresh from ` + "`list_sessions`" + `.
`)

	return b.String()
}
