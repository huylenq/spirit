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
}

// Request type constants.
const (
	ReqPing         = "ping"
	ReqNudge        = "nudge"
	ReqSubscribe    = "subscribe"
	ReqTranscript   = "transcript"
	ReqDiffStats    = "diffstats"
	ReqSummary      = "summary"
	ReqSummarize    = "summarize"
	ReqSummarizeAll = "summarize_all"
	ReqHookEvents   = "hookevents"
	ReqPaneGeometry = "panegeometry"
	ReqDefer        = "defer"
	ReqUndefer      = "undefer"
	ReqRenameWindow    = "rename_window"
	ReqCommitDone      = "commit_done"
	ReqCancelCommitDone = "cancel_commit_done"
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

type DeferData struct {
	PaneID  string `json:"paneID"`
	Minutes int    `json:"minutes"`
}

type RenameWindowData struct {
	SessionName string `json:"sessionName"`
	WindowIndex int    `json:"windowIndex"`
}

type NudgeData struct {
	PaneID          string `json:"paneID"`
	Status          string `json:"status"`
	LastUserMessage string `json:"lastUserMessage,omitempty"`
	SentAt          int64  `json:"sentAt,omitempty"` // UnixMilli timestamp for latency measurement
}

type CommitDoneData struct {
	PaneID string `json:"paneID"`
	PID    int    `json:"pid"`
}

// --- Response data payloads ---

type SessionsData struct {
	Sessions []claude.ClaudeSession `json:"sessions"`
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

type PaneGeometryData struct {
	Panes []tmux.PaneGeometry `json:"panes"`
}

type RenameResultData struct {
	Name string `json:"name"`
}

type SummarizeResultData struct {
	PaneID    string                 `json:"paneID"`
	Summary   *claude.SessionSummary `json:"summary"`
	FromCache bool                   `json:"fromCache"`
}

type SummarizeAllResultData struct {
	Results []SummarizeResultData `json:"results"`
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
