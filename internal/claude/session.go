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
	switch str {
	case "working":
		*s = StatusWorking
	case "stopped", "done":
		*s = StatusDone
	case "deferred":
		*s = StatusDeferred
	default:
		*s = StatusDone
	}
	return nil
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
	Headline        string // brief one-liner from cached summary
	CustomTitle     string // user-set name via /rename in Claude Code
	PermissionMode  string // "plan", "bypassPermissions", etc. (empty = unknown)
	LastActionCommit bool  // last tool call was git commit
}
