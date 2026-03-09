package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

// Command represents a single dispatchable action for the command palette.
type Command struct {
	Name    string                          // display name shown in palette
	Hotkey  string                          // key hint: "w"
	Enabled func(m *Model) bool             // nil = always enabled
	Execute func(m *Model) (Model, tea.Cmd) // run the action
}

// --- Predicate helpers ---

func hasSelection(m *Model) bool {
	_, ok := m.list.SelectedItem()
	return ok
}

func hasSessionID(m *Model) bool {
	s, ok := m.list.SelectedItem()
	return ok && s.SessionID != ""
}

func canCommit(m *Model) bool {
	s, ok := m.list.SelectedItem()
	return ok && s.Status == claude.StatusUserTurn && !s.CommitDonePending
}

// --- Exec methods (extracted from handleKey case blocks) ---

func (m Model) execSwitchPane() (Model, tea.Cmd) {
	s, ok := m.list.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.IsPhantom {
		bookmarkID, cwd := s.LaterBookmarkID, s.CWD
		tmuxSession := m.origPane.Session
		return m, func() tea.Msg {
			if err := m.client.OpenLater(bookmarkID, cwd, tmuxSession); err != nil {
				return flashErrorMsg("open failed: " + err.Error())
			}
			return tea.QuitMsg{}
		}
	}
	if s.LaterBookmarkID != "" {
		m.client.Unlater(s.LaterBookmarkID) //nolint:errcheck
	}
	tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
	return m, tea.Quit
}

func (m Model) execPromptRelay() (Model, tea.Cmd) {
	if _, ok := m.list.SelectedItem(); ok {
		m.state = StatePromptRelay
		m.relay.Activate()
	}
	return m, nil
}

func (m Model) execQueue() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok {
		m.state = StateQueueRelay
		if s.QueuePending != "" {
			m.queueRelay.ActivateWithValue(s.QueuePending)
		} else {
			m.queueRelay.Activate()
		}
	}
	return m, nil
}

func (m Model) execSearch() (Model, tea.Cmd) {
	m.state = StateSearching
	m.search.Activate()
	return m, nil
}

func (m Model) execLater() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok {
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
	if s, ok := m.list.SelectedItem(); ok {
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

func (m Model) execTranscript() (Model, tea.Cmd) {
	m.hideTranscript = !m.hideTranscript
	m.preview.SetHideTranscript(m.hideTranscript)
	return m, nil
}

func (m Model) execGroupMode() (Model, tea.Cmd) {
	newMode := !m.list.GroupByProject()
	m.list.SetGroupByProject(newMode)
	savePrefBool("groupByProject", newMode)
	return m, nil
}

func (m Model) execMinimap() (Model, tea.Cmd) {
	m.showMinimap = !m.showMinimap
	savePrefBool("minimap", m.showMinimap)
	if m.showMinimap {
		if s, ok := m.list.SelectedItem(); ok {
			return m, m.fetchMinimapData(s.TmuxSession)
		}
	}
	return m, nil
}

func (m Model) execSynthesize() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok && s.SessionID != "" {
		m.list.SetSummaryLoading(s.PaneID, true)
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
			m.list.SetSummaryLoading(sess.PaneID, true)
		}
	}
	return m, m.fetchSynthesizeAll(latestPaneID)
}

func (m Model) execRename() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok && !m.renaming {
		m.renaming = true
		return m, m.fetchRenameWindow(s.TmuxSession, s.TmuxWindow)
	}
	return m, nil
}

func (m Model) execKill() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok {
		if s.IsPhantom && s.LaterBookmarkID != "" {
			bookmarkID := s.LaterBookmarkID
			return m, func() tea.Msg {
				claude.RemoveLaterBookmark(bookmarkID)
				return PaneKilledMsg{}
			}
		}
		m.state = StateKillConfirm
		m.killTargetPaneID = s.PaneID
		m.killTargetPID = s.PID
		m.killTargetTitle = sessionDisplayTitle(s)
		m.killTargetBookmarkID = s.LaterBookmarkID
	}
	return m, nil
}

func (m Model) execCommit() (Model, tea.Cmd) {
	s, ok := m.list.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.Status != claude.StatusUserTurn {
		return m, func() tea.Msg { return flashErrorMsg("session is busy") }
	}
	if s.CommitDonePending {
		return m, func() tea.Msg { return flashInfoMsg("commit already pending") }
	}
	paneID, pid := s.PaneID, s.PID
	return m, func() tea.Msg {
		if err := m.client.CommitOnly(paneID, pid); err != nil {
			return flashErrorMsg("commit failed: " + err.Error())
		}
		return flashInfoMsg("commit started")
	}
}

func (m Model) execCommitAndDone() (Model, tea.Cmd) {
	s, ok := m.list.SelectedItem()
	if !ok {
		return m, nil
	}
	if s.Status != claude.StatusUserTurn {
		return m, func() tea.Msg { return flashErrorMsg("session is busy") }
	}
	if s.CommitDonePending {
		return m, func() tea.Msg { return flashInfoMsg("commit+done already pending") }
	}
	paneID, pid := s.PaneID, s.PID
	return m, func() tea.Msg {
		if err := m.client.CommitAndDone(paneID, pid); err != nil {
			return flashErrorMsg("commit+done failed: " + err.Error())
		}
		return flashInfoMsg("commit+done started")
	}
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

func (m Model) execFullscreen() (Model, tea.Cmd) {
	return m, reopenPopup(m.binaryPath, m.inFullscreenPopup)
}

func (m Model) execRefresh() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok {
		return m, capturePreview(s.PaneID)
	}
	return m, nil
}

func (m Model) execCopySessionID() (Model, tea.Cmd) {
	if s, ok := m.list.SelectedItem(); ok && s.SessionID != "" {
		return m, copyToClipboard(s.SessionID)
	}
	return m, nil
}

func (m Model) execCaptureView() (Model, tea.Cmd) {
	text := ansi.Strip(m.View())
	return m, copyToClipboard(text)
}

func (m Model) execToggleDiffs() (Model, tea.Cmd) {
	m.showDiffs = !m.showDiffs
	m.showHooks = false
	m.showRawTranscript = false
	m.preview.SetShowDiffs(m.showDiffs)
	m.preview.SetShowHooks(false)
	m.preview.SetShowRawTranscript(false)
	if m.showDiffs {
		if s, ok := m.list.SelectedItem(); ok {
			return m, m.fetchDiffHunks(s.PaneID, s.SessionID)
		}
	}
	return m, nil
}

func (m Model) execToggleHooks() (Model, tea.Cmd) {
	m.showHooks = !m.showHooks
	m.showRawTranscript = false
	m.showDiffs = false
	m.preview.SetShowHooks(m.showHooks)
	m.preview.SetShowRawTranscript(false)
	m.preview.SetShowDiffs(false)
	if m.showHooks {
		if s, ok := m.list.SelectedItem(); ok {
			return m, m.fetchHooks(s.PaneID)
		}
	}
	return m, nil
}

func (m Model) execToggleRawTranscript() (Model, tea.Cmd) {
	m.showRawTranscript = !m.showRawTranscript
	m.showHooks = false
	m.showDiffs = false
	m.preview.SetShowRawTranscript(m.showRawTranscript)
	m.preview.SetShowHooks(false)
	m.preview.SetShowDiffs(false)
	if m.showRawTranscript {
		if s, ok := m.list.SelectedItem(); ok {
			return m, m.fetchRawTranscript(s.PaneID, s.SessionID)
		}
	}
	return m, nil
}
