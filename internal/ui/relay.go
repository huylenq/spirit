package ui

import "github.com/charmbracelet/bubbles/textinput"

type RelayModel struct {
	input  textinput.Model
	active bool
}

func NewRelayModel() RelayModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = "❯ "
	ti.PromptStyle = RelayPromptStyle
	ti.CharLimit = 512
	return RelayModel{input: ti}
}

func NewQueueRelayModel() RelayModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = "❮ "
	ti.PromptStyle = QueuePromptStyle
	ti.CharLimit = 512
	return RelayModel{input: ti}
}

func (m *RelayModel) Activate() {
	m.active = true
	m.input.Focus()
	m.input.SetValue("")
}

func (m *RelayModel) ActivateWithValue(value string) {
	m.active = true
	m.input.Focus()
	m.input.SetValue(value)
	m.input.CursorEnd()
}

func (m *RelayModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *RelayModel) Confirm() string {
	val := m.input.Value()
	m.active = false
	m.input.Blur()
	return val
}

func (m RelayModel) Active() bool {
	return m.active
}

func (m RelayModel) Value() string {
	return m.input.Value()
}

func (m RelayModel) View() string {
	if !m.active {
		return ""
	}
	return m.input.View()
}

func (m *RelayModel) TextInput() *textinput.Model {
	return &m.input
}
