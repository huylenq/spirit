package app

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// execToggleCopilot toggles the copilot panel on/off.
func execToggleCopilot(m *Model) (Model, tea.Cmd) {
	if m.state == StateCopilot || m.state == StateCopilotConfirm {
		m.state = StateNormal
	} else {
		m.state = StateCopilot
		m.copilotInput.Activate()
	}
	return *m, nil
}

// handleKeyCopilot handles key events when the copilot chat panel is active.
func (m Model) handleKeyCopilot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.state = StateNormal
		return m, nil

	case msg.String() == "enter":
		if !m.copilot.Streaming() {
			text := m.copilotInput.Value()
			if text != "" {
				m.copilot.AddUserMessage(text)
				m.copilot.SetStreaming(true)
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				return m, m.sendCopilotChat(text)
			}
		}
		return m, nil

	case msg.String() == "ctrl+c":
		if m.copilot.Streaming() {
			return m, m.cancelCopilotChat()
		}
		m.state = StateNormal
		return m, nil

	case msg.String() == "ctrl+d":
		m.copilot.ScrollDown(5)
		return m, nil

	case msg.String() == "ctrl+u":
		m.copilot.ScrollUp(5)
		return m, nil

	default:
		// Forward to copilot input relay
		var cmd tea.Cmd
		*m.copilotInput.TextInput(), cmd = m.copilotInput.TextInput().Update(msg)
		return m, cmd
	}
}

// handleKeyCopilotConfirm handles key events when a tool confirmation is pending.
func (m Model) handleKeyCopilotConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.copilot.ClearPendingTool()
		m.state = StateCopilot
		return m, nil

	case "n", "esc":
		m.copilot.ClearPendingTool()
		m.state = StateCopilot
		return m, nil
	}

	return m, nil
}

// sendCopilotChat sends the user's message to the copilot via the daemon RPC.
// The call is blocking (synchronous), so it runs as a Bubble Tea command.
func (m *Model) sendCopilotChat(message string) tea.Cmd {
	return func() tea.Msg {
		response, err := m.client.CopilotChat(message)
		if err != nil {
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: err.Error(),
			}}
		}
		// Send the full response as a text delta, then done
		return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
			Type:    "text_delta",
			Content: response,
		}}
	}
}

// cancelCopilotChat cancels the in-flight copilot prompt.
func (m *Model) cancelCopilotChat() tea.Cmd {
	return func() tea.Msg {
		_ = m.client.CopilotCancel()
		return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
			Type: "done",
		}}
	}
}
