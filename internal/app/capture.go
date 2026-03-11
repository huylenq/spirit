package app

import (
	"os"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/daemon"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
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
	m.sessions = sessions
	m.sidebar.SetItems(sessions)
	m.applyLayout()

	// Discover backlog items from session CWDs
	m.sidebar.SetBacklog(claude.DiscoverBacklogs(sessions))

	// Load minimap geometry if minimap is enabled
	if m.showMinimap {
		if s, ok := m.sidebar.SelectedItem(); ok {
			panes, err := client.PaneGeometry(s.TmuxSession)
			if err == nil && len(panes) > 0 {
				paneStatuses := make(map[string]int)
				paneAvatars := make(map[string]ui.PaneAvatarInfo)
				for _, sess := range sessions {
					if sess.LaterBookmarkID != "" {
						paneStatuses[sess.PaneID] = ui.PaneStatusLater
					} else {
						paneStatuses[sess.PaneID] = claudeStatusToPane(sess.Status)
					}
					paneAvatars[sess.PaneID] = ui.PaneAvatarInfo{
						ColorIdx:  sess.AvatarColorIdx,
						AnimalIdx: sess.AvatarAnimalIdx,
					}
				}
				m.minimap.SetData(panes, paneStatuses, paneAvatars, s.PaneID, s.TmuxSession)
				m.minimapSession = s.TmuxSession
				m.applyLayout()
			}
		}
	}

	// Populate diff stats for all sessions (shown as badges in list)
	selectedID := ""
	if s, ok := m.sidebar.SelectedItem(); ok {
		selectedID = s.SessionID
	}
	var selectedStats map[string]claude.FileDiffStat
	for _, s := range sessions {
		if s.SessionID != "" {
			stats, _ := client.DiffStats(s.SessionID)
			m.sidebar.SetDiffStats(s.SessionID, stats)
			if s.SessionID == selectedID {
				selectedStats = stats
			}
		}
	}

	// Populate preview for selected session
	if s, ok := m.sidebar.SelectedItem(); ok {
		content, _ := tmux.CapturePaneContent(s.PaneID)
		m.detail.SetSession(&s, content)

		if s.SessionID != "" {
			msgs, _ := client.Transcript(s.SessionID)
			m.detail.SetUserMessages(msgs)
			summary, _ := client.Summary(s.SessionID)
			m.detail.SetSummary(summary)
			m.detail.SetDiffStats(selectedStats)
		}
	}

	if usage != nil {
		m.usageBar.SetUsage(usage)
	}

	return ansi.Strip(m.View()), nil
}
