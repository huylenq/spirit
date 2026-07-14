package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
)

func (m Model) execPromptRelay() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		if cmd := m.requireCapability(agent.CapabilityRelayPrompt); cmd != nil {
			return m, cmd
		}
		terminal, err := m.providers.Terminal(s)
		if err != nil {
			return m, func() tea.Msg { return flashErrorMsg(err.Error()) }
		}
		m.state = StatePromptRelay
		m.relay.ActivateWithPrompt(terminal.RelayPrompt)
	}
	return m, nil
}

func (m Model) execRenamePrompt() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		if cmd := m.requireCapability(agent.CapabilityRenameNative); cmd != nil {
			return m, cmd
		}
		m.state = StateRenamePrompt
		m.renamePrompt.Activate()
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

// execProjectCodeEdit opens the per-project code editor for the selected
// session's project, prefilled with the existing code or an auto-suggestion.
func (m Model) execProjectCodeEdit() (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok || s.Project == "" {
		return m, nil
	}
	m.projectCodeProject = s.Project
	prefill := s.ProjectCode
	if prefill == "" {
		prefill = claude.SuggestProjectCode(s.Project)
	}
	m.state = StateProjectCodeEdit
	m.projectCodeRelay.ActivateWithValue(prefill)
	return m, nil
}

func (m Model) execQueue() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		if cmd := m.requireCapability(agent.CapabilityQueue); cmd != nil {
			return m, cmd
		}
		m.state = StateQueueRelay
		m.queueCursor = -1 // start with text input focused
		m.queueRelay.Activate()
	}
	return m, nil
}

func (m Model) execLater() (Model, tea.Cmd) {
	if s, ok := m.sidebar.SelectedItem(); ok {
		if cmd := m.requireCapability(agent.CapabilityLater); cmd != nil {
			return m, cmd
		}
		if s.LaterID != "" {
			// Toggle: unlater to restore real status
			paneID, laterID := s.PaneID, s.LaterID
			return m, func() tea.Msg {
				// Later ID may not be populated yet; look it up
				if laterID == "" {
					laterID = claude.FindLaterIDByPane(paneID)
				}
				if laterID == "" {
					return flashErrorMsg("no Later record found")
				}
				if err := m.client.Unlater(laterID); err != nil {
					return flashErrorMsg("unlater failed: " + err.Error())
				}
				return flashInfoMsg("restored from later")
			}
		}
		// Enter wait-duration prompt
		m.state = StateLaterWait
		m.laterKillMode = false
		m.laterRelay.Activate()
	}
	return m, nil
}

func (m Model) execLaterKill() (Model, tea.Cmd) {
	if _, ok := m.sidebar.SelectedItem(); ok {
		if cmd := m.requireCapability(agent.CapabilityLater); cmd != nil {
			return m, cmd
		}
		m.state = StateLaterWait
		m.laterKillMode = true
		m.laterRelay.Activate()
	}
	return m, nil
}

// withWaitSuffix appends " (wait)" to base when wait is non-empty.
func withWaitSuffix(base, wait string) string {
	if wait == "" {
		return base
	}
	return base + " (" + wait + ")"
}

// execLaterConfirm runs the actual Later/LaterKill RPC with the given wait duration.
func (m Model) execLaterConfirm(wait string) (Model, tea.Cmd) {
	s, ok := m.sidebar.SelectedItem()
	if !ok {
		return m, nil
	}
	if m.laterKillMode {
		paneID, pid, sessionID := s.PaneID, s.PID, s.SessionID
		return m, func() tea.Msg {
			if err := m.client.LaterKill(paneID, pid, sessionID, wait); err != nil {
				return flashErrorMsg("later+kill failed: " + err.Error())
			}
			return flashInfoMsg(withWaitSuffix("saved for later, pane killed", wait))
		}
	}
	paneID, sessionID := s.PaneID, s.SessionID
	cmds := []tea.Cmd{func() tea.Msg {
		if err := m.client.Later(paneID, sessionID, wait); err != nil {
			return flashErrorMsg("later failed: " + err.Error())
		}
		return flashInfoMsg(withWaitSuffix("saved for later", wait))
	}}
	cmds = append(cmds, m.autoJump(s.PaneID)...)
	return m, tea.Batch(cmds...)
}
