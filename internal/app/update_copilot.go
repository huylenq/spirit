package app

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// execOpenCopilot opens or re-focuses the copilot. Never hides it.
func execOpenCopilot(m *Model) (Model, tea.Cmd) {
	if m.state == StateCopilot || m.state == StateCopilotConfirm {
		return *m, nil // already focused, no-op
	}
	if m.copilotVisible {
		// Visible but unfocused → re-focus
		m.state = StateCopilot
		m.copilotInput.TextInput().Focus()
		m.copilotInput.SetPromptStyle(ui.CopilotPromptStyle)
	} else {
		// Hidden → open focused
		m.state = StateCopilot
		m.copilotVisible = true
		m.copilotInput.Activate()
	}
	return *m, nil
}

// execToggleCopilot cycles the copilot overlay through three states:
//   - Hidden → focused (open with input active)
//   - Focused → hidden (gc while focused closes it)
//   - Unfocused (visible, StateNormal) → focused (gc re-focuses)
func execToggleCopilot(m *Model) (Model, tea.Cmd) {
	if m.state == StateCopilot || m.state == StateCopilotConfirm {
		// Focused → hide
		m.state = StateNormal
		m.copilotVisible = false
	} else if m.copilotVisible {
		// Visible but unfocused → hide
		m.copilotVisible = false
	} else {
		// Hidden → open focused
		m.state = StateCopilot
		m.copilotVisible = true
		m.copilotInput.Activate()
	}
	return *m, nil
}

// unfocusCopilot transitions the copilot from focused to unfocused (visible but read-only).
func (m *Model) unfocusCopilot() {
	m.state = StateNormal
	m.copilotInput.TextInput().Blur()
	m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
}

// handleKeyCopilot handles key events when the copilot chat panel is active.
func (m Model) handleKeyCopilot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape):
		m.unfocusCopilot()
		return m, nil

	case msg.String() == "enter":
		if !m.copilot.Streaming() {
			text := m.copilotInput.Value()
			if text == "/new" {
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				return m, m.clearCopilotHistory()
			}
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
		m.unfocusCopilot()
		return m, nil

	case msg.String() == "ctrl+d":
		m.copilot.ScrollDown(5)
		return m, nil

	case msg.String() == "ctrl+u":
		m.copilot.ScrollUp(5)
		return m, nil

	case msg.String() == "ctrl+a":
		m.state = StateAdjustCopilot
		m.copilotInput.TextInput().Blur()
		m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
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

// sendCopilotChat fires off the copilot prompt to the daemon.
// Stream events arrive via the subscribe connection, not the RPC return.
func (m *Model) sendCopilotChat(message string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.CopilotChat(message); err != nil {
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: err.Error(),
			}}
		}
		return nil // stream events arrive via subscribe connection
	}
}

// clearCopilotHistory tells the daemon to wipe history, then clears the local model.
func (m *Model) clearCopilotHistory() tea.Cmd {
	return func() tea.Msg {
		if err := m.client.CopilotClearHistory(); err != nil {
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: "clear history: " + err.Error(),
			}}
		}
		return CopilotHistoryReadyMsg{} // empty = clear
	}
}

// cancelCopilotChat cancels the in-flight copilot prompt.
// The daemon pushes "done" via the stream when the subprocess exits.
func (m *Model) cancelCopilotChat() tea.Cmd {
	return func() tea.Msg {
		_ = m.client.CopilotCancel()
		return nil // daemon pushes "done" via stream when subprocess exits
	}
}

// handleKeyAdjustCopilot handles key events in StateAdjustCopilot (resize/reposition mode).
// Arrows move the overlay; shift+arrows resize it; r resets; esc/enter returns to chat.
func (m Model) handleKeyAdjustCopilot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		m.copilotOffY--
		savePrefInt("copilotOffY", m.copilotOffY)
	case "down":
		m.copilotOffY++
		savePrefInt("copilotOffY", m.copilotOffY)
	case "left":
		m.copilotOffX--
		savePrefInt("copilotOffX", m.copilotOffX)
	case "right":
		m.copilotOffX++
		savePrefInt("copilotOffX", m.copilotOffX)
	case "shift+left":
		m.copilotDW -= 5
		savePrefInt("copilotDW", m.copilotDW)
	case "shift+right":
		m.copilotDW += 5
		savePrefInt("copilotDW", m.copilotDW)
	case "shift+up":
		m.copilotDH += 3
		savePrefInt("copilotDH", m.copilotDH)
	case "shift+down":
		m.copilotDH -= 3
		savePrefInt("copilotDH", m.copilotDH)
	case "r":
		m.copilotOffX, m.copilotOffY, m.copilotDW, m.copilotDH = 0, 0, 0, 0
		savePrefInt("copilotOffX", 0)
		savePrefInt("copilotOffY", 0)
		savePrefInt("copilotDW", 0)
		savePrefInt("copilotDH", 0)
	case "esc", "enter":
		m.state = StateCopilot
		m.copilotInput.TextInput().Focus()
		m.copilotInput.SetPromptStyle(ui.CopilotPromptStyle)
	}
	return m, nil
}
