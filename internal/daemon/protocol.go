package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ledger"
	"github.com/huylenq/spirit/internal/tmux"
)

// Request/Response are newline-delimited JSON over Unix socket.

type Request struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type Response struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Version uint64          `json:"version,omitempty"`
	Deduped bool            `json:"deduped,omitempty"`
}

// Request type constants.
const (
	ReqPing             = "ping"
	ReqNudge            = "nudge"
	ReqSubscribe        = "subscribe"
	ReqTranscript       = "transcript"
	ReqDiffStats        = "diffstats"
	ReqSummary          = "summary"
	ReqSynthesize       = "synthesize"
	ReqSynthesizeAll    = "synthesize_all"
	ReqHookEvents       = "hookevents"
	ReqPaneGeometry     = "panegeometry"
	ReqLater            = "later"
	ReqLaterKill        = "later_kill"
	ReqUnlater          = "unlater"
	ReqOpenLater        = "open_later"
	ReqRenameAllWindows = "rename_all_windows"
	ReqCommitOnly       = "commit_only"
	ReqCommitDone       = "commit_done"
	ReqQueueCommitDone  = "queue_commit_done"
	ReqCancelCommitDone = "cancel_commit_done"
	ReqQueue            = "queue"
	ReqCancelQueueItem  = "cancel_queue_item"
	ReqRawTranscript    = "raw_transcript"
	ReqDiffHunks        = "diffhunks"
	ReqAllHookEffects   = "allhookeffects"

	ReqPendingPrompt          = "pending_prompt"
	ReqRegisterOrchestrator   = "register_orchestrator"
	ReqUnregisterOrchestrator = "unregister_orchestrator"
	ReqSessions               = "sessions"
	ReqSend                   = "send"
	ReqRelay                  = "relay"
	ReqSpawn                  = "spawn"
	ReqKill                   = "kill"
	ReqPulse                  = "pulse"
	ReqApplyTitle             = "apply_title"
	ReqSetTags                = "set_tags"
	ReqSetNote                = "set_note"

	ReqActionReport = "action_report"

	ReqWatchCreate      = "watch_create"
	ReqWatchList        = "watch_list"
	ReqWatchCancel      = "watch_cancel"
	ReqAttentionList    = "attention_list"
	ReqAttentionResolve = "attention_resolve"

	ReqBacklogList   = "backlog_list"
	ReqBacklogCreate = "backlog_create"
	ReqBacklogUpdate = "backlog_update"
	ReqBacklogDelete = "backlog_delete"

	ReqCopilotChat             = "copilot_chat"
	ReqCopilotCancel           = "copilot_cancel"
	ReqCopilotStatus           = "copilot_status"
	ReqCopilotHistory          = "copilot_history"
	ReqCopilotClearHistory     = "copilot_clear_history"
	ReqCopilotTogglePreamble   = "copilot_toggle_preamble"
	ReqCopilotSetModel         = "copilot_set_model"
	ReqCopilotSetMode          = "copilot_set_mode"
	ReqCopilotPermissionAnswer = "copilot_permission_answer"
)

// Response type constants.
const (
	RespPong          = "pong"
	RespSessions      = "sessions"
	RespResult        = "result"
	RespError         = "error"
	RespCopilotStream = "copilot_stream"
	RespCopilotSnapshot = "copilot_snapshot"
)

// --- Request data payloads ---

type SessionIDData struct {
	SessionID string `json:"sessionID"`
}

type PaneSessionData struct {
	PaneID    string `json:"paneID"`
	SessionID string `json:"sessionID"`
}

type SkipPaneData struct {
	SkipPaneID string `json:"skipPaneID"`
}

type PaneData struct {
	PaneID string `json:"paneID"`
}

type SessionNameData struct {
	SessionName string `json:"sessionName"`
}

type LaterData struct {
	PaneID    string `json:"paneID"`
	SessionID string `json:"sessionID"`
	Wait      string `json:"wait,omitempty"` // optional duration (e.g. "5m", "1h"); empty = indefinite
}

type LaterKillData struct {
	PaneID    string `json:"paneID"`
	PID       int    `json:"pid"`
	SessionID string `json:"sessionID"`
	Wait      string `json:"wait,omitempty"` // optional duration (e.g. "5m", "1h"); empty = indefinite
}

type UnlaterData struct {
	LaterID string `json:"laterID"`
}

type OpenLaterData struct {
	LaterID     string `json:"laterID"`
	CWD         string `json:"cwd"`
	TmuxSession string `json:"tmuxSession"`
}

type RenameAllResultData struct {
	Renamed int      `json:"renamed"`
	Killed  int      `json:"killed"`
	Errors  []string `json:"errors,omitempty"`
}

type NudgeData struct {
	PaneID          string `json:"paneID"`
	SessionID       string `json:"sessionID,omitempty"`
	Status          string `json:"status"`
	LastUserMessage string `json:"lastUserMessage,omitempty"`
	StopReason      string `json:"stopReason,omitempty"`
	PermissionMode  string `json:"permissionMode,omitempty"`
	IsWaiting       *bool  `json:"isWaiting,omitempty"`
	IsGitCommit     *bool  `json:"isGitCommit,omitempty"`
	IsFileEdit      *bool  `json:"isFileEdit,omitempty"`
	SkillName       string `json:"skillName,omitempty"`
	SkillSet        bool   `json:"skillSet,omitempty"`
	Compacted       bool   `json:"compacted,omitempty"`
	Remove          bool   `json:"remove,omitempty"`
}

type CommitDoneData struct {
	PaneID    string `json:"paneID"`
	SessionID string `json:"sessionID"`
	PID       int    `json:"pid"`
}

type QueueData struct {
	PaneID    string `json:"paneID"`
	SessionID string `json:"sessionID"`
	Message   string `json:"message"`
	// ActionID, when set, links the queue item to the batch/MCP action that
	// enqueued it (W8): the delivery signal and the turn it causes carry it.
	ActionID string `json:"actionID,omitempty"`
}

// QueueResultData returns the durable id of the freshly enqueued item (W8).
type QueueResultData struct {
	ItemID string `json:"itemID"`
}

type CancelQueueItemData struct {
	SessionID string `json:"sessionID"`
	Index     int    `json:"index"`
}

type PendingPromptData struct {
	PaneID   string `json:"paneID"`
	Prompt   string `json:"prompt"`
	PlanMode bool   `json:"planMode,omitempty"`
}

type RelayData struct {
	PaneID     string           `json:"paneID"`
	Message    string           `json:"message"`
	Capability agent.Capability `json:"capability,omitempty"`
}

// --- Response data payloads ---

type SessionsData struct {
	Sessions []agent.Session    `json:"sessions"`
	Usage    *claude.UsageStats `json:"usage,omitempty"`
}

type TranscriptData struct {
	Messages []string           `json:"messages"`
	Turn     claude.CurrentTurn `json:"turn"`
}

type DiffStatsData struct {
	Stats map[string]claude.FileDiffStat `json:"stats"`
}

type SummaryData struct {
	Summary   *claude.SessionSummary `json:"summary"`
	FromCache bool                   `json:"fromCache"`
}

type HookEventsData struct {
	Events []claude.HookEvent `json:"events"`
}

type TranscriptEntriesData struct {
	Entries []claude.TranscriptEntry `json:"entries"`
}

type DiffHunksData struct {
	Hunks []claude.FileDiffHunk `json:"hunks"`
}

type AllHookEffectsData struct {
	Effects []claude.GlobalHookEffect `json:"effects"`
}

type PaneGeometryData struct {
	Panes []tmux.PaneGeometry `json:"panes"`
}

type SynthesizeResultData struct {
	PaneID    string                 `json:"paneID"`
	Summary   *claude.SessionSummary `json:"summary"`
	FromCache bool                   `json:"fromCache"`
}

type SynthesizeAllResultData struct {
	Results []SynthesizeResultData `json:"results"`
}

// --- Eval API request/response data ---

type SessionsFilterData struct {
	Status string `json:"status,omitempty"`
}

type SendData struct {
	SessionID string `json:"sessionID"`
	Message   string `json:"message"`
}

type SpawnData struct {
	Provider    agent.ProviderID `json:"provider,omitempty"`
	CWD         string           `json:"cwd"`
	TmuxSession string           `json:"tmuxSession"`
	Message     string           `json:"message,omitempty"`
	// SplitFromPane, if set, makes spawn split a new pane in the same window
	// as the given pane (e.g. "%145") instead of opening a new tmux window.
	// Takes precedence over TmuxSession when both are set.
	SplitFromPane string `json:"splitFromPane,omitempty"`
}

type SpawnResultData struct {
	SessionID string `json:"sessionID"`
	PaneID    string `json:"paneID"`
}

type PulseData struct {
	Pulse *claude.Pulse `json:"pulse"`
}

type SetTagsData struct {
	SessionID string   `json:"sessionID"`
	Tags      []string `json:"tags"`
}

type SetNoteData struct {
	SessionID string `json:"sessionID"`
	Note      string `json:"note"`
}

// ActionReportData reports the outcome of a side-effect operation executed
// through an out-of-process surface (the `spirit mcp` server) back to the
// daemon, so failed ActionReceipts become action_failed signals in the
// perception ledger. Successful operations are currently not reported — the
// receipt returned to the caller is their record.
type ActionReportData struct {
	ActionID  string `json:"actionID"`
	Operation string `json:"operation"`
	SessionID string `json:"sessionID,omitempty"`
	Project   string `json:"project,omitempty"`
	Error     string `json:"error,omitempty"`
}

// --- Watch / attention data payloads (W7) ---

// WatchCreateData creates one reactive watch (spec Decision 10). Zero limits
// get daemon defaults (24h expiry, 60s cooldown, 20 firings); validity (expiry
// + rate limit present) is enforced by the ledger.
type WatchCreateData struct {
	SessionID          string `json:"sessionID,omitempty"`
	Project            string `json:"project,omitempty"`
	Condition          string `json:"condition"`
	Response           string `json:"response"`
	ExpiresInMinutes   int    `json:"expiresInMinutes,omitempty"`
	CooldownSeconds    int    `json:"cooldownSeconds,omitempty"`
	MaxFirings         int    `json:"maxFirings,omitempty"`
	LLMBudget          int    `json:"llmBudget,omitempty"`
	CreatedBy          string `json:"createdBy,omitempty"`
	CreatedByRequestID string `json:"createdByRequestID,omitempty"`
}

type WatchIDData struct {
	WatchID string `json:"watchID"`
}

type AttentionResolveData struct {
	ItemID     string `json:"itemID"`
	Resolution string `json:"resolution,omitempty"`
}

// WatchResultData returns one watch record.
type WatchResultData struct {
	Watch ledger.Watch `json:"watch"`
}

// AttentionListData is the attention inbox payload: unresolved items (with
// audit + recommendation) and every known watch.
type AttentionListData struct {
	Items   []ledger.AttentionItem `json:"items"`
	Watches []ledger.Watch         `json:"watches"`
	// Descriptions maps item id → one-line human digest derived from the
	// latest linked signal, so surfaces don't re-derive evidence.
	Descriptions map[string]string `json:"descriptions,omitempty"`
}

type BacklogListData struct {
	CWD string `json:"cwd"`
}

type BacklogCreateData struct {
	CWD  string `json:"cwd"`
	Body string `json:"body"`
}

type BacklogUpdateData struct {
	CWD  string `json:"cwd"`
	ID   string `json:"id"`
	Body string `json:"body"`
}

type BacklogDeleteData struct {
	CWD string `json:"cwd"`
	ID  string `json:"id"`
}

type BacklogListResultData struct {
	Backlogs []claude.Backlog `json:"backlogs"`
}

type BacklogItemResultData struct {
	Backlog claude.Backlog `json:"backlog"`
}

// SubscribeData carries the originating client's stable identity so the daemon
// can scope correlated copilot stream events back to the subscriber that owns
// the in-flight turn (Decision 1/6). Empty ClientID (older clients) falls back to
// broadcast delivery.
type SubscribeData struct {
	ClientID string `json:"clientId,omitempty"`
}

// --- Copilot data payloads ---

// CopilotChatData is one Lulu prompt. Beyond the message it carries request
// identity and the request-scoped selection captured at the originating client
// (spec Decisions 1, 2): RequestID correlates the streamed turn, ClientID scopes
// delivery, and Scope grounds "review this"/"tell it to fix it" in a specific
// session rather than a title-match guess.
type CopilotChatData struct {
	Message   string        `json:"message"`
	RequestID string        `json:"requestId,omitempty"`
	ClientID  string        `json:"clientId,omitempty"`
	Scope     *CopilotScope `json:"scope,omitempty"`
}

// CopilotScope is the local UI attention state at send time. It is request-scoped
// (never persisted daemon-side) so one TUI client can never overwrite another's
// selection. The daemon validates SelectedSessionID against current fleet truth
// before building the prompt.
type CopilotScope struct {
	SelectedSessionID string               `json:"selectedSessionId,omitempty"`
	Selected          *CopilotScopeSession `json:"selected,omitempty"`
	ActiveProject     string               `json:"activeProject,omitempty"`
	ActiveLane        string               `json:"activeLane,omitempty"`
	ActiveView        string               `json:"activeView,omitempty"`
	VisibleSessionIDs []string             `json:"visibleSessionIds,omitempty"`
}

// CopilotScopeSession is the selected session's snapshot as the originating
// client saw it — a copy, not a lookup-by-name. The daemon prefers its own fresh
// copy when the id still resolves, but the client snapshot is what proves the
// referent even if the fleet shifts between render and prompt.
type CopilotScopeSession struct {
	SessionID       string   `json:"sessionId"`
	Provider        string   `json:"provider,omitempty"`
	Name            string   `json:"name,omitempty"`
	Project         string   `json:"project,omitempty"`
	GitBranch       string   `json:"gitBranch,omitempty"`
	CWD             string   `json:"cwd,omitempty"`
	Model           string   `json:"model,omitempty"`
	Status          string   `json:"status,omitempty"`
	Lane            string   `json:"lane,omitempty"`
	Note            string   `json:"note,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	IsWaiting       bool     `json:"isWaiting,omitempty"`
	HasOverlap      bool     `json:"hasOverlap,omitempty"`
	IsWorktree      bool     `json:"isWorktree,omitempty"`
	WorktreeName    string   `json:"worktreeName,omitempty"`
	LastUserMessage string   `json:"lastUserMessage,omitempty"`
}

type CopilotSetModelData struct {
	ModelID string `json:"modelId"`
}

// CopilotSetModeData switches the Hermes ACP session mode — the coarse autonomy
// ceiling (Decision 9). ModeID is one of the advertised mode ids
// (default / accept_edits / dont_ask).
type CopilotSetModeData struct {
	ModeID string `json:"modeId"`
}

// CopilotModeInfo is one advertised Hermes session mode. Modes are the coarse
// autonomy ceiling that governs Hermes-native edit approvals: `default` asks per
// edit, `accept_edits` auto-allows workspace/tmp edits, `dont_ask` auto-allows all
// non-sensitive edits (sensitive paths always prompt). See spec Decision 9.
type CopilotModeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CopilotModeState mirrors Hermes's SessionModeState (session/new + session/load
// responses, and current_mode_update stream events).
type CopilotModeState struct {
	AvailableModes []CopilotModeInfo `json:"availableModes,omitempty"`
	CurrentModeID  string            `json:"currentModeId,omitempty"`
}

// CopilotPermissionOption is one answer Hermes offers for a session/request_permission.
// The daemon assigns a stable keyboard accelerator (Key) so the TUI renders a
// consistent y/a/n/N affordance regardless of which subset of options Hermes sent.
// Kind is Hermes's hint (allow_once/allow_always/reject_once/reject_always); OptionID
// is the stable discriminator that must be echoed back verbatim (allow_session ships
// with kind allow_always, so the id — not the kind — is authoritative).
type CopilotPermissionOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Key      string `json:"key"`
}

// CopilotPermissionDiff is one file edit rendered in an edit-approval request,
// carrying Hermes's real diff payload (a diff content block).
type CopilotPermissionDiff struct {
	Path    string `json:"path"`
	OldText string `json:"oldText,omitempty"`
	NewText string `json:"newText"`
}

// CopilotPermissionRequest is the typed session/request_permission payload the
// daemon forwards to the originating TUI client for a human decision (Decision 5/6).
// It carries the tool kind and title, the real diff for edits, the command for
// dangerous executes, the offered options with assigned keys, a sensitive-path flag,
// and the absolute auto-deny deadline so the UI can show a countdown.
type CopilotPermissionRequest struct {
	PermissionID string                    `json:"permissionId"`
	ToolCallID   string                    `json:"toolCallId,omitempty"`
	Title        string                    `json:"title"`
	Kind         string                    `json:"kind"`
	Command      string                    `json:"command,omitempty"`
	Diffs        []CopilotPermissionDiff   `json:"diffs,omitempty"`
	Options      []CopilotPermissionOption `json:"options"`
	Sensitive    bool                      `json:"sensitive,omitempty"`
	SensitiveHit string                    `json:"sensitiveHit,omitempty"`
	DeadlineUnix int64                     `json:"deadlineUnix,omitempty"`
}

// CopilotPermissionAnswerData carries the human's decision back to the daemon,
// which resolves the pending ACP request (Decision 6). OptionID is the chosen
// option id, or "" to refuse. PermissionID correlates to the pending request; a
// stale/duplicate answer (already resolved) is an informative no-op.
type CopilotPermissionAnswerData struct {
	PermissionID string `json:"permissionId"`
	OptionID     string `json:"optionId,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
}

type CopilotModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CopilotModelState struct {
	AvailableModels []CopilotModelInfo `json:"availableModels,omitempty"`
	CurrentModelID  string             `json:"currentModelId,omitempty"`
}

type CopilotStatusData struct {
	Ready       bool              `json:"ready"`
	EventsToday int               `json:"eventsToday"`
	SessionID   string            `json:"sessionId,omitempty"`
	Models      CopilotModelState `json:"models,omitempty"`
	Modes       CopilotModeState  `json:"modes,omitempty"`
}

// CopilotHistoryMsg is a persisted copilot conversation turn (user or copilot role).
type CopilotHistoryMsg struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

// CopilotHistoryData is the response payload for ReqCopilotHistory.
type CopilotHistoryData struct {
	Messages []CopilotHistoryMsg `json:"messages"`
}

// CopilotSnapshotData is the daemon-authoritative copilot state sent when a
// TUI subscribes. It includes the persisted history plus any response that is
// still being streamed so a reconnect can hydrate without waiting for a new
// prompt.
type CopilotSnapshotData struct {
	History         []CopilotHistoryMsg `json:"history"`
	SessionID       string              `json:"sessionId,omitempty"`
	ActivePrompt    string              `json:"activePrompt,omitempty"`
	ActiveOutput    string              `json:"activeOutput,omitempty"`
	ActiveRequestID string              `json:"activeRequestId,omitempty"`
	ActiveClientID  string              `json:"activeClientId,omitempty"`
	Streaming       bool                `json:"streaming"`
}

// CopilotStreamData wraps a stream message for the subscribe connection. Every
// event carries the originating RequestID and ClientID (Decision 6): the daemon
// routes delivery to the originating client, and the client drops late chunks
// whose RequestID no longer matches its in-flight turn (belt to the daemon's
// turn-epoch suspenders).
type CopilotStreamData struct {
	// Type is one of: text_delta, thought, tool_call, tool_update, plan, usage,
	// session, mode, permission_request, permission_resolved, done, error.
	Type      string `json:"type"`
	Content   string `json:"content"`
	ToolID    string `json:"tool_id,omitempty"`
	Status    string `json:"status,omitempty"`
	Kind      string `json:"kind,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	// Permission carries the typed payload for a permission_request chunk, or the
	// resolved id/title for a permission_resolved chunk (Decision 6).
	Permission *CopilotPermissionRequest `json:"permission,omitempty"`
}

// --- Helpers ---

// NewCorrelationID returns a random 128-bit hex id for request/client
// correlation. Used for both per-prompt request ids and per-client stable ids.
func NewCorrelationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is catastrophic; fail loud rather than emit a
		// predictable id that would silently break correlation.
		panic("daemon: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func marshalData(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func errResponse(msg string) Response {
	return Response{Type: RespError, Error: msg}
}

func resultResponse(data any) Response {
	return Response{Type: RespResult, Data: marshalData(data)}
}

// DaemonInfo holds paths for the daemon socket and PID file.
type DaemonInfo struct {
	SocketPath string
	PIDPath    string
}

// DefaultDaemonInfo is defined in workdir.go — it auto-detects from the
// binary's location and falls back to ~/.spirit/daemon.sock.

// IdleTimeout is how long the daemon stays alive with zero clients.
const IdleTimeout = 10 * time.Minute
