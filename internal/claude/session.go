package claude

import (
	"encoding/json"
	"time"
)

type Status int

const (
	StatusWorking  Status = iota
	StatusDone
	StatusDeferred
)

func (s Status) String() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusDone:
		return "stopped"
	case StatusDeferred:
		return "deferred"
	default:
		return "unknown"
	}
}

func (s Status) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *Status) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	*s = ParseStatus(str)
	return nil
}

func ParseStatus(str string) Status {
	switch str {
	case "working":
		return StatusWorking
	case "stopped", "done":
		return StatusDone
	case "deferred":
		return StatusDeferred
	default:
		return StatusDone
	}
}

type Location struct {
	IsSSH bool
	Host  string
}

type ClaudeSession struct {
	PaneID      string
	Status      Status
	Project     string // basename of cwd
	CWD         string
	GitBranch   string
	TmuxSession string
	TmuxWindow  int
	TmuxPane    int
	PID         int
	Location    Location
	LastChanged time.Time
	DeferUntil  time.Time
	SessionID       string
	FirstMessage    string // first user message in transcript (display name heuristic)
	LastUserMessage string
	// Display name priority: CustomTitle → Headline → FirstMessage → "(New session)"
	// CustomTitle: set by Claude Code's /rename (written to transcript as custom-title entry).
	//   The daemon sends /rename via tmux.SendKeys after summarization, but this only works
	//   when the Claude Code session is idle at the prompt.
	// Headline: derived from the summary cache (always available after summarization).
	//   Used as fallback when /rename hasn't been processed yet.
	Headline    string // brief one-liner from cached summary
	CustomTitle string // user-set name via /rename in Claude Code
	PermissionMode   string // "plan", "bypassPermissions", etc. (empty = unknown)
	LastActionCommit bool   // last tool call was git commit
	CommitDonePending  bool // daemon is waiting for commit-and-done to resolve
	SummarizePending   bool // daemon has in-flight summarization for this pane
}
