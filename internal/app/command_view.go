package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) execMinimap() (Model, tea.Cmd) {
	m.showMinimap = !m.showMinimap
	savePrefBool("minimap", m.showMinimap)
	if m.showMinimap {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchMinimapData(s.TmuxSession)
		}
	}
	return m, nil
}

func (m Model) execFullscreen() (Model, tea.Cmd) {
	return m, reopenPopup(m.binaryPath, m.inFullscreenPopup)
}

func (m Model) execRefresh() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		return m, capturePreview(s.PaneID)
	}
	return m, nil
}

func (m Model) execDebug() (Model, tea.Cmd) {
	m.debugMode = !m.debugMode
	if m.debugMode {
		return m, m.fetchGlobalEffects()
	}
	return m, nil
}

func (m Model) execHelp() (Model, tea.Cmd) {
	m.showHelp = true
	return m, nil
}

func (m Model) execGroupMode() (Model, tea.Cmd) {
	newMode := !m.sidebar.GroupByProject()
	m.sidebar.SetGroupByProject(newMode)
	savePrefBool("groupByProject", newMode)
	return m, nil
}

func (m Model) execSynthesize() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok && s.SessionID != "" {
		m.sidebar.SetSummaryLoading(s.PaneID, true)
		return m, m.fetchSynthesize(s.PaneID, s.SessionID)
	}
	return m, nil
}

func (m Model) execSynthesizeAll() (Model, tea.Cmd) {
	var latestPaneID string
	var latestTime time.Time
	for _, sess := range m.sessions {
		if sess.LastChanged.After(latestTime) {
			latestTime = sess.LastChanged
			latestPaneID = sess.PaneID
		}
	}
	for _, sess := range m.sessions {
		if sess.PaneID != latestPaneID && sess.SessionID != "" {
			m.sidebar.SetSummaryLoading(sess.PaneID, true)
		}
	}
	return m, m.fetchSynthesizeAll(latestPaneID)
}

func (m Model) execToggleHooks() (Model, tea.Cmd) {
	m.showHooks = !m.showHooks
	m.showRawTranscript = false
	m.showDiffs = false
	m.detail.SetShowHooks(m.showHooks)
	m.detail.SetShowRawTranscript(false)
	m.detail.SetShowDiffs(false)
	if m.showHooks {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, m.fetchHooks(s.PaneID, s.SessionID)
		}
	}
	return m, nil
}

func (m Model) execShowSpiritAnimal() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); !ok {
		return m, nil
	}
	m.showSpiritAnimal = true
	return m, nil
}
