package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/claude"
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
	_, ok := m.sidebar.SelectedItem()
	return ok
}

func hasSessionID(m *Model) bool {
	s, ok := m.sidebar.SelectedItem()
	return ok && s.SessionID != ""
}

func canCommit(m *Model) bool {
	s, ok := m.sidebar.SelectedItem()
	return ok && s.Status == claude.StatusUserTurn && !s.CommitDonePending
}

func (m Model) execSearch() (Model, tea.Cmd) {
	m.state = StateSearching
	m.search.Activate()
	return m, nil
}
