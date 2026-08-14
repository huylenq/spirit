package daemon

import (
	"testing"

	"github.com/huylenq/spirit/internal/agent"
)

func TestSessionsEqualDetectsCustomTitleChanges(t *testing.T) {
	base := agent.Session{
		PaneID:    "%1",
		SessionID: "session-1",
		Status:    agent.StatusUserTurn,
	}

	if sessionsEqual([]agent.Session{base}, []agent.Session{base}) != true {
		t.Fatal("identical sessions should compare equal")
	}

	updated := base
	updated.CustomTitle = "renamed from Codex"
	if sessionsEqual([]agent.Session{base}, []agent.Session{updated}) {
		t.Fatal("custom title changes must trigger a session refresh")
	}
}
