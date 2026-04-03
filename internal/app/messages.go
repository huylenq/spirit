package app

import (
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/scripting"
	"github.com/huylenq/spirit/internal/tmux"
	"github.com/huylenq/spirit/internal/ui"
)

// SessionsRefreshedMsg is sent when session discovery completes (pushed by daemon).
type SessionsRefreshedMsg struct {
	Sessions []claude.ClaudeSession
	Usage    *claude.UsageStats
	Err      error
}

// PreviewReadyMsg is sent when pane content capture completes.
type PreviewReadyMsg struct {
	PaneID  string
	Content string
	Err     error
}

// ChatOutlineReadyMsg is sent when user messages are extracted from a session transcript.
type ChatOutlineReadyMsg struct {
	PaneID   string
	Messages []string
}

// HooksReadyMsg is sent when hook events are loaded for a pane.
type HooksReadyMsg struct {
	PaneID string
	Events []claude.HookEvent
}

// RawTranscriptReadyMsg is sent when transcript entries are loaded.
type RawTranscriptReadyMsg struct {
	PaneID  string
	Entries []claude.TranscriptEntry
}

// DiffHunksReadyMsg is sent when file diff hunks are loaded for a session.
type DiffHunksReadyMsg struct {
	PaneID string
	CWD    string
	Hunks  []claude.FileDiffHunk
}

// DiffStatsReadyMsg is sent when file diff stats are extracted from a transcript.
type DiffStatsReadyMsg struct {
	PaneID    string
	SessionID string
	Stats     map[string]claude.FileDiffStat
}

// SummaryReadyMsg is sent when a conversation summary completes.
type SummaryReadyMsg struct {
	PaneID        string
	Summary       *claude.SessionSummary
	Err           error
	FromCache     bool
	UserRequested bool // true when triggered by 's'/'S' key, false for passive loads
}

// SynthesizeAllResult holds one result from batch synthesis.
type SynthesizeAllResult struct {
	PaneID    string
	Summary   *claude.SessionSummary
	FromCache bool
}

// SynthesizeAllReadyMsg is sent when batch synthesis completes.
type SynthesizeAllReadyMsg struct {
	Results []SynthesizeAllResult
	Err     error
}

// GlobalEffectsReadyMsg is sent when global hook effects are loaded.
type GlobalEffectsReadyMsg struct {
	Effects []claude.GlobalHookEffect
}

// ClearFlashMsg auto-dismisses the error flash overlay.
type ClearFlashMsg struct{}

// ClearToastMsg pops the oldest entry from the toast queue after its TTL expires.
type ClearToastMsg struct{}

// MinimapReadyMsg is sent when pane geometry is loaded for the minimap.
type MinimapReadyMsg struct {
	SessionName string
	Panes       []tmux.PaneGeometry
}

// WindowRenameMsg is sent when Haiku finishes generating a window name.
type WindowRenameMsg struct {
	Name string
	Err  error
}

// OriginalPaneCapturedMsg is sent when the initial tmux pane state is captured at startup.
type OriginalPaneCapturedMsg struct {
	Session string
	Window  int
	Pane    int
	PaneID  string
	Err     error
}

// NewSessionCreatedMsg is sent when a new tmux window+session is spawned via "a".
type NewSessionCreatedMsg struct {
	PaneID string
}

// pathValidatedMsg is sent when the async path validation for "A" succeeds.
type pathValidatedMsg struct {
	cwd     string
	project string
}

// backlogWrittenMsg signals a successful backlog write/delete.
// Triggers re-discovery so the sidebar reflects the change.
type backlogWrittenMsg struct {
	flash string // optional success flash (empty = no flash from this msg)
}

// BacklogsRefreshedMsg is sent when backlog discovery completes.
type BacklogsRefreshedMsg struct {
	Backlogs []claude.Backlog
}

// ApplyTitleReadyMsg is sent when the apply-title RPC completes.
type ApplyTitleReadyMsg struct {
	Err error
}

// PaneKilledMsg is sent after attempting to kill a session and close its pane.
type PaneKilledMsg struct {
	Err error
}

// DaemonDisconnectedMsg is sent when the daemon connection drops.
type DaemonDisconnectedMsg struct {
	Err error
}

// DaemonReconnectedMsg is sent when reconnection to the daemon succeeds.
type DaemonReconnectedMsg struct {
	Client *daemon.Client
}

// MacroEditorExitedMsg is sent when the external $EDITOR process for a macro file exits.
type MacroEditorExitedMsg struct{}

// CopilotStreamChunkMsg delivers a streaming chunk from the copilot backend.
type CopilotStreamChunkMsg struct {
	Msg ui.CopilotStreamMsg
}

// CopilotHistoryReadyMsg delivers the restored copilot conversation history on TUI startup.
type CopilotHistoryReadyMsg struct {
	Messages []daemon.CopilotHistoryMsg
}

// LuaEvalDoneMsg is sent when an async Lua eval completes.
type LuaEvalDoneMsg struct {
	Result string
	Msgs   scripting.Msgs // flash/toast messages emitted during script execution
	Err    error
}
