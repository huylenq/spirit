package claude

import (
	"encoding/json"
	"time"
)

type Status int

const (
	StatusAgentTurn Status = iota
	StatusUserTurn
)

func (s Status) String() string {
	switch s {
	case StatusAgentTurn:
		return "agent-turn"
	case StatusUserTurn:
		return "user-turn"
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
	case "agent-turn", "working":
		return StatusAgentTurn
	case "user-turn", "idle", "stopped", "done", "later", "deferred":
		return StatusUserTurn
	default:
		return StatusUserTurn
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
	LastChanged     time.Time
	CreatedAt       time.Time // pane creation time (from tmux pane_created)
	IsPhantom       bool      // true when session has no live tmux pane (created from bookmark)
	LaterBookmarkID string    // links to the bookmark file (empty if not a Deferred session)
	SessionID       string
	FirstMessage    string // first user message in transcript (display name heuristic)
	LastUserMessage string
	// Display name priority: CustomTitle → Headline → FirstMessage → "(New session)"
	// CustomTitle: set by Claude Code's /rename (written to transcript as custom-title entry).
	//   The daemon sends /rename via tmux.SendKeys after synthesis, but this only works
	//   when the Claude Code session is idle at the prompt.
	// Headline: derived from the summary cache (always available after synthesis).
	//   Used as fallback when /rename hasn't been processed yet.
	Headline    string // brief one-liner from cached summary
	ProblemType string // bug, feature, refactoring, etc. from cached summary
	CustomTitle string // user-set name via /rename in Claude Code
	PermissionMode   string // "plan", "bypassPermissions", etc. (empty = unknown)
	LastActionCommit bool   // last tool call was git commit
	StopReason       string // from Stop hook reason field (cleared on next agent-turn)
	SkillName        string // slash-command skill invoked (e.g. "simplify"); cleared on next non-skill prompt
	IsWaiting        bool   // true when Notification(permission_prompt|elicitation_dialog)
	CompactCount     int    // number of PreCompact events fired
	CommitDonePending  bool   // daemon is waiting for commit-and-done to resolve
	SynthesizePending  bool   // daemon has in-flight synthesis for this pane
	QueuePending       []string // daemon-annotated: messages queued for delivery when Done (FIFO)
	AvatarAnimalIdx    int    // index into avatarAnimals slice
	AvatarColorIdx     int    // index into avatarColors slice
}

// DisplayName returns the session's display name using the standard priority:
// CustomTitle → Headline → FirstMessage. Returns "" if none are set.
func (s ClaudeSession) DisplayName() string {
	switch {
	case s.CustomTitle != "":
		return s.CustomTitle
	case s.Headline != "":
		return s.Headline
	case s.FirstMessage != "":
		return s.FirstMessage
	default:
		return ""
	}
}

// LaterBookmark is the persistent on-disk record for a bookmarked session.
type LaterBookmark struct {
	ID           string    `json:"id"`
	PaneID       string    `json:"paneID"`       // original pane (may be dead)
	Project      string    `json:"project"`
	CWD          string    `json:"cwd"`
	GitBranch    string    `json:"gitBranch"`
	Headline     string    `json:"headline"`
	ProblemType  string    `json:"problemType"`
	CustomTitle  string    `json:"customTitle"`
	FirstMessage string    `json:"firstMessage"`
	SessionID    string    `json:"sessionID"`
	CreatedAt    time.Time `json:"createdAt"`
}
