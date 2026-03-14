package app

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// --- Copilot state helpers ---

// setCopilotVisible sets visibility, persists the preference, and recalculates
// layout when in docked mode (since the detail panel width changes).
func (m *Model) setCopilotVisible(v bool) {
	m.copilotVisible = v
	savePrefBool("copilotVisible", v)
	if m.copilotMode == CopilotModeDocked {
		m.applyLayout()
	}
}

// focusCopilot activates copilot input and sets focused styling.
func (m *Model) focusCopilot() {
	m.copilotInput.TextInput().Focus()
	m.copilotInput.SetPromptStyle(ui.CopilotPromptStyle)
}

// unfocusCopilot transitions the copilot from focused to unfocused (visible but read-only).
func (m *Model) unfocusCopilot() {
	m.state = StateNormal
	m.copilotInput.TextInput().Blur()
	m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
}

// hideCopilot hides the copilot and resets any active copilot state.
func (m *Model) hideCopilot() {
	m.setCopilotVisible(false)
	if m.state == StateCopilot || m.state == StateCopilotConfirm || m.state == StateAdjustCopilot {
		m.state = StateNormal
		m.copilotInput.TextInput().Blur()
	}
}

// showCopilotFocused shows and focuses the copilot from a hidden state.
func (m *Model) showCopilotFocused() {
	m.state = StateCopilot
	m.setCopilotVisible(true)
	m.copilotInput.Activate()
}

// --- Copilot exec functions ---

// execOpenCopilot opens or re-focuses the copilot. Never hides it.
// Tab (single) behavior:
//   - Hidden → show + focus
//   - Unfocused → focus
//   - Focused → unfocus
func execOpenCopilot(m *Model) (Model, tea.Cmd) {
	if m.state == StateCopilot || m.state == StateCopilotConfirm {
		// Focused → unfocus
		m.unfocusCopilot()
		return *m, nil
	}
	if m.copilotVisible {
		// Visible but unfocused → re-focus (detect pending tool)
		if m.copilot.PendingTool() != nil {
			m.state = StateCopilotConfirm
		} else {
			m.state = StateCopilot
		}
		m.focusCopilot()
	} else {
		m.showCopilotFocused()
	}
	return *m, nil
}

// execToggleCopilot toggles copilot visibility (gc chord):
//   - Hidden → show + focus
//   - Visible (any focus) → hide
func execToggleCopilot(m *Model) (Model, tea.Cmd) {
	if m.copilotVisible {
		m.hideCopilot()
	} else {
		m.showCopilotFocused()
	}
	return *m, nil
}

// execHideCopilot unconditionally hides the copilot (double-tab).
func execHideCopilot(m *Model) (Model, tea.Cmd) {
	m.hideCopilot()
	return *m, nil
}

// execSwitchCopilotMode toggles between float and docked mode.
func execSwitchCopilotMode(m *Model) (Model, tea.Cmd) {
	if m.copilotMode == CopilotModeFloat {
		m.copilotMode = CopilotModeDocked
	} else {
		m.copilotMode = CopilotModeFloat
	}
	savePrefString("copilotMode", m.copilotMode)
	m.applyLayout()
	cmd := m.setFlash("copilot: "+m.copilotMode, false, 2*time.Second)
	return *m, cmd
}

// --- Key handlers ---

// handleKeyCopilot handles key events when the copilot chat panel is active.
func (m Model) handleKeyCopilot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "tab":
		// Single tab while focused → unfocus (double-tab handled in handleKey)
		m.unfocusCopilot()
		return m, nil

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
			if text == "/preamble" {
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				return m, m.toggleCopilotPreamble()
			}
			if text != "" {
				m.copilot.AddUserMessage(text)
				m.copilot.SetStreaming(true)
				m.copilot.ResetScroll()
				m.copilotInput.Deactivate()
				m.copilotInput.Activate()
				cmd := m.sendCopilotChat(text)
				// Float mode: auto-unfocus after submit
				if m.copilotMode == CopilotModeFloat {
					m.unfocusCopilot()
				}
				return m, cmd
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

	case msg.String() == "shift+left":
		if m.copilotMode == CopilotModeDocked {
			m.copilotDockedW = max(m.copilotDockedW-5, minCopilotDockedW)
			savePrefInt("copilotDockedW", m.copilotDockedW)
			m.applyLayout()
			return m, nil
		}
		// Forward to input in float mode
		var cmd tea.Cmd
		*m.copilotInput.TextInput(), cmd = m.copilotInput.TextInput().Update(msg)
		return m, cmd

	case msg.String() == "shift+right":
		if m.copilotMode == CopilotModeDocked {
			m.copilotDockedW = min(m.copilotDockedW+5, m.innerWidth()/2)
			savePrefInt("copilotDockedW", m.copilotDockedW)
			m.applyLayout()
			return m, nil
		}
		var cmd tea.Cmd
		*m.copilotInput.TextInput(), cmd = m.copilotInput.TextInput().Update(msg)
		return m, cmd

	case msg.String() == "alt+\"":
		// Adjust mode: float only
		if m.copilotMode == CopilotModeFloat {
			m.state = StateAdjustCopilot
			m.copilotInput.TextInput().Blur()
			m.copilotInput.SetPromptStyle(ui.CopilotPromptDimStyle)
		}
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

// --- Copilot daemon RPCs ---

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

// toggleCopilotPreamble toggles live session injection and shows the new state.
func (m *Model) toggleCopilotPreamble() tea.Cmd {
	return func() tea.Msg {
		state, err := m.client.CopilotTogglePreamble()
		if err != nil {
			return CopilotStreamChunkMsg{Msg: ui.CopilotStreamMsg{
				Type:    "error",
				Content: "toggle preamble: " + err.Error(),
			}}
		}
		m.copilot.AddInfoMessage("preamble: " + state)
		return nil
	}
}

// cancelCopilotChat cancels the in-flight copilot prompt.
func (m *Model) cancelCopilotChat() tea.Cmd {
	return func() tea.Msg {
		_ = m.client.CopilotCancel()
		return nil
	}
}

// --- Adjust mode (float only) ---

// handleKeyAdjustCopilot handles key events in StateAdjustCopilot (resize/reposition mode).
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
		m.focusCopilot()
	}
	return m, nil
}
