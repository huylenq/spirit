package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PromptEditorModel wraps a textarea for multiline prompt input (new session).
type PromptEditorModel struct {
	input  textarea.Model
	active bool
}

func NewPromptEditorModel() PromptEditorModel {
	ta := textarea.New()
	ta.Placeholder = "Initial prompt (optional)…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4096
	ta.SetWidth(60)
	ta.SetHeight(8)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	return PromptEditorModel{input: ta}
}

func (m *PromptEditorModel) Activate() {
	m.active = true
	m.input.SetValue("")
	m.input.Focus()
}

func (m *PromptEditorModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m *PromptEditorModel) Confirm() string {
	val := m.input.Value()
	m.active = false
	m.input.Blur()
	return val
}

func (m PromptEditorModel) Active() bool {
	return m.active
}

func (m PromptEditorModel) Value() string {
	return m.input.Value()
}

func (m *PromptEditorModel) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *PromptEditorModel) SetSize(w, h int) {
	m.input.SetWidth(w)
	m.input.SetHeight(h)
}

// View returns the styled overlay for the prompt editor.
func (m *PromptEditorModel) View(title string, width int) string {
	if !m.active {
		return ""
	}

	// Adapt textarea width to available space (subtract border+padding)
	innerWidth := width - 8
	if innerWidth > 0 {
		m.input.SetWidth(innerWidth)
	}

	header := PromptEditorTitleStyle.Render("New session: " + title)
	body := m.input.View()
	hint := FooterDimStyle.Render("enter") + " send  " +
		FooterDimStyle.Render("alt+enter") + " newline  " +
		FooterDimStyle.Render("esc") + " cancel"

	content := header + "\n\n" + body + "\n\n" + hint

	return PromptEditorOverlayStyle.Width(width - 4).Render(content)
}
