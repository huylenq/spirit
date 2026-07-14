package app

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
)

// Command represents a single dispatchable action for the command palette.
// Hotkey display and (for chord-bound commands) the executor are derived from
// Binding or Chord, so the palette never duplicates the keymap registry.
type Command struct {
	Name       string
	Binding    *key.Binding        // single-key binding; mutually exclusive with Chord
	Chord      *Chord              // chord registry entry; mutually exclusive with Binding
	Enabled    func(m *Model) bool // nil = always enabled
	Capability agent.Capability    // optional provider capability gate
	Execute    func(m *Model) (Model, tea.Cmd)
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

func (c Command) IsEnabled(m *Model) bool {
	if c.Enabled != nil && !c.Enabled(m) {
		return false
	}
	return c.Capability == "" || m.capabilityAvailable(c.Capability)
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

func (m *Model) capabilityAvailable(capability agent.Capability) bool {
	session, ok := m.sidebar.SelectedItem()
	return ok && m.providers.Availability(session, capability).Supported
}

func (m *Model) requireCapability(capability agent.Capability) tea.Cmd {
	session, ok := m.sidebar.SelectedItem()
	if !ok {
		return func() tea.Msg { return flashErrorMsg("no session selected") }
	}
	availability := m.providers.Availability(session, capability)
	if availability.Supported {
		return nil
	}
	return func() tea.Msg { return flashErrorMsg(availability.Reason) }
}

func canCommit(m *Model) bool {
	s, ok := m.sidebar.SelectedItem()
	return ok && m.capabilityAvailable(agent.CapabilityCommit) && s.Status == claude.StatusUserTurn && !s.CommitDonePending
}

func (m Model) execSearch() (Model, tea.Cmd) {
	m.state = StateSearching
	m.search.Activate()
	return m, nil
}
