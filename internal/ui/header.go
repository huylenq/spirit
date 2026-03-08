package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func RenderHeader(sessions []claude.ClaudeSession, width int) string {
	working, done, deferred := 0, 0, 0
	for _, s := range sessions {
		switch s.Status {
		case claude.StatusWorking:
			working++
		case claude.StatusDone:
			done++
		case claude.StatusDeferred:
			deferred++
		}
	}

	stats := ""
	if working > 0 {
		stats += StatWorkingStyle.Render(fmt.Sprintf(" %s %d clauding", IconBolt, working))
	}
	if done > 0 {
		stats += StatDoneStyle.Render(fmt.Sprintf("  %s %d your turn", IconFlag, done))
	}
	if deferred > 0 {
		stats += StatDeferredStyle.Render(fmt.Sprintf("  %s %d deferred", IconHourglass, deferred))
	}

	return HeaderStyle.Width(width).Align(lipgloss.Right).Render(stats)
}
