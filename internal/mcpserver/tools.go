package mcpserver

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ledger"
	"github.com/huylenq/spirit/internal/receipt"
)

// daemonAPI is the narrow slice of the daemon client the MCP tools need. *daemon.Client
// satisfies it; tests inject a fake so the server round-trips without a live daemon.
type daemonAPI interface {
	Sessions(statusFilter string) ([]agent.Session, error)
	Transcript(sessionID string) ([]string, claude.CurrentTurn, error)
	TranscriptEntries(sessionID string) ([]claude.TranscriptEntry, error)
	DiffStats(sessionID string) (map[string]claude.FileDiffStat, error)
	DiffHunks(sessionID string) ([]claude.FileDiffHunk, error)
	HookEvents(sessionID string) ([]claude.HookEvent, error)
	Summary(sessionID string) (*claude.SessionSummary, error)
	Send(sessionID, message string) error
	Queue(paneID, sessionID, message string) error
	QueueMessage(paneID, sessionID, message, actionID string) (string, error)
	SpawnProvider(provider agent.ProviderID, cwd, tmuxSession, message, splitFromPane string) (daemon.SpawnResultData, error)
	Kill(sessionID string) error
	SetTags(sessionID string, tags []string) error
	SetNote(sessionID, note string) error
	Later(paneID, sessionID, wait string) error
	LaterKill(paneID string, pid int, sessionID, wait string) error
	CommitOnly(paneID, sessionID string, pid int) error
	CommitAndDone(paneID, sessionID string, pid int) error
	ReportActionFailure(actionID, operation, sessionID, errMsg string) error
	WatchCreate(req daemon.WatchCreateData) (ledger.Watch, error)
	WatchList() ([]ledger.Watch, error)
	WatchCancel(watchID string) (ledger.Watch, error)
	AttentionList() (daemon.AttentionListData, error)
	AttentionResolve(itemID, resolution string) error
	ReactiveStatus() (daemon.ReactiveStatusData, error)
}

// Compile-time check that the real client satisfies the interface.
var _ daemonAPI = (*daemon.Client)(nil)

// tool is one MCP tool: a typed schema plus a handler that returns the JSON payload
// and whether it represents a tool-execution error (isError).
type tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	SideEffect  bool // true if the tool mutates fleet state and returns an ActionReceipt
	Handler     func(api daemonAPI, args json.RawMessage) (payload any, isError bool)
}

// ToolInfo is a doc-facing view of a tool, for SKILL.md / help generation.
type ToolInfo struct {
	Name        string
	Description string
	SideEffect  bool
}

// Tools returns the doc-facing inventory of MCP tools (single source of truth shared
// with the generated Hermes skill).
func Tools() []ToolInfo {
	built := buildTools()
	infos := make([]ToolInfo, 0, len(built))
	for _, t := range built {
		infos = append(infos, ToolInfo{Name: t.Name, Description: t.Description, SideEffect: t.SideEffect})
	}
	return infos
}

// schema is a helper to write a tool input schema literally.
func schema(s string) json.RawMessage { return json.RawMessage(s) }

// batchInputSchema is the shared plan_actions / run_actions argument schema —
// the single batch step schema (internal/batch) expressed as JSON Schema.
const batchInputSchema = `{"type":"object","properties":{
"actions":{"type":"array","minItems":1,"description":"Ordered batch steps.","items":{"type":"object","properties":{
"op":{"type":"string","enum":["send","queue","tag","note","later","kill","commit","spawn","wait"]},
"session_id":{"type":"string","description":"Target session (required for every op except spawn)."},
"message":{"type":"string","description":"send/queue: the message; spawn: optional initial prompt."},
"tags":{"type":"array","items":{"type":"string"},"description":"tag: replacement tag list."},
"note":{"type":"string","description":"note: the note text (empty clears)."},
"kill":{"type":"boolean","description":"later: also kill the pane (destructive)."},
"done":{"type":"boolean","description":"commit: auto-kill after the commit completes (destructive)."},
"cwd":{"type":"string","description":"spawn: working directory."},
"provider":{"type":"string","enum":["claude","codex"],"description":"spawn: agent provider (default claude)."},
"tmux_session":{"type":"string","description":"spawn: tmux session to open a window in."},
"phase":{"type":"string","enum":["idle","working","cycle"],"description":"wait: target lifecycle phase."},
"timeout_seconds":{"type":"integer","description":"wait: max seconds (default 60, cap 600)."}
},"required":["op"]}},
"on_error":{"type":"string","enum":["stop","continue"],"description":"Partial-failure policy (default stop: later steps are skipped and returned as the resubmittable remainder)."},
"resume_of":{"type":"string","description":"Set to a previous batch_id when resubmitting its remainder."}
},"required":["actions"]}`

// buildTools returns the full tool inventory. Read-only tools return raw data;
// side-effect tools return an *receipt.ActionReceipt.
func buildTools() []tool {
	return []tool{
		// --- Read-only inspection ---
		{
			Name:        "list_sessions",
			Description: "List all Claude Code / Codex sessions across tmux panes. Optional status filter. Returns a JSON array of session objects (use their id for other tools).",
			InputSchema: schema(`{"type":"object","properties":{"status":{"type":"string","enum":["idle","working"],"description":"Filter by lifecycle status: idle (user-turn) or working (agent-turn)."}}}`),
			Handler:     handleListSessions,
		},
		{
			Name:        "get_session",
			Description: "Get a single session snapshot by id (provider, cwd, branch, status, queue, tags, note, overlap, waiting state).",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session UUID."}},"required":["session_id"]}`),
			Handler:     handleGetSession,
		},
		{
			Name:        "get_transcript",
			Description: "Get a session's transcript. By default returns the user-message list plus current-turn snapshot; raw=true returns all parsed entries with metadata.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"raw":{"type":"boolean","description":"Return all entries with metadata instead of the user-message tail."}},"required":["session_id"]}`),
			Handler:     handleGetTranscript,
		},
		{
			Name:        "get_diff",
			Description: "Get a session's working-tree diff. By default returns per-file stats; hunks=true returns the actual content changes.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"hunks":{"type":"boolean","description":"Return content hunks instead of per-file stats."}},"required":["session_id"]}`),
			Handler:     handleGetDiff,
		},
		{
			Name:        "get_hooks",
			Description: "Get the recorded hook events for a session (tool-use, prompt, stop, notification, etc.).",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`),
			Handler:     handleGetHooks,
		},
		{
			Name:        "get_summary",
			Description: "Get the cached AI summary for a session, if one has been synthesized.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`),
			Handler:     handleGetSummary,
		},
		{
			Name:        "list_watches",
			Description: "List reactive watches (spec Decision 10): scope, condition, response policy, FSM state, firing/LLM budgets, and last outcome. Includes recently expired/cancelled/failed watches.",
			InputSchema: schema(`{"type":"object","properties":{}}`),
			Handler:     handleListWatches,
		},
		{
			Name:        "list_attention",
			Description: "List unresolved attention items (open + delivered): category, severity, scope, one-line description, any attached recommendation, and the causal audit chain (signals → watch → policy → LLM run → delivery). Use resolve_attention to close items that no longer need the user.",
			InputSchema: schema(`{"type":"object","properties":{}}`),
			Handler:     handleListAttention,
		},
		{
			Name:        "reactive_status",
			Description: "Report whether durable reactivity is enabled/paused, whether the daemon holds a durable lease, why the reactive engine is currently eligible (gate_reason: subscriber | durable | none), the live TUI subscriber count, quiet-hours state, and the remaining global daily provider budget. READ-ONLY: enabling or disabling durable reactivity is a human act (spirit reactive enable), never a tool call — there is deliberately no MCP enable/disable.",
			InputSchema: schema(`{"type":"object","properties":{}}`),
			Handler:     handleReactiveStatus,
		},
		{
			Name:        "plan_actions",
			Description: "Dry-run a batch of actions (W8): validates the whole batch fail-fast (unknown session, capability-gated op for the target's provider, malformed step), resolves every target against the live fleet, and returns the ordered plan — one line per step with its risk class (read_only / reversible / destructive per the approval table) and approval points marked. Executes NOTHING. Use before run_actions to preview and to propose destructive batches for approval.",
			InputSchema: schema(batchInputSchema),
			Handler:     handlePlanActions,
		},
		{
			Name:        "list_runbooks",
			Description: "List the named runbooks (durable Lua definitions in ~/.spirit/runbooks plus builtins): name, description, declared params (required marked with '!'), and declared action classes. A runbook's execute phase emits a batch that rides the same action pipeline as run_actions.",
			InputSchema: schema(`{"type":"object","properties":{}}`),
			Handler:     handleListRunbooks,
		},
		{
			Name:        "explain_runbook",
			Description: "Explain a runbook without executing ANY of it (not even its build phase): metadata, declared params, declared action classes, and source path.",
			InputSchema: schema(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
			Handler:     handleExplainRunbook,
		},
		{
			Name:        "plan_runbook",
			Description: "Dry-run a runbook: runs its side-effect-free build phase (pure computation over the fleet snapshot + params — no side-effect functions exist in that VM) and returns the emitted batch as a plan with resolved targets and risk classes. Executes NOTHING.",
			InputSchema: schema(`{"type":"object","properties":{"name":{"type":"string"},"params":{"type":"object","additionalProperties":{"type":"string"},"description":"Runbook parameters (string values). Required params are marked in explain_runbook/list_runbooks."}},"required":["name"]}`),
			Handler:     handlePlanRunbook,
		},
		{
			Name:        "wait_session",
			Description: "Block until a session reaches a lifecycle phase: idle (user-turn), working (agent-turn), or cycle (a full working-then-idle round trip). Use after a side-effect tool to reconcile that the target actually reacted — e.g. send_message then wait_session working. Returns a receipt-style result with the outcome (reached | timeout | vanished) and observed session state.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"phase":{"type":"string","enum":["idle","working","cycle"],"description":"Target phase; cycle waits for the session to be observed working and then return to idle."},"timeout_seconds":{"type":"integer","description":"Max seconds to wait (default 60, capped at 600)."}},"required":["session_id","phase"]}`),
			Handler:     handleWaitSession,
		},

		// --- Side effects (return an ActionReceipt) ---
		{
			Name:        "run_actions",
			Description: "Execute a validated batch of actions as ONE unit (one tool call = one human decision for the whole batch). Validates exactly like plan_actions — an invalid batch is rejected whole, never half-executed — then runs the steps in order through the same daemon operations the individual tools use, returning one ActionReceipt per step (stamped with the batch_id). Partial failure: on_error 'stop' (default) skips the steps after a failure and returns them verbatim in 'remainder' — resume by resubmitting the remainder with resume_of set to this batch_id; 'continue' runs every step regardless. Destructive steps (kill, later+kill, commit+done) follow the approval table: propose via plan_actions first unless the user gave the exact imperative and target.",
			InputSchema: schema(batchInputSchema),
			SideEffect:  true,
			Handler:     handleRunActions,
		},
		{
			Name:        "run_runbook",
			Description: "Execute a named runbook: its build phase emits a batch which rides the same action pipeline as run_actions — per-step ActionReceipts, stop-on-failure with a resubmittable remainder. Structured results, never bare terminal side effects. Use explain_runbook/plan_runbook first for anything declaring destructive action classes.",
			InputSchema: schema(`{"type":"object","properties":{"name":{"type":"string"},"params":{"type":"object","additionalProperties":{"type":"string"}}},"required":["name"]}`),
			SideEffect:  true,
			Handler:     handleRunRunbook,
		},
		{
			Name:        "send_message",
			Description: "Send a message to a session's tmux pane now. The session must be idle to accept input. Returns an ActionReceipt with the observed post-send session state for reconciliation.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"message":{"type":"string"}},"required":["session_id","message"]}`),
			SideEffect:  true,
			Handler:     handleSendMessage,
		},
		{
			Name:        "queue_message",
			Description: "Queue a message for delivery when the session next becomes idle (fire-and-forget; safe when the session is busy). Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"message":{"type":"string"}},"required":["session_id","message"]}`),
			SideEffect:  true,
			Handler:     handleQueueMessage,
		},
		{
			Name:        "spawn_session",
			Description: "Spawn a new coding session in the given directory. provider defaults to claude. Splits a new tmux window unless tmux_session is given. Returns an ActionReceipt with the new session id.",
			InputSchema: schema(`{"type":"object","properties":{"cwd":{"type":"string","description":"Working directory for the new session."},"provider":{"type":"string","enum":["claude","codex"]},"message":{"type":"string","description":"Optional initial prompt."},"tmux_session":{"type":"string","description":"Optional tmux session name to open a new window in."}},"required":["cwd"]}`),
			SideEffect:  true,
			Handler:     handleSpawnSession,
		},
		{
			Name:        "kill_session",
			Description: "Kill a session (SIGTERM + kill pane + cleanup). Destructive. Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`),
			SideEffect:  true,
			Handler:     handleKillSession,
		},
		{
			Name:        "set_tags",
			Description: "Replace a session's attention tags. Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}},"required":["session_id","tags"]}`),
			SideEffect:  true,
			Handler:     handleSetTags,
		},
		{
			Name:        "set_note",
			Description: "Set a session's freeform note. Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"note":{"type":"string"}},"required":["session_id","note"]}`),
			SideEffect:  true,
			Handler:     handleSetNote,
		},
		{
			Name:        "later_session",
			Description: "Mark a session for later (keeps the pane alive). kill=true also kills the pane. Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"kill":{"type":"boolean","description":"Also kill the pane after marking later."}},"required":["session_id"]}`),
			SideEffect:  true,
			Handler:     handleLaterSession,
		},
		{
			Name:        "commit_session",
			Description: "Run the commit workflow in a session. done=true registers auto-kill after the commit completes. Destructive/high-cost. Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string"},"done":{"type":"boolean","description":"Auto-kill the session after committing."}},"required":["session_id"]}`),
			SideEffect:  true,
			Handler:     handleCommitSession,
		},
		{
			Name:        "create_watch",
			Description: "Create a reactive watch: while a TUI client is attached, Spirit reacts to the watched condition — inbox records it, notify raises one coalesced notification, inspect_and_recommend additionally runs one bounded LLM inspection and attaches a proposal to the attention item (never a prompt to a coding session, never fleet mutation). A watch must have an expiry and rate limit; defaults: 24h expiry, 60s cooldown, 20 firings, LLM budget 5. Returns an ActionReceipt whose action_id is the watch's created_by_request_id.",
			InputSchema: schema(`{"type":"object","properties":{"session_id":{"type":"string","description":"Watch one session."},"project":{"type":"string","description":"Watch a whole project (repo root path). Omit both for fleet-wide."},"action_id":{"type":"string","description":"Anchor an action_reconciled watch to ONE action (e.g. a batch step's action_id): fires exactly when that action's delivery/failure signal lands."},"condition":{"type":"string","enum":["completed_turn","waiting","overlap","action_reconciled"]},"response":{"type":"string","enum":["inbox","notify","inspect_and_recommend"]},"expires_in_minutes":{"type":"integer","description":"Watch lifetime (default 1440 = 24h, max 7 days)."},"cooldown_seconds":{"type":"integer","description":"Minimum gap between firings (default 60)."},"max_firings":{"type":"integer","description":"Total firing budget (default 20, max 100)."},"llm_budget":{"type":"integer","description":"Max LLM runs for inspect_and_recommend (default 5, max 20)."}},"required":["condition","response"]}`),
			SideEffect:  true,
			Handler:     handleCreateWatch,
		},
		{
			Name:        "cancel_watch",
			Description: "Cancel a live reactive watch by watch_id. Returns an ActionReceipt with the watch's final record.",
			InputSchema: schema(`{"type":"object","properties":{"watch_id":{"type":"string"}},"required":["watch_id"]}`),
			SideEffect:  true,
			Handler:     handleCancelWatch,
		},
		{
			Name:        "resolve_attention",
			Description: "Resolve an attention item that no longer needs the user (verified, handled, or moot). Records the resolution on the item's audit chain. Returns an ActionReceipt.",
			InputSchema: schema(`{"type":"object","properties":{"item_id":{"type":"string"},"resolution":{"type":"string","description":"Why it is resolved (e.g. 'verified: tests pass')."}},"required":["item_id"]}`),
			SideEffect:  true,
			Handler:     handleResolveAttention,
		},
	}
}

// --- argument shapes ---

type idArgs struct {
	SessionID string `json:"session_id"`
}

// --- read-only handlers ---

func handleListSessions(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(args, &a)
	sessions, err := api.Sessions(a.Status)
	if err != nil {
		return errPayload("list_sessions", err), true
	}
	return sessions, false
}

func handleGetSession(api daemonAPI, args json.RawMessage) (any, bool) {
	var a idArgs
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return errPayload("get_session", fmt.Errorf("session_id is required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	if !ok {
		return errPayload("get_session", fmt.Errorf("session not found: %s", a.SessionID)), true
	}
	return s, false
}

func handleGetTranscript(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Raw       bool   `json:"raw"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return errPayload("get_transcript", fmt.Errorf("session_id is required")), true
	}
	if a.Raw {
		entries, err := api.TranscriptEntries(a.SessionID)
		if err != nil {
			return errPayload("get_transcript", err), true
		}
		return entries, false
	}
	msgs, turn, err := api.Transcript(a.SessionID)
	if err != nil {
		return errPayload("get_transcript", err), true
	}
	return map[string]any{"messages": msgs, "turn": turn}, false
}

func handleGetDiff(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Hunks     bool   `json:"hunks"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return errPayload("get_diff", fmt.Errorf("session_id is required")), true
	}
	if a.Hunks {
		hunks, err := api.DiffHunks(a.SessionID)
		if err != nil {
			return errPayload("get_diff", err), true
		}
		return hunks, false
	}
	stats, err := api.DiffStats(a.SessionID)
	if err != nil {
		return errPayload("get_diff", err), true
	}
	return stats, false
}

func handleGetHooks(api daemonAPI, args json.RawMessage) (any, bool) {
	var a idArgs
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return errPayload("get_hooks", fmt.Errorf("session_id is required")), true
	}
	events, err := api.HookEvents(a.SessionID)
	if err != nil {
		return errPayload("get_hooks", err), true
	}
	return events, false
}

func handleGetSummary(api daemonAPI, args json.RawMessage) (any, bool) {
	var a idArgs
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return errPayload("get_summary", fmt.Errorf("session_id is required")), true
	}
	summary, err := api.Summary(a.SessionID)
	if err != nil {
		return errPayload("get_summary", err), true
	}
	return summary, false
}

// --- wait_session (blocking observation, receipt-style result) ---

// waitPollInterval is the fleet polling cadence while waiting, matching the
// agent CLI's waitForPhase and Lua's pollSession. Package var so tests can
// shrink it.
var waitPollInterval = 500 * time.Millisecond

const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 600 * time.Second
)

// waitResult is wait_session's receipt-style structured result (spec Decision 5
// reconciliation): not an ActionReceipt — nothing was mutated — but the same
// target/observed-state vocabulary so a receipt and its reconciliation read
// alike.
type waitResult struct {
	Operation     string                 `json:"operation"`
	Target        receipt.Target         `json:"target"`
	Phase         string                 `json:"phase"`
	Outcome       string                 `json:"outcome"` // reached | timeout | vanished
	WaitedMs      int64                  `json:"waited_ms"`
	ObservedState *receipt.ObservedState `json:"observed_state,omitempty"`
	Error         string                 `json:"error,omitempty"`
}

// handleWaitSession blocks until the target session reaches the requested
// phase, the timeout elapses, or the session disappears from the fleet
// (useful for kill reconciliation). Timeout is a tool error so the model
// notices reconciliation failed; vanished is a legitimate observation.
func handleWaitSession(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID      string `json:"session_id"`
		Phase          string `json:"phase"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return errPayload("wait_session", fmt.Errorf("session_id and phase are required")), true
	}
	if a.Phase != "idle" && a.Phase != "working" && a.Phase != "cycle" {
		return errPayload("wait_session", fmt.Errorf("phase must be idle, working, or cycle; got %q", a.Phase)), true
	}
	timeout := defaultWaitTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
	}
	if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}

	start := time.Now()
	deadline := start.Add(timeout)
	result := waitResult{Operation: "wait_session", Phase: a.Phase, Target: receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}}
	sawWorking := false
	everSeen := false
	var lastObserved *receipt.ObservedState

	for {
		sessions, err := api.Sessions("")
		if err != nil {
			result.Outcome = "timeout"
			result.WaitedMs = time.Since(start).Milliseconds()
			result.Error = err.Error()
			return result, true
		}
		var found *agent.Session
		for i := range sessions {
			if sessions[i].SessionID == a.SessionID {
				found = &sessions[i]
				break
			}
		}
		switch {
		case found != nil:
			everSeen = true
			result.Target.PaneID = found.PaneID
			result.Target.DisplayName = found.DisplayName()
			lastObserved = &receipt.ObservedState{
				Status:    found.Status.String(),
				IsWaiting: found.IsWaiting,
				QueueLen:  len(found.QueuePending),
				Alive:     true,
			}
			if waitPhaseReached(a.Phase, found.Status, &sawWorking) {
				result.Outcome = "reached"
				result.WaitedMs = time.Since(start).Milliseconds()
				result.ObservedState = lastObserved
				return result, false
			}
		case everSeen:
			// The session was observed and is now gone — e.g. it was killed.
			result.Outcome = "vanished"
			result.WaitedMs = time.Since(start).Milliseconds()
			result.ObservedState = &receipt.ObservedState{Alive: false}
			return result, false
		}

		if !time.Now().Before(deadline) {
			result.Outcome = "timeout"
			result.WaitedMs = time.Since(start).Milliseconds()
			if !everSeen {
				result.Error = fmt.Sprintf("session %s was never observed in the fleet", a.SessionID)
				result.ObservedState = &receipt.ObservedState{Alive: false}
			} else {
				result.Error = fmt.Sprintf("session %s did not reach phase %s within %s", a.SessionID, a.Phase, timeout)
				result.ObservedState = lastObserved
			}
			return result, true
		}
		time.Sleep(waitPollInterval)
	}
}

// waitPhaseReached mirrors the agent CLI / Lua phase semantics exactly
// (idle=user-turn, working=agent-turn, cycle=working then idle). For "cycle"
// it records the first working observation in *sawWorking so a later idle
// counts as a completed round trip and a pre-work false-idle does not.
func waitPhaseReached(phase string, status agent.Status, sawWorking *bool) bool {
	switch phase {
	case "idle":
		return status == agent.StatusUserTurn
	case "working":
		return status == agent.StatusAgentTurn
	case "cycle":
		if status == agent.StatusAgentTurn {
			*sawWorking = true
		}
		return status == agent.StatusUserTurn && *sawWorking
	}
	return false
}

// --- side-effect handlers (return ActionReceipt) ---

func handleSendMessage(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" || a.Message == "" {
		return receipt.New("send_message", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("session_id and message are required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("send_message", targetOf(a.SessionID, s, ok))
	rcpt.Params = map[string]any{"message": a.Message}
	if !ok {
		return rcpt.Fail(fmt.Errorf("session not found: %s", a.SessionID)), true
	}
	if err := api.Send(a.SessionID, a.Message); err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeDelivered
	rcpt.ObservedState = observe(api, a.SessionID)
	return rcpt, false
}

func handleQueueMessage(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" || a.Message == "" {
		return receipt.New("queue_message", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("session_id and message are required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("queue_message", targetOf(a.SessionID, s, ok))
	rcpt.Params = map[string]any{"message": a.Message}
	if !ok {
		return rcpt.Fail(fmt.Errorf("session not found: %s", a.SessionID)), true
	}
	// Stamp the receipt's action id onto the queue item (W8): the delivery
	// signal and the turn it causes carry it, so action_reconciled watches can
	// anchor to this exact instruction.
	itemID, err := api.QueueMessage(s.PaneID, a.SessionID, a.Message, rcpt.ActionID)
	if err != nil {
		return rcpt.Fail(err), true
	}
	if itemID != "" {
		rcpt.Params["queue_item_id"] = itemID
	}
	rcpt.DeliveryOutcome = receipt.OutcomeQueued
	rcpt.ObservedState = observe(api, a.SessionID)
	return rcpt, false
}

func handleSpawnSession(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		CWD         string `json:"cwd"`
		Provider    string `json:"provider"`
		Message     string `json:"message"`
		TmuxSession string `json:"tmux_session"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.CWD == "" {
		return receipt.New("spawn_session", receipt.Target{}).Fail(fmt.Errorf("cwd is required")), true
	}
	provider := agent.ParseProviderID(a.Provider)
	rcpt := receipt.New("spawn_session", receipt.Target{ResolvedBy: receipt.ResolvedExplicit})
	rcpt.Params = map[string]any{"cwd": a.CWD, "provider": string(provider), "message": a.Message, "tmux_session": a.TmuxSession}
	// No splitFromPane: the daemon has no caller pane context; open a new window.
	result, err := api.SpawnProvider(provider, a.CWD, a.TmuxSession, a.Message, "")
	if err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.Target.SessionID = result.SessionID
	rcpt.Target.PaneID = result.PaneID
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.ObservedState = observe(api, result.SessionID)
	return rcpt, false
}

func handleKillSession(api daemonAPI, args json.RawMessage) (any, bool) {
	var a idArgs
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return receipt.New("kill_session", receipt.Target{}).Fail(fmt.Errorf("session_id is required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("kill_session", targetOf(a.SessionID, s, ok))
	if err := api.Kill(a.SessionID); err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.ObservedState = observe(api, a.SessionID) // expected Alive:false after a kill
	return rcpt, false
}

func handleSetTags(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string   `json:"session_id"`
		Tags      []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return receipt.New("set_tags", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("session_id is required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("set_tags", targetOf(a.SessionID, s, ok))
	rcpt.Params = map[string]any{"tags": a.Tags}
	if err := api.SetTags(a.SessionID, a.Tags); err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.ObservedState = observe(api, a.SessionID)
	return rcpt, false
}

func handleSetNote(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return receipt.New("set_note", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("session_id is required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("set_note", targetOf(a.SessionID, s, ok))
	rcpt.Params = map[string]any{"note": a.Note}
	if err := api.SetNote(a.SessionID, a.Note); err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.ObservedState = observe(api, a.SessionID)
	return rcpt, false
}

func handleLaterSession(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Kill      bool   `json:"kill"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return receipt.New("later_session", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("session_id is required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("later_session", targetOf(a.SessionID, s, ok))
	rcpt.Params = map[string]any{"kill": a.Kill}
	if !ok {
		return rcpt.Fail(fmt.Errorf("session not found: %s", a.SessionID)), true
	}
	var err error
	if a.Kill {
		err = api.LaterKill(s.PaneID, s.PID, a.SessionID, "")
	} else {
		err = api.Later(s.PaneID, a.SessionID, "")
	}
	if err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.ObservedState = observe(api, a.SessionID)
	return rcpt, false
}

func handleCommitSession(api daemonAPI, args json.RawMessage) (any, bool) {
	var a struct {
		SessionID string `json:"session_id"`
		Done      bool   `json:"done"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.SessionID == "" {
		return receipt.New("commit_session", receipt.Target{SessionID: a.SessionID, ResolvedBy: receipt.ResolvedExplicit}).
			Fail(fmt.Errorf("session_id is required")), true
	}
	s, ok := resolveSession(api, a.SessionID)
	rcpt := receipt.New("commit_session", targetOf(a.SessionID, s, ok))
	rcpt.Params = map[string]any{"done": a.Done}
	if !ok {
		return rcpt.Fail(fmt.Errorf("session not found: %s", a.SessionID)), true
	}
	var err error
	if a.Done {
		err = api.CommitAndDone(s.PaneID, a.SessionID, s.PID)
	} else {
		err = api.CommitOnly(s.PaneID, a.SessionID, s.PID)
	}
	if err != nil {
		return rcpt.Fail(err), true
	}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	rcpt.ObservedState = observe(api, a.SessionID)
	return rcpt, false
}

// --- helpers ---

// resolveSession looks a session up by id from the current fleet.
func resolveSession(api daemonAPI, id string) (agent.Session, bool) {
	sessions, err := api.Sessions("")
	if err != nil {
		return agent.Session{}, false
	}
	for _, s := range sessions {
		if s.SessionID == id {
			return s, true
		}
	}
	return agent.Session{}, false
}

// targetOf builds a receipt Target from a resolved (or unresolved) session.
func targetOf(id string, s agent.Session, ok bool) receipt.Target {
	t := receipt.Target{SessionID: id, ResolvedBy: receipt.ResolvedExplicit}
	if ok {
		t.PaneID = s.PaneID
		t.DisplayName = s.DisplayName()
	}
	return t
}

// observe re-fetches the target session after an operation and captures a compact
// snapshot for reconciliation (Decision 5). Returns Alive:false if the session is
// gone (e.g. after a kill).
func observe(api daemonAPI, id string) *receipt.ObservedState {
	s, ok := resolveSession(api, id)
	if !ok {
		return &receipt.ObservedState{Alive: false}
	}
	return &receipt.ObservedState{
		Status:    s.Status.String(),
		IsWaiting: s.IsWaiting,
		QueueLen:  len(s.QueuePending),
		Alive:     true,
	}
}

// errPayload is the JSON body returned for a read-only tool failure (isError=true).
func errPayload(op string, err error) map[string]any {
	return map[string]any{"error": err.Error(), "operation": op}
}
