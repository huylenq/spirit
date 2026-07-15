package agent

import (
	"encoding/json"
	"time"
)

// ProviderID is the stable identifier used at protocol and persistence boundaries.
type ProviderID string

const (
	ProviderClaude ProviderID = "claude"
	ProviderCodex  ProviderID = "codex"
)

func ParseProviderID(s string) ProviderID {
	switch ProviderID(s) {
	case ProviderCodex:
		return ProviderCodex
	default:
		return ProviderClaude
	}
}

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

func (s Status) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *Status) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = ParseStatus(value)
	return nil
}

func ParseStatus(value string) Status {
	switch value {
	case "agent-turn", "working":
		return StatusAgentTurn
	default:
		return StatusUserTurn
	}
}

type Location struct {
	IsSSH bool
	Host  string
}

// Session is Spirit's provider-neutral orchestration model. Provider-specific
// parsers populate the common fields and may retain opaque data in Metadata.
type Session struct {
	Provider                ProviderID
	Model                   string
	TurnID                  string
	TranscriptPath          string
	Metadata                map[string]any `json:",omitempty"`
	PaneID                  string
	Status                  Status
	Project                 string
	ProjectCode             string
	CWD                     string
	GitBranch               string
	TmuxSession             string
	TmuxWindow              int
	TmuxPane                int
	PID                     int
	Location                Location
	LastChanged             time.Time
	CreatedAt               time.Time
	IsPhantom               bool
	LaterID                 string
	SessionID               string
	FirstMessage            string
	LastUserMessage         string
	LastAssistantMessage    string
	LastRecap               string
	Insights                []string
	SynthesizedTitle        string
	TitleDrift              bool
	ProblemType             string
	CustomTitle             string
	PermissionMode          string
	LastActionCommit        bool
	StopReason              string
	SkillName               string
	IsWaiting               bool
	CompactCount            int
	CommitDonePending       bool
	SynthesizePending       bool
	HasOverlap              bool
	QueuePending            []string
	QueueItems              []QueueItem
	AvatarAnimalIdx         int
	AvatarColorIdx          int
	IsWorktree              bool
	WorktreeName            string
	WorktreeRootProjectPath string
	Tags                    []string
	Note                    string
	LaterWakeAt             *time.Time
}

func (s Session) DisplayName() string {
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

type LaterRecord struct {
	Provider         ProviderID `json:"provider,omitempty"`
	ID               string     `json:"id"`
	PaneID           string     `json:"paneID"`
	Project          string     `json:"project"`
	CWD              string     `json:"cwd"`
	GitBranch        string     `json:"gitBranch"`
	SynthesizedTitle string     `json:"synthesizedTitle"`
	ProblemType      string     `json:"problemType"`
	CustomTitle      string     `json:"customTitle"`
	FirstMessage     string     `json:"firstMessage"`
	SessionID        string     `json:"sessionID"`
	CreatedAt        time.Time  `json:"createdAt"`
	WakeAt           *time.Time `json:"wakeAt,omitempty"`
}
