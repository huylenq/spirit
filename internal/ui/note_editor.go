package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// NoteEditorModel wraps a textarea for freeform session notes.
type NoteEditorModel struct {
	input     textarea.Model
	lastWidth int
}

func NewNoteEditorModel() NoteEditorModel {
	ta := textarea.New()
	ta.Placeholder = "Write a note…"
	ta.ShowLineNumbers = false
	ta.Prompt = "" // no "┃ " vertical bar
	ta.CharLimit = 4096
	ta.SetWidth(60)
	ta.SetHeight(8)

	// Match "Your Messages" text color; no cursor-line highlight, no border.
	plain := TranscriptMsgStyle
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.Text = plain
	ta.FocusedStyle.Placeholder = plain
	ta.FocusedStyle.CursorLine = plain
	ta.FocusedStyle.Prompt = lipgloss.NewStyle()
	ta.BlurredStyle = ta.FocusedStyle

	return NoteEditorModel{input: ta}
}

func (m *NoteEditorModel) Activate(content string) {
	m.input.SetValue(content)
	m.input.Focus()
}

func (m *NoteEditorModel) Deactivate() {
	m.input.Blur()
	m.input.SetValue("")
}

// SetWidth updates the textarea width only when it changes.
func (m *NoteEditorModel) SetWidth(w int) {
	if m.lastWidth != w {
		m.input.SetWidth(w)
		m.lastWidth = w
	}
}

func (m NoteEditorModel) Value() string        { return m.input.Value() }
func (m NoteEditorModel) ViewTextarea() string { return m.input.View() }

func (m *NoteEditorModel) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}
