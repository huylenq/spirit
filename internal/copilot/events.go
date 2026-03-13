package copilot

import "time"

// CopilotEventType identifies what happened in the copilot event journal.
type CopilotEventType string

const (
	EventSessionSpawned    CopilotEventType = "session_spawned"
	EventSessionDied       CopilotEventType = "session_died"
	EventStatusChange      CopilotEventType = "status_change"
	EventPromptSubmitted   CopilotEventType = "prompt_submitted"
	EventToolUsed          CopilotEventType = "tool_used"
	EventAgentStopped      CopilotEventType = "agent_stopped"
	EventPermissionWait    CopilotEventType = "permission_wait"
	EventCompacted         CopilotEventType = "compacted"
	EventGitCommit         CopilotEventType = "git_commit"
	EventFileOverlap       CopilotEventType = "file_overlap"
	EventSynthesized       CopilotEventType = "synthesized"
	EventDigestGenerated   CopilotEventType = "digest_generated"
	EventSkillInvoked      CopilotEventType = "skill_invoked"
	EventSessionBookmarked CopilotEventType = "session_bookmarked"
)

// CopilotEvent is a single entry in the copilot event journal.
type CopilotEvent struct {
	Time      time.Time        `json:"time"`
	Type      CopilotEventType `json:"type"`
	SessionID string           `json:"sid,omitempty"`
	Project   string           `json:"project,omitempty"`
	Detail    string           `json:"detail,omitempty"`
}
