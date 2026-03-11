package daemon

import (
	"encoding/json"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
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
	ReqPing         = "ping"
	ReqNudge        = "nudge"
	ReqSubscribe    = "subscribe"
	ReqTranscript   = "transcript"
	ReqDiffStats    = "diffstats"
	ReqSummary      = "summary"
	ReqSynthesize    = "synthesize"
	ReqSynthesizeAll = "synthesize_all"
	ReqHookEvents   = "hookevents"
	ReqPaneGeometry = "panegeometry"
	ReqLater     = "later"
	ReqLaterKill = "later_kill"
	ReqUnlater   = "unlater"
	ReqOpenLater = "open_later"
	ReqRenameWindow    = "rename_window"
	ReqCommitOnly       = "commit_only"
	ReqCommitDone       = "commit_done"
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
	ReqSpawn                  = "spawn"
	ReqKill                   = "kill"
	ReqDigest                 = "digest"
)

// Response type constants.
const (
	RespPong     = "pong"
	RespSessions = "sessions"
	RespResult   = "result"
	RespError    = "error"
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
}

type LaterKillData struct {
	PaneID    string `json:"paneID"`
	PID       int    `json:"pid"`
	SessionID string `json:"sessionID"`
}

type UnlaterData struct {
	BookmarkID string `json:"bookmarkID"`
}

type OpenLaterData struct {
	BookmarkID  string `json:"bookmarkID"`
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

func DefaultDaemonInfo() DaemonInfo {
	dir := claude.StatusDir()
	return DaemonInfo{
		SocketPath: dir + "/daemon.sock",
		PIDPath:    dir + "/daemon.pid",
	}
}

// IdleTimeout is how long the daemon stays alive with zero clients.
const IdleTimeout = 10 * time.Minute
