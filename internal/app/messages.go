package app

import (
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/tmux"
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

// TranscriptReadyMsg is sent when user messages are extracted from a session transcript.
type TranscriptReadyMsg struct {
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
