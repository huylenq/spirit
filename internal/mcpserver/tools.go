package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
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
	SpawnProvider(provider agent.ProviderID, cwd, tmuxSession, message, splitFromPane string) (daemon.SpawnResultData, error)
	Kill(sessionID string) error
	SetTags(sessionID string, tags []string) error
	SetNote(sessionID, note string) error
	Later(paneID, sessionID, wait string) error
	LaterKill(paneID string, pid int, sessionID, wait string) error
	CommitOnly(paneID, sessionID string, pid int) error
	CommitAndDone(paneID, sessionID string, pid int) error
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

		// --- Side effects (return an ActionReceipt) ---
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
	if err := api.Queue(s.PaneID, a.SessionID, a.Message); err != nil {
		return rcpt.Fail(err), true
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
