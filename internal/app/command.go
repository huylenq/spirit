package app

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/claude"
)

// Command represents a single dispatchable action for the command palette.
// Hotkey display and (for chord-bound commands) the executor are derived from
// Binding or Chord, so the palette never duplicates the keymap registry.
type Command struct {
	Name    string
	Binding *key.Binding        // single-key binding; mutually exclusive with Chord
	Chord   *Chord              // chord registry entry; mutually exclusive with Binding
	Enabled func(m *Model) bool // nil = always enabled
	Execute func(m *Model) (Model, tea.Cmd)
}

// HotkeyDisplay returns the palette hotkey hint. Prefers the binding's help
// text since it can pretty-print (e.g. "⇧tab" for shift+tab).
func (c Command) HotkeyDisplay() string {
	if c.Binding != nil {
		if h := c.Binding.Help().Key; h != "" {
			return h
		}
		if keys := c.Binding.Keys(); len(keys) > 0 {
			return keys[0]
		}
	}
	if c.Chord != nil {
		return formatChordKeys(c.Chord.Keys)
	}
	return ""
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
