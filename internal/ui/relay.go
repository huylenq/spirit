package ui

import "github.com/charmbracelet/bubbles/textinput"

type RelayModel struct {
	input      textinput.Model
	active     bool
	bangMode   bool
	origPrompt string
}

func NewRelayModel() RelayModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = "❯ "
	ti.PromptStyle = RelayPromptStyle
	ti.CharLimit = 512
	return RelayModel{input: ti, origPrompt: "❯ "}
}

func NewQueueRelayModel() RelayModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = "❮ "
	ti.PromptStyle = QueuePromptStyle
	ti.CharLimit = 512
	return RelayModel{input: ti, origPrompt: "❮ "}
}

func NewTagRelayModel() RelayModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = "# "
	ti.PromptStyle = TagPromptStyle
	ti.CharLimit = 64
	return RelayModel{input: ti, origPrompt: "# "}
}

func (m *RelayModel) Activate() {
	m.active = true
	m.bangMode = false
	m.input.Prompt = m.origPrompt
	m.input.Focus()
	m.input.SetValue("")
}

func (m *RelayModel) ActivateWithValue(value string) {
	m.active = true
	m.bangMode = false
	m.input.Prompt = m.origPrompt
	m.input.Focus()
	m.input.SetValue(value)
	m.input.CursorEnd()
}

func (m *RelayModel) Deactivate() {
	m.active = false
	m.bangMode = false
	m.input.Prompt = m.origPrompt
	m.input.Blur()
	m.input.SetValue("")
}

// EnterBangMode replaces the chevron prompt with "!" (keeping the current style).
func (m *RelayModel) EnterBangMode() {
	m.bangMode = true
	m.input.Prompt = "! "
}

func (m *RelayModel) Confirm() string {
	bang := m.bangMode
	val := m.teardown()
	if bang {
		return "!" + val
	}
	return val
}

// ConfirmRaw returns the input value without bang prefix (for send mode where ! was already sent).
func (m *RelayModel) ConfirmRaw() string {
	return m.teardown()
}

// teardown resets relay state and returns the input value.
func (m *RelayModel) teardown() string {
	val := m.input.Value()
	m.bangMode = false
	m.input.Prompt = m.origPrompt
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

func (m RelayModel) IsBangMode() bool {
	return m.bangMode
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
