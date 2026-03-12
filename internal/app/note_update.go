package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// execNoteEdit activates inline editing of the selected session's note.
func (m Model) execNoteEdit() (tea.Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); !ok {
		return m, nil
	}
	// If transcript is hidden, flip to overlay so the panel is visible.
	if m.chatOutlineMode == ChatOutlineHidden {
		m.chatOutlineMode = ChatOutlineOverlay
		m.detail.SetChatOutlineMode(m.chatOutlineMode)
	}
	m.detail.StartNoteEdit()
	m.state = StateNoteEdit
	return m, nil
}

// handleKeyNoteEdit handles input when the note panel is being edited inline.
// Esc saves and exits; all other keys are forwarded to the textarea.
func (m Model) handleKeyNoteEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !key.Matches(msg, Keys.Escape) {
		return m, m.detail.UpdateNoteEditor(msg)
	}
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		m.detail.StopNoteEdit()
		m.state = StateNormal
		return m, nil
	}
	savedText := strings.TrimSpace(m.detail.NoteValue())
	if err := claude.WriteNote(s.SessionID, savedText); err != nil {
		return m, m.setFlash("save note: "+err.Error(), true, 3*time.Second)
	}
	for i := range m.sessions {
		if m.sessions[i].SessionID == s.SessionID {
			m.sessions[i].Note = savedText
			break
		}
	}
	m.detail.SetNote(savedText)
	m.detail.StopNoteEdit()
	m.state = StateNormal
	flashText := "note saved"
	if savedText == "" {
		flashText = "note cleared"
	}
	return m, m.setFlash(flashText, false, 3*time.Second)
}
