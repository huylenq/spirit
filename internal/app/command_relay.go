package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (m Model) execPromptRelay() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		m.state = StatePromptRelay
		m.relay.Activate()
	}
	return m, nil
}

func (m Model) execTagRelay() (Model, tea.Cmd) {
	canTag := false
	if s, ok := m.sidebar.SelectedItem(); ok && s.SessionID != "" {
		canTag = true
	} else if _, ok := m.sidebar.SelectedBacklog(); ok {
		canTag = true
	}
	if canTag {
		m.state = StateTagRelay
		m.tagRelay.Activate()
	}
	return m, nil
}

func (m Model) execQueue() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		m.state = StateQueueRelay
		m.queueCursor = -1 // start with text input focused
		m.queueRelay.Activate()
	}
	return m, nil
}

func (m Model) execLater() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		if s.LaterBookmarkID != "" {
			// Toggle: unlater to restore real status
			paneID, bookmarkID := s.PaneID, s.LaterBookmarkID
			return m, func() tea.Msg {
				// Bookmark ID may not be populated yet; look it up
				if bookmarkID == "" {
					bookmarkID = claude.FindBookmarkIDByPane(paneID)
				}
				if bookmarkID == "" {
					return flashErrorMsg("no bookmark found")
				}
				if err := m.client.Unlater(bookmarkID); err != nil {
					return flashErrorMsg("unlater failed: " + err.Error())
				}
				return flashInfoMsg("restored from later")
			}
		}
		paneID, sessionID := s.PaneID, s.SessionID
		return m, func() tea.Msg {
			if err := m.client.Later(paneID, sessionID); err != nil {
				return flashErrorMsg("later failed: " + err.Error())
			}
			return flashInfoMsg("saved for later")
		}
	}
	return m, nil
}

func (m Model) execLaterKill() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		paneID, pid, sessionID := s.PaneID, s.PID, s.SessionID
		return m, func() tea.Msg {
			if err := m.client.LaterKill(paneID, pid, sessionID); err != nil {
				return flashErrorMsg("later+kill failed: " + err.Error())
			}
			return flashInfoMsg("saved for later, pane killed")
		}
	}
	return m, nil
}
