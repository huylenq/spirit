package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func RenderHeader(sessions []claude.ClaudeSession, width int, usageBar *UsageBarModel) string {
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

	// Right side: session counters
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

	// Left side: inline usage bar (if data available)
	left := ""
	if usageBar != nil && usageBar.HasData() {
		left = usageBar.InlineView(width)
	}

	if left == "" {
		return HeaderStyle.Width(width).Align(lipgloss.Right).Render(stats)
	}

	// Compose: left-aligned usage bar + right-aligned stats
	statsWidth := lipgloss.Width(stats)
	leftWidth := lipgloss.Width(left)
	gap := width - leftWidth - statsWidth - 2 // 2 for padding
	if gap < 1 {
		gap = 1
	}
	row := " " + left + strings.Repeat(" ", gap) + stats + " "
	return HeaderStyle.Width(width).Render(row)
}
