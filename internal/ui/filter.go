package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type FilterModel struct {
	input  textinput.Model
	active bool
}

func NewFilterModel() FilterModel {
	ti := textinput.New()
	ti.Placeholder = "filter by title, summary, messages..."
	ti.Prompt = "/ "
	ti.PromptStyle = FilterPromptStyle
	ti.CharLimit = 64
	return FilterModel{input: ti}
}

func (m *FilterModel) Activate() {
	m.active = true
	m.input.Focus()
	m.input.SetValue("")
}

func (m *FilterModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *FilterModel) Confirm() string {
	val := m.input.Value()
	m.active = false
	m.input.Blur()
	return val
}

func (m FilterModel) Active() bool {
	return m.active
}

func (m FilterModel) Value() string {
	return m.input.Value()
}

func (m *FilterModel) UpdateInput(msg interface{}) {
	// Type assert to tea.Msg would happen in the app layer
	// This is called from the app's Update function
}

func (m FilterModel) View() string {
	if !m.active {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(m.input.View())
}

func (m *FilterModel) TextInput() *textinput.Model {
	return &m.input
}
