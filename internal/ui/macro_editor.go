package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/claude"
)

// MacroEditorModel wraps a textarea for creating/editing Lua macros.
type MacroEditorModel struct {
	input  textarea.Model
	active bool
}

func NewMacroEditorModel() MacroEditorModel {
	ta := textarea.New()
	ta.Placeholder = "Lua macro body…"
	ta.ShowLineNumbers = true
	ta.CharLimit = 8192
	ta.SetWidth(60)
	ta.SetHeight(12)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	return MacroEditorModel{input: ta}
}

// Activate opens the editor with a template for a new macro.
func (m *MacroEditorModel) Activate() {
	m.active = true
	m.input.SetValue("-- key: \n-- name: \n")
	m.input.Focus()
	// Position cursor at end of "-- key: " line
	m.input.SetCursor(0)
}

// ActivateWithContent opens the editor with existing content.
func (m *MacroEditorModel) ActivateWithContent(content string) {
	m.active = true
	m.input.SetValue(content)
	m.input.Focus()
}

func (m *MacroEditorModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
}

func (m MacroEditorModel) Active() bool  { return m.active }
func (m MacroEditorModel) Value() string { return m.input.Value() }

func (m *MacroEditorModel) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// ParseHeader extracts key, name, and body from the editor content.
func (m MacroEditorModel) ParseHeader() (key, name, body string) {
	return claude.ParseMacroHeader(m.Value())
}

// View renders the macro editor overlay.
func (m *MacroEditorModel) View(width int) string {
	if !m.active {
		return ""
	}

	innerWidth := width - 8
	if innerWidth > 0 {
		m.input.SetWidth(innerWidth)
	}

	header := MacroEditorTitleStyle.Render("New macro")
	body := m.input.View()
	hint := MacroEditorKeyStyle.Render("ctrl+s") + FooterDimStyle.Render(" save  ") +
		MacroEditorKeyStyle.Render("esc") + FooterDimStyle.Render(" cancel")

	content := header + "\n\n" + body + "\n\n" + hint
	return MacroEditorOverlayStyle.Width(width - 4).Render(content)
}
