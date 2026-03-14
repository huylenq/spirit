package copilot

import (
	"fmt"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

// BuildSessionsPreamble constructs a lightweight preamble with just live session state.
// This is the only daemon-only data worth injecting — everything else the agent can
// fetch via cmc agent commands or already knows from OpenClaw's context.
func BuildSessionsPreamble(sessions []claude.ClaudeSession) string {
	var b strings.Builder
	b.WriteString("<live-sessions time=\"")
	b.WriteString(time.Now().Format("2006-01-02T15:04:05-07:00"))
	b.WriteString("\">\n")

	if len(sessions) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, s := range sessions {
			b.WriteString(formatSession(s))
			b.WriteString("\n")
		}
	}
	b.WriteString("</live-sessions>")
	return b.String()
}

func formatSession(s claude.ClaudeSession) string {
	status := "idle"
	if s.Status == claude.StatusAgentTurn {
		status = "working"
	}

	name := s.DisplayName()
	if name == "" {
		name = "(new)"
	}

	var flags []string
	if s.IsWaiting {
		flags = append(flags, "waiting")
	}
	if s.HasOverlap {
		flags = append(flags, "overlap")
	}
	if s.CompactCount > 0 {
		flags = append(flags, fmt.Sprintf("compact:%d", s.CompactCount))
	}

	line := fmt.Sprintf("- [%s] %s %s/%s \"%s\"", status, s.SessionID, s.Project, s.GitBranch, name)
	if len(flags) > 0 {
		line += " (" + strings.Join(flags, ", ") + ")"
	}
	return line
}
