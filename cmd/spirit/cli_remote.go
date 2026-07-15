package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/receipt"
	"github.com/huylenq/spirit/internal/scripting"
)

// All subcommands output JSON to stdout and errors to stderr.
// Exit 0 on success, 1 on error.

// --- Command registry ---

// agentCommand describes a spirit agent subcommand for dispatch and doc generation.
type agentCommand struct {
	Name     string   // verb: "sessions", "send", etc.
	Args     string   // arg syntax: "<id> <message>", "[--status idle|working]"
	Desc     string   // one-line description
	Examples []string // bash examples (each prefixed with "spirit agent")
	Handler  func()
}

// agentCommands is the single source of truth for spirit agent subcommands.
// Used for dispatch, help text, --agent-help, and SKILL.md generation.
var agentCommands = []agentCommand{
	{
		Name: "sessions", Args: "[--status idle|working]",
		Desc: "List all sessions (JSON array)",
		Examples: []string{
			"sessions",
			"sessions --status idle",
			"sessions --status working",
		},
		Handler: runSessions,
	},
	{
		Name: "session", Args: "<id>",
		Desc: "Get single session (JSON object)",
		Examples: []string{
			"session SESSION_ID",
		},
		Handler: runSession,
	},
	{
		Name: "send", Args: "<id> <message>",
		Desc: "Send message (session must be idle)",
		Examples: []string{
			`send SESSION_ID "your message here"`,
		},
		Handler: runSend,
	},
	{
		Name: "queue", Args: "<id> <message>",
		Desc: "Queue for delivery when idle",
		Examples: []string{
			`queue SESSION_ID "when you are done, run the linter"`,
		},
		Handler: runQueue,
	},
	{
		Name: "spawn", Args: "<cwd> [-m <msg>] [--provider claude|codex] [--new-window | --tmux-session <name>]",
		Desc: "Spawn new session (defaults to splitting the caller's tmux pane; --provider selects the agent)",
		Examples: []string{
			"spawn /path/to/project",
			`spawn /path/to/project -m "fix the failing tests"`,
			"spawn /path/to/project --provider codex",
			"spawn /path/to/project --new-window",
		},
		Handler: runSpawn,
	},
	{
		Name: "kill", Args: "<id>",
		Desc: "Kill session",
		Examples: []string{
			"kill SESSION_ID",
		},
		Handler: runKill,
	},
	{
		Name: "transcript", Args: "<id> [--raw]",
		Desc: "Get transcript (--raw for all entries with metadata)",
		Examples: []string{
			"transcript SESSION_ID",
			"transcript SESSION_ID --raw",
		},
		Handler: runTranscript,
	},
	{
		Name: "diff", Args: "<id> [--hunks]",
		Desc: "Diff stats (--hunks for actual content changes)",
		Examples: []string{
			"diff SESSION_ID",
			"diff SESSION_ID --hunks",
		},
		Handler: runDiff,
	},
	{
		Name: "summary", Args: "<id>",
		Desc: "Cached AI summary",
		Examples: []string{
			"summary SESSION_ID",
		},
		Handler: runSummary,
	},
	{
		Name: "synthesize", Args: "<id>|--all",
		Desc: "Trigger AI summary synthesis",
		Examples: []string{
			"synthesize SESSION_ID",
			"synthesize --all",
		},
		Handler: runSynthesize,
	},
	{
		Name: "commit", Args: "<id> [--done]",
		Desc: "Commit (--done to auto-kill after)",
		Examples: []string{
			"commit SESSION_ID",
			"commit SESSION_ID --done",
		},
		Handler: runCommit,
	},
	{
		Name: "later", Args: "<id> [--kill]",
		Desc: "Mark later (--kill to also kill)",
		Examples: []string{
			"later SESSION_ID",
			"later SESSION_ID --kill",
		},
		Handler: runLater,
	},
	{
		Name: "hooks", Args: "<id>",
		Desc: "Hook events for a session",
		Examples: []string{
			"hooks SESSION_ID",
		},
		Handler: runHookEvents,
	},
	{
		Name: "inspect", Args: "<id> [--transcript-tail N] [--diff-hunks] [--hooks]",
		Desc: "Aggregate dossier: session snapshot + optional transcript tail, diff hunks, hook events",
		Examples: []string{
			"inspect SESSION_ID",
			"inspect SESSION_ID --transcript-tail 10",
			"inspect SESSION_ID --diff-hunks --hooks",
		},
		Handler: runInspect,
	},
	{
		Name: "wait", Args: "<id> --phase idle|working|cycle [--timeout N]",
		Desc: "Block until session reaches phase (cycle = working then idle); nonzero exit on timeout",
		Examples: []string{
			"wait SESSION_ID --phase idle",
			"wait SESSION_ID --phase cycle --timeout 120",
		},
		Handler: runWait,
	},
	{
		Name: "tag", Args: "<id> [tag...]",
		Desc: "Set a session's tags (replaces existing; no tags clears them)",
		Examples: []string{
			"tag SESSION_ID review urgent",
			"tag SESSION_ID",
		},
		Handler: runTag,
	},
	{
		Name: "note", Args: "<id> <note>",
		Desc: "Set a session's note (empty string clears it)",
		Examples: []string{
			`note SESSION_ID "waiting on API review"`,
			`note SESSION_ID ""`,
		},
		Handler: runNote,
	},
	{
		Name: "plan", Args: "'<batch-json>'|-",
		Desc: "Dry-run a batch of actions: validate + resolve targets + risk classes; executes NOTHING",
		Examples: []string{
			`plan '[{"op":"queue","session_id":"SESSION_ID","message":"run the linter"}]'`,
			`plan '{"actions":[{"op":"kill","session_id":"SESSION_ID"}]}'`,
		},
		Handler: runPlan,
	},
	{
		Name: "action", Args: "'<batch-json>'|-",
		Desc: "Execute a validated batch as one unit; one ActionReceipt per step, stop-on-failure remainder is resubmittable (resume_of)",
		Examples: []string{
			`action '[{"op":"queue","session_id":"SESSION_ID","message":"run the linter"},{"op":"wait","session_id":"SESSION_ID","phase":"cycle"}]'`,
			`action '{"actions":[...],"on_error":"continue"}'`,
		},
		Handler: runAction,
	},
	{
		Name: "runbook", Args: "list|explain|plan|run <name> [--param k=v ...]",
		Desc: "Named runbooks: explain (metadata only), plan (dry-run the emitted batch), run (execute with per-step receipts)",
		Examples: []string{
			"runbook list",
			"runbook explain broadcast",
			`runbook plan broadcast --param message="wrap up" --param project=/path/to/repo`,
			`runbook run broadcast --param message="wrap up"`,
		},
		Handler: runRunbook,
	},
	{
		Name: "backlog", Args: "list|create|update|delete <args>",
		Desc: "Backlog CRUD",
		Examples: []string{
			"backlog list /path/to/project",
			`backlog create /path/to/project "implement the retry logic"`,
			`backlog update /path/to/project ITEM_ID "updated description"`,
			"backlog delete /path/to/project ITEM_ID",
		},
		Handler: runBacklog,
	},
}

// --- Helpers ---

// emitReceipt prints the real receipt.ActionReceipt for a mutating agent verb
// (W8 receipt unification, closing the W5 seam): action_id, target identity,
// delivery outcome, and — for session-targeted ops — the observed post-action
// state for reconciliation (Decision 5). Alive:false after a kill is the
// expected observation.
func emitReceipt(client *daemon.Client, operation, sessionID string, outcome receipt.Outcome, params map[string]any) {
	rcpt := receipt.New(operation, receipt.Target{SessionID: sessionID, ResolvedBy: receipt.ResolvedExplicit})
	rcpt.Params = params
	rcpt.DeliveryOutcome = outcome
	if sessionID != "" {
		if sessions, err := client.Sessions(""); err == nil {
			obs := &receipt.ObservedState{Alive: false}
			for _, s := range sessions {
				if s.SessionID == sessionID {
					rcpt.Target.PaneID = s.PaneID
					rcpt.Target.DisplayName = s.DisplayName()
					obs = &receipt.ObservedState{Status: s.Status.String(), IsWaiting: s.IsWaiting, QueueLen: len(s.QueuePending), Alive: true}
					break
				}
			}
			rcpt.ObservedState = obs
		}
	}
	jsonOut(rcpt)
}

// jsonOut marshals v to stdout as indented JSON.
func jsonOut(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// dieUsage prints usage to stderr and exits 1.
func dieUsage(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// connectOrDie connects to the daemon or exits with an error.
func connectOrDie() *daemon.Client {
	client, err := daemon.ConnectRPCOnly()
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not running: %v\n", err)
		os.Exit(1)
	}
	return client
}

// resolveSessionOrDie finds a session by ID or exits with an error.
func resolveSessionOrDie(client *daemon.Client, id string) claude.ClaudeSession {
	sessions, err := client.Sessions("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "list sessions: %v\n", err)
		os.Exit(1)
	}
	for _, s := range sessions {
		if s.SessionID == id {
			return s
		}
	}
	fmt.Fprintf(os.Stderr, "session not found: %s\n", id)
	os.Exit(1)
	return claude.ClaudeSession{} // unreachable
}

// --- Dispatcher ---

// runAgent dispatches `spirit agent <verb>` subcommands via the command table.
// Shifts os.Args by 1 so handlers see verb at [1], first arg at [2].
func runAgent() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: spirit agent <command> [args...]\n\nCommands:")
		for _, cmd := range agentCommands {
			fmt.Fprintf(os.Stderr, "  %-40s %s\n", cmd.Name+" "+cmd.Args, cmd.Desc)
		}
		os.Exit(1)
	}

	// Shift args so handlers see verb at [1], first arg at [2].
	os.Args = os.Args[1:]
	verb := os.Args[1]

	for _, cmd := range agentCommands {
		if cmd.Name == verb {
			cmd.Handler()
			return
		}
	}
	dieUsage(fmt.Sprintf("unknown agent command: %s\nRun 'spirit agent' for usage.", verb))
}

// --- Agent help (--agent-help) ---

// agentHelpText generates the machine-readable reference for --agent-help.
func agentHelpText() string {
	var b strings.Builder
	b.WriteString(`# spirit agent — Machine-Friendly Session Management

CLI for monitoring and controlling Claude Code sessions across tmux panes.
All commands output JSON to stdout. Errors go to stderr with exit code 1.
Requires a running daemon (auto-started on first use).

## Commands

`)
	for _, cmd := range agentCommands {
		fmt.Fprintf(&b, "  spirit agent %-42s %s\n", cmd.Name+" "+cmd.Args, cmd.Desc)
	}

	b.WriteString(`
## Orchestration
  spirit orchestrator register <id>                Exclude session from listings
  spirit orchestrator unregister <id>              Re-include session

## Escape Hatch
  spirit eval -e '<lua>'                           Evaluate Lua for advanced queries

`)
	b.WriteString(scripting.LuaScriptingReference)
	return b.String()
}

// --- SKILL.md generation ---

const skillFrontmatter = `---
name: Spirit
description: Use this skill when asked about Claude Code sessions, when you need to check what coding sessions are running, send messages to sessions, manage session lifecycle, or orchestrate multi-session development work. Triggers on mentions of "sessions", "Claude Code", "spirit", "coding agents", "what's running", or when the user asks to check on, interact with, or manage development sessions.
---`

// genSkillMD generates the complete SKILL.md content from the command registry.
func genSkillMD() string {
	var b strings.Builder

	b.WriteString(skillFrontmatter)
	b.WriteString("\n\n")

	b.WriteString(`# Spirit

Spirit is a TUI + daemon that monitors and orchestrates Claude Code sessions running in tmux panes. You can interact with it via ` + "`spirit agent <command>`" + ` which talks to the running daemon and returns JSON.

**Prerequisite:** The spirit daemon must be running. If commands fail with connection errors, tell the user to start it (` + "`spirit daemon`" + ` or open the TUI).

## Commands

`)

	// Generate command reference from registry
	for _, cmd := range agentCommands {
		fmt.Fprintf(&b, "### %s\n\n", cmd.Name)
		fmt.Fprintf(&b, "%s\n\n", cmd.Desc)
		b.WriteString("```bash\n")
		for _, ex := range cmd.Examples {
			fmt.Fprintf(&b, "spirit agent %s\n", ex)
		}
		b.WriteString("```\n\n")
	}

	// Static sections
	b.WriteString(`### orchestrator

Self-exclude from session listings (for orchestrator patterns):

` + "```bash" + `
spirit orchestrator register SESSION_ID
spirit orchestrator unregister SESSION_ID
` + "```" + `

## Session Object Fields

Each session object returned by ` + "`spirit agent sessions`" + ` or ` + "`spirit agent session`" + ` contains:

| Field | Type | Description |
|-------|------|-------------|
| ` + "`id`" + ` | string | Session UUID |
| ` + "`pane_id`" + ` | string | tmux pane ID |
| ` + "`project`" + ` | string | Project directory name |
| ` + "`cwd`" + ` | string | Full working directory path |
| ` + "`git_branch`" + ` | string | Current git branch |
| ` + "`status`" + ` | string | ` + "`\"idle\"`" + ` or ` + "`\"working\"`" + ` |
| ` + "`display_name`" + ` | string | Best available name (custom > synthesized > first message) |
| ` + "`first_message`" + ` | string | First user prompt |
| ` + "`last_user_message`" + ` | string | Most recent user prompt |
| ` + "`synthesized_title`" + ` | string | LLM-generated summary |
| ` + "`is_waiting`" + ` | bool | Waiting for user input (permission prompt, etc.) |
| ` + "`compact_count`" + ` | number | How many times context was compacted |
| ` + "`stop_reason`" + ` | string | Why the session stopped |
| ` + "`created_at`" + ` | number | Unix timestamp |
| ` + "`last_changed`" + ` | number | Unix timestamp |

## Important Notes

- Session IDs are UUIDs. Always get them from ` + "`spirit agent sessions`" + ` first.
- ` + "`send`" + ` types into the tmux pane — the session must be idle to accept input.
- ` + "`queue`" + ` is safer for fire-and-forget — it waits for idle automatically.
- All commands return JSON to stdout.
- ` + "`spirit eval -e '<lua>'`" + ` remains available as an escape hatch for advanced queries not covered by agent commands.
`)

	return b.String()
}

// --- Handler functions (unchanged) ---

func runSessions() {
	filter := ""
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--status" && i+1 < len(os.Args) {
			filter = os.Args[i+1]
			i++
		}
	}

	client := connectOrDie()
	defer client.Close()

	sessions, err := client.Sessions(filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessions: %v\n", err)
		os.Exit(1)
	}
	jsonOut(sessions)
}

func runSession() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent session <session-id>")
	}
	id := os.Args[2]

	client := connectOrDie()
	defer client.Close()

	s := resolveSessionOrDie(client, id)
	jsonOut(s)
}

func runSend() {
	if len(os.Args) < 4 {
		dieUsage("usage: spirit agent send <session-id> <message>")
	}
	id := os.Args[2]
	msg := os.Args[3]

	client := connectOrDie()
	defer client.Close()

	if err := client.Send(id, msg); err != nil {
		fmt.Fprintf(os.Stderr, "send: %v\n", err)
		os.Exit(1)
	}
	emitReceipt(client, "send", id, receipt.OutcomeDelivered, map[string]any{"message": msg})
}

func runQueue() {
	if len(os.Args) < 4 {
		dieUsage("usage: spirit agent queue <session-id> <message>")
	}
	id := os.Args[2]
	msg := os.Args[3]

	client := connectOrDie()
	defer client.Close()

	s := resolveSessionOrDie(client, id)
	params := map[string]any{"message": msg}
	rcptActionID := receipt.NewActionID()
	itemID, err := client.QueueMessage(s.PaneID, id, msg, rcptActionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "queue: %v\n", err)
		os.Exit(1)
	}
	if itemID != "" {
		params["queue_item_id"] = itemID
	}
	rcpt := receipt.New("queue", receipt.Target{SessionID: id, ResolvedBy: receipt.ResolvedExplicit, PaneID: s.PaneID, DisplayName: s.DisplayName()})
	rcpt.ActionID = rcptActionID // the id the queue item carries for ledger linkage
	rcpt.Params = params
	rcpt.DeliveryOutcome = receipt.OutcomeQueued
	if sessions, err := client.Sessions(""); err == nil {
		for _, cur := range sessions {
			if cur.SessionID == id {
				rcpt.ObservedState = &receipt.ObservedState{Status: cur.Status.String(), IsWaiting: cur.IsWaiting, QueueLen: len(cur.QueuePending), Alive: true}
				break
			}
		}
	}
	jsonOut(rcpt)
}

func runSpawn() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent spawn <cwd> [-m <msg>] [--new-window | --tmux-session <name>]")
	}
	cwd := os.Args[2]
	message := ""
	tmuxSession := ""
	provider := ""
	forceNewWindow := false

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--message", "-m":
			if i+1 < len(os.Args) {
				message = os.Args[i+1]
				i++
			}
		case "--provider":
			if i+1 < len(os.Args) {
				provider = os.Args[i+1]
				i++
			}
		case "--tmux-session":
			if i+1 < len(os.Args) {
				tmuxSession = os.Args[i+1]
				i++
			}
		case "--new-window":
			forceNewWindow = true
		}
	}

	// Default: if the caller is inside a tmux pane, split that pane so the new
	// session lands next to its orchestrator. --new-window or --tmux-session
	// opts out of split behavior.
	splitFromPane := ""
	if !forceNewWindow && tmuxSession == "" {
		splitFromPane = os.Getenv("TMUX_PANE")
	}

	client := connectOrDie()
	defer client.Close()

	// Provider defaults to Claude. Unknown providers are rejected by the daemon's
	// provider registry / capability gate (no hardcoded name list here).
	providerID := agent.ProviderClaude
	if provider != "" {
		providerID = agent.ProviderID(provider)
	}

	result, err := client.SpawnProvider(providerID, cwd, tmuxSession, message, splitFromPane)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn: %v\n", err)
		os.Exit(1)
	}
	jsonOut(result)
}

func runKill() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent kill <session-id>")
	}
	id := os.Args[2]

	client := connectOrDie()
	defer client.Close()

	if err := client.Kill(id); err != nil {
		fmt.Fprintf(os.Stderr, "kill: %v\n", err)
		os.Exit(1)
	}
	emitReceipt(client, "kill", id, receipt.OutcomeCompleted, nil)
}

func runTranscript() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent transcript <session-id> [--raw]")
	}
	id := os.Args[2]
	raw := false
	for _, arg := range os.Args[3:] {
		if arg == "--raw" {
			raw = true
		}
	}

	client := connectOrDie()
	defer client.Close()

	if raw {
		entries, err := client.TranscriptEntries(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "transcript: %v\n", err)
			os.Exit(1)
		}
		jsonOut(entries)
	} else {
		msgs, _, err := client.Transcript(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "transcript: %v\n", err)
			os.Exit(1)
		}
		jsonOut(msgs)
	}
}

func runDiff() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent diff <session-id> [--hunks]")
	}
	id := os.Args[2]
	hunks := false
	for _, arg := range os.Args[3:] {
		if arg == "--hunks" {
			hunks = true
		}
	}

	client := connectOrDie()
	defer client.Close()

	if hunks {
		h, err := client.DiffHunks(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff: %v\n", err)
			os.Exit(1)
		}
		jsonOut(h)
	} else {
		stats, err := client.DiffStats(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff: %v\n", err)
			os.Exit(1)
		}
		jsonOut(stats)
	}
}

func runSummary() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent summary <session-id>")
	}
	id := os.Args[2]

	client := connectOrDie()
	defer client.Close()

	summary, err := client.Summary(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "summary: %v\n", err)
		os.Exit(1)
	}
	jsonOut(summary)
}

func runSynthesize() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent synthesize <session-id> | spirit agent synthesize --all")
	}

	client := connectOrDie()
	defer client.Close()

	if os.Args[2] == "--all" {
		results, err := client.SynthesizeAll("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "synthesize: %v\n", err)
			os.Exit(1)
		}
		jsonOut(results)
		return
	}

	id := os.Args[2]
	s := resolveSessionOrDie(client, id)
	summary, fromCache, err := client.Synthesize(s.PaneID, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "synthesize: %v\n", err)
		os.Exit(1)
	}
	jsonOut(map[string]any{"summary": summary, "from_cache": fromCache})
}

func runCommit() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent commit <session-id> [--done]")
	}
	id := os.Args[2]
	done := false
	for _, arg := range os.Args[3:] {
		if arg == "--done" {
			done = true
		}
	}

	client := connectOrDie()
	defer client.Close()

	s := resolveSessionOrDie(client, id)
	var err error
	if done {
		err = client.CommitAndDone(s.PaneID, id, s.PID)
	} else {
		err = client.CommitOnly(s.PaneID, id, s.PID)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "commit: %v\n", err)
		os.Exit(1)
	}
	emitReceipt(client, "commit", id, receipt.OutcomeCompleted, map[string]any{"done": done})
}

func runLater() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent later <session-id> [--kill]")
	}
	id := os.Args[2]
	kill := false
	for _, arg := range os.Args[3:] {
		if arg == "--kill" {
			kill = true
		}
	}

	client := connectOrDie()
	defer client.Close()

	s := resolveSessionOrDie(client, id)
	var err error
	if kill {
		err = client.LaterKill(s.PaneID, s.PID, id, "")
	} else {
		err = client.Later(s.PaneID, id, "")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "later: %v\n", err)
		os.Exit(1)
	}
	emitReceipt(client, "later", id, receipt.OutcomeCompleted, map[string]any{"kill": kill})
}

func runHookEvents() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent hooks <session-id>")
	}
	id := os.Args[2]

	client := connectOrDie()
	defer client.Close()

	events, err := client.HookEvents(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hooks: %v\n", err)
		os.Exit(1)
	}
	jsonOut(events)
}

// runInspect aggregates a bounded dossier for one session by composing existing
// daemon RPCs client-side (matching how every other verb calls the client): the
// session snapshot is always included; transcript tail, diff hunks, and hook
// events are added on demand via flags.
func runInspect() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent inspect <session-id> [--transcript-tail N] [--diff-hunks] [--hooks]")
	}
	id := os.Args[2]
	transcriptTail := 0
	wantDiffHunks := false
	wantHooks := false
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--transcript-tail":
			if i+1 < len(os.Args) {
				n, err := strconv.Atoi(os.Args[i+1])
				if err != nil || n < 0 {
					dieUsage("inspect: --transcript-tail requires a non-negative integer")
				}
				transcriptTail = n
				i++
			}
		case "--diff-hunks":
			wantDiffHunks = true
		case "--hooks":
			wantHooks = true
		}
	}

	client := connectOrDie()
	defer client.Close()

	s := resolveSessionOrDie(client, id)
	out := map[string]any{"session": s}

	if transcriptTail > 0 {
		msgs, turn, err := client.Transcript(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect transcript: %v\n", err)
			os.Exit(1)
		}
		out["transcript_tail"] = tailMessages(msgs, transcriptTail)
		out["current_turn"] = turn
	}

	if wantDiffHunks {
		hunks, err := client.DiffHunks(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect diff: %v\n", err)
			os.Exit(1)
		}
		out["diff_hunks"] = hunks
	}

	if wantHooks {
		events, err := client.HookEvents(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect hooks: %v\n", err)
			os.Exit(1)
		}
		out["hook_events"] = events
	}

	jsonOut(out)
}

// runWait blocks until a session reaches the requested phase, mirroring the Lua
// wait/cycle semantics exactly (idle=user-turn, working=agent-turn, cycle=working
// then idle). Exits nonzero on timeout so callers fail fast.
func runWait() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent wait <session-id> --phase idle|working|cycle [--timeout N]")
	}
	id := os.Args[2]
	phase := ""
	timeout := 300
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--phase":
			if i+1 < len(os.Args) {
				phase = os.Args[i+1]
				i++
			}
		case "--timeout":
			if i+1 < len(os.Args) {
				n, err := strconv.Atoi(os.Args[i+1])
				if err != nil || n <= 0 {
					dieUsage("wait: --timeout requires a positive integer")
				}
				timeout = n
				i++
			}
		}
	}
	if phase != "idle" && phase != "working" && phase != "cycle" {
		dieUsage("wait: --phase must be idle, working, or cycle")
	}

	client := connectOrDie()
	defer client.Close()

	s, err := waitForPhase(client, id, phase, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait: %v\n", err)
		os.Exit(1)
	}
	jsonOut(s)
}

// waitForPhase polls the daemon at 500ms cadence (matching internal/scripting's
// pollSession) until the session reaches the target phase. Returns an error on
// timeout.
func waitForPhase(client *daemon.Client, id, phase string, timeoutSecs int) (claude.ClaudeSession, error) {
	deadline := time.Now().Add(time.Duration(timeoutSecs) * time.Second)
	sawWorking := false
	for time.Now().Before(deadline) {
		sessions, err := client.Sessions("")
		if err != nil {
			return claude.ClaudeSession{}, err
		}
		for _, s := range sessions {
			if s.SessionID != id {
				continue
			}
			if phaseReached(phase, s.Status, &sawWorking) {
				return s, nil
			}
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return claude.ClaudeSession{}, fmt.Errorf("timeout waiting for session %s to reach phase %s", id, phase)
}

// phaseReached reports whether a session in the given status has reached the
// target phase, mirroring the Lua wait/cycle semantics exactly (idle=user-turn,
// working=agent-turn, cycle=working then idle). For "cycle" it records the first
// working observation in *sawWorking so a later idle counts as a completed round
// trip and a pre-work false-idle does not.
func phaseReached(phase string, status claude.Status, sawWorking *bool) bool {
	switch phase {
	case "idle":
		return status == claude.StatusUserTurn
	case "working":
		return status == claude.StatusAgentTurn
	case "cycle":
		if status == claude.StatusAgentTurn {
			*sawWorking = true
		}
		return status == claude.StatusUserTurn && *sawWorking
	}
	return false
}

// tailMessages returns the last n messages (all of them when there are n or
// fewer). Used to bound inspect's transcript evidence.
func tailMessages(msgs []string, n int) []string {
	if n <= 0 || len(msgs) <= n {
		return msgs
	}
	return msgs[len(msgs)-n:]
}

// runTag replaces a session's tags. `tag <id>` with no further args clears them.
func runTag() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent tag <session-id> [tag...]")
	}
	id := os.Args[2]
	tags := os.Args[3:]
	if tags == nil {
		tags = []string{}
	}

	client := connectOrDie()
	defer client.Close()

	if err := client.SetTags(id, tags); err != nil {
		fmt.Fprintf(os.Stderr, "tag: %v\n", err)
		os.Exit(1)
	}
	emitReceipt(client, "tag", id, receipt.OutcomeCompleted, map[string]any{"tags": tags})
}

// runNote sets a session's note. An empty string clears it.
func runNote() {
	if len(os.Args) < 4 {
		dieUsage("usage: spirit agent note <session-id> <note>")
	}
	id := os.Args[2]
	note := os.Args[3]

	client := connectOrDie()
	defer client.Close()

	if err := client.SetNote(id, note); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
		os.Exit(1)
	}
	emitReceipt(client, "note", id, receipt.OutcomeCompleted, map[string]any{"note": note})
}

func runBacklog() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent backlog list <cwd> | create <cwd> <body> | update <cwd> <id> <body> | delete <cwd> <id>")
	}

	client := connectOrDie()
	defer client.Close()

	sub := os.Args[2]
	switch sub {
	case "list":
		if len(os.Args) < 4 {
			dieUsage("usage: spirit agent backlog list <cwd>")
		}
		items, err := client.BacklogList(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "backlog list: %v\n", err)
			os.Exit(1)
		}
		jsonOut(items)

	case "create":
		if len(os.Args) < 5 {
			dieUsage("usage: spirit agent backlog create <cwd> <body>")
		}
		item, err := client.BacklogCreate(os.Args[3], os.Args[4])
		if err != nil {
			fmt.Fprintf(os.Stderr, "backlog create: %v\n", err)
			os.Exit(1)
		}
		jsonOut(item)

	case "update":
		if len(os.Args) < 6 {
			dieUsage("usage: spirit agent backlog update <cwd> <id> <body>")
		}
		item, err := client.BacklogUpdate(os.Args[3], os.Args[4], os.Args[5])
		if err != nil {
			fmt.Fprintf(os.Stderr, "backlog update: %v\n", err)
			os.Exit(1)
		}
		jsonOut(item)

	case "delete":
		if len(os.Args) < 5 {
			dieUsage("usage: spirit agent backlog delete <cwd> <id>")
		}
		if err := client.BacklogDelete(os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintf(os.Stderr, "backlog delete: %v\n", err)
			os.Exit(1)
		}
		rcpt := receipt.New("backlog.delete", receipt.Target{})
		rcpt.Params = map[string]any{"id": os.Args[4], "cwd": os.Args[3]}
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted
		jsonOut(rcpt)

	default:
		dieUsage("usage: spirit agent backlog list|create|update|delete ...")
	}
}
