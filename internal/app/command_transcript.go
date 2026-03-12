package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (m Model) execChatOutline() (Model, tea.Cmd) {
	m.chatOutlineMode = nextChatOutlineMode(m.chatOutlineMode)
	savePrefString("chatOutlineMode", m.chatOutlineMode)
	m.detail.SetChatOutlineMode(m.chatOutlineMode)
	return m, nil
}

func (m Model) execCommit() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.Status != claude.StatusUserTurn {
		return m, func() tea.Msg { return flashErrorMsg("session is busy") }
	}
	if s.CommitDonePending {
		return m, func() tea.Msg { return flashInfoMsg("commit already pending") }
	}
	paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
	return m, func() tea.Msg {
		if err := m.client.CommitOnly(paneID, sessionID, pid); err != nil {
			return flashErrorMsg("commit failed: " + err.Error())
		}
		return flashInfoMsg("commit started")
	}
}

func (m Model) execCommitAndDone() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.Status != claude.StatusUserTurn {
		return m, func() tea.Msg { return flashErrorMsg("session is busy") }
	}
	if s.CommitDonePending {
		return m, func() tea.Msg { return flashInfoMsg("commit+done already pending") }
	}
	paneID, sessionID, pid := s.PaneID, s.SessionID, s.PID
	return m, func() tea.Msg {
		if err := m.client.CommitAndDone(paneID, sessionID, pid); err != nil {
			return flashErrorMsg("commit+done failed: " + err.Error())
		}
		return flashInfoMsg("commit+done started")
	}
}

func (m Model) execToggleDiffs() (Model, tea.Cmd) {
	m.showDiffs = !m.showDiffs
	m.showHooks = false
	m.showRawTranscript = false
	m.detail.SetShowDiffs(m.showDiffs)
	m.detail.SetShowHooks(false)
	m.detail.SetShowRawTranscript(false)
	if m.showDiffs {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchDiffHunks(s.PaneID, s.SessionID, s.CWD)
		}
	}
	return m, nil
}

func (m Model) execToggleRawTranscript() (Model, tea.Cmd) {
	m.showRawTranscript = !m.showRawTranscript
	m.showHooks = false
	m.showDiffs = false
	m.detail.SetShowRawTranscript(m.showRawTranscript)
	m.detail.SetShowHooks(false)
	m.detail.SetShowDiffs(false)
	if m.showRawTranscript {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchRawTranscript(s.PaneID, s.SessionID)
		}
	}
	return m, nil
}
