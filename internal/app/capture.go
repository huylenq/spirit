package app

import (
	"os"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

// RenderCapture does a headless render of the TUI and returns ANSI-stripped text.
// cols and rows of 0 mean "auto-detect from terminal, else use default".
func RenderCapture(client *daemon.Client, cols, rows int) (string, error) {
	sessions, usage, err := client.Subscribe()
	if err != nil {
		return "", err
	}

	if cols <= 0 || rows <= 0 {
		w, h, err := term.GetSize(os.Stdout.Fd())
		if err != nil || w <= 0 || h <= 0 {
			w, h = 200, 50
		}
		if cols <= 0 {
			cols = w
		}
		if rows <= 0 {
			rows = h
		}
	}

	m := NewModel(client)
	m.width = cols
	m.height = rows
	m.ready = true
	// showMinimap loaded from prefs by NewModel — keep it
	m.sessions = sessions
	m.list.SetItems(sessions)
	m.applyLayout()

	// Populate diff stats for all sessions (shown as badges in list)
	selectedID := ""
	if s, ok := m.list.SelectedItem(); ok {
		selectedID = s.SessionID
	}
	var selectedStats map[string]claude.FileDiffStat
	for _, s := range sessions {
		if s.SessionID != "" {
			stats, _ := client.DiffStats(s.SessionID)
			m.list.SetDiffStats(s.SessionID, stats)
			if s.SessionID == selectedID {
				selectedStats = stats
			}
		}
	}

	// Populate preview for selected session
	if s, ok := m.list.SelectedItem(); ok {
		content, _ := tmux.CapturePaneContent(s.PaneID)
		m.preview.SetSession(&s, content)

		if s.SessionID != "" {
			msgs, _ := client.Transcript(s.SessionID)
			m.preview.SetUserMessages(msgs)
			summary, _ := client.Summary(s.SessionID)
			m.preview.SetSummary(summary)
			m.preview.SetDiffStats(selectedStats)
		}
	}

	if usage != nil {
		m.usageBar.SetUsage(usage)
	}

	return ansi.Strip(m.View()), nil
}
