package daemon

import (
	"encoding/json"
	"time"

	"github.com/huylenq/spirit/internal/claude"
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
	ReqPing               = "ping"
	ReqNudge              = "nudge"
	ReqSubscribe          = "subscribe"
	ReqTranscript         = "transcript"
	ReqDiffStats          = "diffstats"
	ReqSummary            = "summary"
	ReqSynthesize         = "synthesize"
	ReqSynthesizeAll      = "synthesize_all"
	ReqHookEvents         = "hookevents"
	ReqPaneGeometry       = "panegeometry"
	ReqLater              = "later"
	ReqLaterKill          = "later_kill"
	ReqUnlater            = "unlater"
	ReqOpenLater          = "open_later"
	ReqRenameWindow       = "rename_window"
	ReqCommitOnly         = "commit_only"
	ReqCommitDone         = "commit_done"
	ReqCommitSimplifyDone = "commit_simplify_done"
	ReqCancelCommitDone   = "cancel_commit_done"
	ReqQueue              = "queue"
	ReqCancelQueueItem    = "cancel_queue_item"
	ReqRawTranscript      = "raw_transcript"
	ReqDiffHunks          = "diffhunks"
	ReqAllHookEffects     = "allhookeffects"

	ReqPendingPrompt          = "pending_prompt"
	ReqRegisterOrchestrator   = "register_orchestrator"
	ReqUnregisterOrchestrator = "unregister_orchestrator"
	ReqSessions               = "sessions"
	ReqSend                   = "send"
	ReqSpawn                  = "spawn"
	ReqKill                   = "kill"
	ReqDigest                 = "digest"
	ReqApplyTitle             = "apply_title"
	ReqSetTags                = "set_tags"
	ReqSetNote                = "set_note"

	ReqBacklogList   = "backlog_list"
	ReqBacklogCreate = "backlog_create"
	ReqBacklogUpdate = "backlog_update"
	ReqBacklogDelete = "backlog_delete"

	ReqCopilotChat           = "copilot_chat"
	ReqCopilotCancel         = "copilot_cancel"
	ReqCopilotStatus         = "copilot_status"
	ReqCopilotHistory        = "copilot_history"
	ReqCopilotClearHistory   = "copilot_clear_history"
	ReqCopilotTogglePreamble = "copilot_toggle_preamble"
)

// Response type constants.
const (
	RespPong          = "pong"
	RespSessions      = "sessions"
	RespResult        = "result"
	RespError         = "error"
	RespCopilotStream = "copilot_stream"
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

type RenameWindowData struct {
	SessionName string `json:"sessionName"`
	WindowIndex int    `json:"windowIndex"`
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

// --- Response data payloads ---

type SessionsData struct {
	Sessions []claude.ClaudeSession `json:"sessions"`
	Usage    *claude.UsageStats     `json:"usage,omitempty"`
}

type TranscriptData struct {
	Messages []string `json:"messages"`
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

type RenameResultData struct {
	Name string `json:"name"`
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
	CWD         string `json:"cwd"`
	TmuxSession string `json:"tmuxSession"`
	Message     string `json:"message,omitempty"`
}

type SpawnResultData struct {
	SessionID string `json:"sessionID"`
	PaneID    string `json:"paneID"`
}

type DigestData struct {
	Digest *claude.WorkspaceDigest `json:"digest"`
}

type SetTagsData struct {
	SessionID string   `json:"sessionID"`
	Tags      []string `json:"tags"`
}

type SetNoteData struct {
	SessionID string `json:"sessionID"`
	Note      string `json:"note"`
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

// --- Copilot data payloads ---

type CopilotChatData struct {
	Message string `json:"message"`
}

type CopilotStatusData struct {
	Ready       bool `json:"ready"`
	EventsToday int  `json:"eventsToday"`
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

// CopilotStreamData wraps a stream message for the subscribe connection.
type CopilotStreamData struct {
	Type    string `json:"type"` // "text_delta", "tool_call", "tool_update", "done", "error"
	Content string `json:"content"`
	ToolID  string `json:"tool_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// --- Helpers ---

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
// binary's location and falls back to ~/.cache/spirit/daemon.sock.

// IdleTimeout is how long the daemon stays alive with zero clients.
const IdleTimeout = 10 * time.Minute
