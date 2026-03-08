package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type SearchModel struct {
	input  textinput.Model
	active bool
}

func NewSearchModel() SearchModel {
	ti := textinput.New()
	ti.Placeholder = "search by title, summary, messages..."
	ti.Prompt = "/ "
	ti.PromptStyle = SearchPromptStyle
	ti.CharLimit = 64
	return SearchModel{input: ti}
}

func (m *SearchModel) Activate() {
	m.active = true
	m.input.Focus()
	m.input.SetValue("")
}

func (m *SearchModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *SearchModel) Confirm() string {
	val := m.input.Value()
	m.active = false
	m.input.Blur()
	return val
}

func (m SearchModel) Active() bool {
	return m.active
}

func (m SearchModel) Value() string {
	return m.input.Value()
}

func (m *SearchModel) UpdateInput(msg interface{}) {
	// Type assert to tea.Msg would happen in the app layer
	// This is called from the app's Update function
}

func (m SearchModel) View() string {
	if !m.active {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(m.input.View())
}

func (m *SearchModel) TextInput() *textinput.Model {
	return &m.input
}
