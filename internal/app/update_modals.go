package app

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleKeySearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape), key.Matches(msg, Keys.Enter):
		m.search.Confirm()
		m.state = StateNormal
		// Remember selection, clear filter, re-select (search & jump)
		ref := m.sidebar.CursorRef()
		m.sidebar.ClearNarrow()
		m.sidebar.SelectByRef(ref)
		return m, nil
	case key.Matches(msg, Keys.MsgNext):
		m.sidebar.MoveDown()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
		}
		return m, nil
	case key.Matches(msg, Keys.MsgPrev):
		m.sidebar.MoveUp()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
		}
		return m, nil
	default:
		// Forward to textinput
		ti := m.search.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		m.sidebar.SetNarrow(m.search.Value())
		// Update preview for new selection
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(cmd, capturePreview(s.PaneID), m.fetchTranscript(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
		}
		return m, cmd
	}
}

func (m Model) handleKeyKillConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		return m, killPaneCmd(m.killTargetPaneID, m.killTargetSessionID, m.killTargetPID, m.killTargetBookmarkID)
	case "n", "esc":
		m.state = StateNormal
		m.killTargetPaneID = ""
		m.killTargetSessionID = ""
		m.killTargetPID = 0
		m.killTargetTitle = ""
		m.killTargetAnimalIdx = 0
		m.killTargetColorIdx = 0
		m.killTargetBookmarkID = ""
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleKeyMinimapSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.state = StateNormal
		m.flashMsg = ""
		m.flashExpiry = time.Time{}
		savePrefInt("minimapMaxH", m.minimapMaxH)
		return m, nil
	case "M":
		m.minimapMode = nextMinimapMode(m.minimapMode)
		savePrefString("minimapMode", m.minimapMode)
		m.applyLayout()
	case "+", "=":
		if m.minimapMaxH < 30 {
			m.minimapMaxH++
			m.applyLayout()
		}
	case "-":
		if m.minimapMaxH > 5 {
			m.minimapMaxH--
			m.applyLayout()
		}
	case "c":
		m.minimapCollapse = !m.minimapCollapse
		savePrefBool("minimapCollapse", m.minimapCollapse)
		m.applyLayout()
	default:
		// Exit and persist scale, then re-dispatch so the key isn't swallowed
		m.state = StateNormal
		m.flashMsg = ""
		m.flashExpiry = time.Time{}
		savePrefInt("minimapMaxH", m.minimapMaxH)
		return m.handleKey(msg)
	}
	return m, m.flashMinimapSettings()
}

func (m Model) handleKeyPrefsEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.prefsEditor.CompletionVisible() {
		switch {
		case key.Matches(msg, Keys.Escape):
			m.prefsEditor.DismissCompletion()
			return m, nil
		case msg.String() == "tab", key.Matches(msg, Keys.Enter):
			m.prefsEditor.ApplyCompletion()
			return m, nil
		case key.Matches(msg, Keys.Up):
			m.prefsEditor.CompletionUp()
			return m, nil
		case key.Matches(msg, Keys.Down):
			m.prefsEditor.CompletionDown()
			return m, nil
		case msg.String() == "ctrl+s":
			return m.savePrefsEditor()
		default:
			cmd := m.prefsEditor.UpdateTextarea(msg)
			return m, cmd
		}
	}

	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		m.prefsEditor.Deactivate()
		return m, nil
	case msg.String() == "ctrl+s":
		return m.savePrefsEditor()
	default:
		cmd := m.prefsEditor.UpdateTextarea(msg)
		return m, cmd
	}
}

func (m Model) savePrefsEditor() (tea.Model, tea.Cmd) {
	text := m.prefsEditor.Value()
	unknowns := m.applyPrefsFromText(text)
	msg := "saved"
	if unknowns > 0 {
		msg = fmt.Sprintf("saved (%d unknown keys)", unknowns)
	}
	return m, func() tea.Msg { return flashInfoMsg(msg) }
}

// flashMinimapSettings shows the current minimap mode+scale in the flash bar with a 3s timeout.
func (m *Model) flashMinimapSettings() tea.Cmd {
	m.flashMsg = minimapModeFlash(m.minimapMode, m.minimapMaxH, m.minimapCollapse)
	m.flashIsError = false
	m.flashExpiry = time.Now().Add(3 * time.Second)
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
}
