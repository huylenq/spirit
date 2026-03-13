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
	PaneID          string
	Status          Status
	Project         string // basename of cwd
	CWD             string
	GitBranch       string
	TmuxSession     string
	TmuxWindow      int
	TmuxPane        int
	PID             int
	Location        Location
	LastChanged     time.Time
	CreatedAt       time.Time // pane creation time (from tmux pane_created)
	IsPhantom       bool      // true when session has no live tmux pane (created from bookmark)
	LaterBookmarkID string    // links to the bookmark file (empty if not a Deferred session)
	SessionID       string
	FirstMessage         string // first user message in transcript (display name heuristic)
	LastUserMessage      string
	LastAssistantMessage string   // last assistant text response
	Insights             []string // all ★ Insight blocks (oldest first)
	// Display name priority: CustomTitle → SynthesizedTitle → FirstMessage → "(New session)"
	// CustomTitle: set by Claude Code's /rename (written to transcript as custom-title entry).
	//   The daemon sends /rename via tmux.SendKeys after synthesis, but this only works
	//   when the Claude Code session is idle at the prompt.
	// SynthesizedTitle: AI-generated one-liner (always available after synthesis).
	//   Used as fallback when /rename hasn't been processed yet.
	SynthesizedTitle        string   // AI-generated title from cached summary
	TitleDrift              bool     // SynthesizedTitle differs from last applied /rename
	ProblemType             string   // bug, feature, refactoring, etc. from cached summary
	CustomTitle             string   // user-set name via /rename in Claude Code
	PermissionMode          string   // "plan", "bypassPermissions", etc. (empty = unknown)
	LastActionCommit        bool     // last tool call was git commit
	StopReason              string   // from Stop hook reason field (cleared on next agent-turn)
	SkillName               string   // slash-command skill invoked (e.g. "simplify"); cleared on next non-skill prompt
	IsWaiting               bool     // true when Notification(permission_prompt|elicitation_dialog)
	CompactCount            int      // number of PreCompact events fired
	CommitDonePending       bool     // daemon is waiting for commit-and-done to resolve
	SynthesizePending       bool     // daemon has in-flight synthesis for this pane
	HasOverlap              bool     // 2+ sessions editing the same file
	QueuePending            []string // daemon-annotated: messages queued for delivery when Done (FIFO)
	AvatarAnimalIdx         int      // index into avatarAnimals slice
	AvatarColorIdx          int      // index into avatarColors slice
	IsWorktree              bool     // session runs in a Claude Code worktree
	WorktreeName            string   // e.g. "ember-cat"
	WorktreeRootProjectPath string   // parent repo path (the real project root)
	Tags                    []string // user-defined labels (persisted to ~/.cache/cmc/{sessionID}.tags)
	Note                    string   // freeform note (persisted to ~/.cache/cmc/{sessionID}.note)
}

// DisplayName returns the session's display name using the standard priority:
// CustomTitle → SynthesizedTitle → FirstMessage. Returns "" if none are set.
func (s ClaudeSession) DisplayName() string {
	switch {
	case s.CustomTitle != "":
		return s.CustomTitle
	case s.SynthesizedTitle != "":
		return s.SynthesizedTitle
	case s.FirstMessage != "":
		return s.FirstMessage
	default:
		return ""
	}
}

// LaterBookmark is the persistent on-disk record for a bookmarked session.
type LaterBookmark struct {
	ID           string    `json:"id"`
	PaneID       string    `json:"paneID"` // original pane (may be dead)
	Project      string    `json:"project"`
	CWD          string    `json:"cwd"`
	GitBranch    string    `json:"gitBranch"`
	SynthesizedTitle string `json:"synthesizedTitle"`
	ProblemType  string    `json:"problemType"`
	CustomTitle  string    `json:"customTitle"`
	FirstMessage string    `json:"firstMessage"`
	SessionID    string    `json:"sessionID"`
	CreatedAt    time.Time `json:"createdAt"`
}
