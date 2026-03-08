package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type DeferPromptModel struct {
	input  textinput.Model
	active bool
}

func NewDeferPromptModel() DeferPromptModel {
	ti := textinput.New()
	ti.Placeholder = "minutes"
	ti.Prompt = "Defer for: "
	ti.PromptStyle = FilterPromptStyle
	ti.CharLimit = 8
	return DeferPromptModel{input: ti}
}

func (m *DeferPromptModel) Activate() {
	m.active = true
	m.input.Focus()
	m.input.SetValue("10")
}

func (m *DeferPromptModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *DeferPromptModel) Confirm() string {
	val := m.input.Value()
	m.active = false
	m.input.Blur()
	return val
}

func (m DeferPromptModel) Active() bool {
	return m.active
}

func (m DeferPromptModel) View() string {
	if !m.active {
		return ""
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(m.input.View())
}

func (m *DeferPromptModel) TextInput() *textinput.Model {
	return &m.input
}
