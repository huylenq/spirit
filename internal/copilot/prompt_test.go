package copilot

import (
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/claude"
)

func TestBuildSessionsPreambleIncludesWorkQueueLane(t *testing.T) {
	preamble := BuildSessionsPreamble([]claude.ClaudeSession{
		{
			SessionID:    "your-turn-id",
			Project:      "spirit",
			GitBranch:    "main",
			FirstMessage: "answer this",
			Status:       claude.StatusUserTurn,
		},
		{
			SessionID:    "later-id",
			Project:      "spirit",
			GitBranch:    "main",
			FirstMessage: "do later",
			Status:       claude.StatusUserTurn,
			LaterID:      "later-record",
		},
		{
			SessionID:    "working-id",
			Project:      "spirit",
			GitBranch:    "main",
			FirstMessage: "keep going",
			Status:       claude.StatusAgentTurn,
		},
	})

	for _, want := range []string{
		"[lane=your-turn, status=idle] your-turn-id",
		"[lane=later, status=idle] later-id",
		"[lane=working, status=working] working-id",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("preamble missing %q:\n%s", want, preamble)
		}
	}
}
