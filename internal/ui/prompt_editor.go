package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModelOptions lists the models available for new session creation.
// Each model's first letter doubles as its alt+key shortcut.
var ModelOptions = []string{"opus", "sonnet", "haiku"}

// Cached styles for prompt editor hint rendering (avoids allocations in View hot path).
var (
	peKeyStyle      = lipgloss.NewStyle().Foreground(ColorGreen)
	peActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	peBadgeStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
)

// PromptEditorModel wraps a textarea for multiline prompt input (new session).
type PromptEditorModel struct {
	input         textarea.Model
	active        bool
	selectedModel string // "" = default (no --model flag)
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
	m.selectedModel = ""
}

func (m *PromptEditorModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
	m.selectedModel = ""
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

// SetModel sets the model (toggle: same value clears it back to default).
func (m *PromptEditorModel) SetModel(model string) {
	if m.selectedModel == model {
		m.selectedModel = ""
	} else {
		m.selectedModel = model
	}
}

func (m PromptEditorModel) SelectedModel() string { return m.selectedModel }

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
	if m.selectedModel != "" {
		header += "  " + peBadgeStyle.Render("["+m.selectedModel+"]")
	}

	body := m.input.View()

	// Compact model hint: "alt+ opus · sonnet · haiku" with key letter green
	var modelParts []string
	for _, name := range ModelOptions {
		if name == m.selectedModel {
			modelParts = append(modelParts, peActiveStyle.Render(name))
		} else {
			modelParts = append(modelParts, peKeyStyle.Render(string(name[0]))+FooterDimStyle.Render(name[1:]))
		}
	}
	sep := FooterDimStyle.Render(" · ")
	modelHint := peKeyStyle.Render("alt+ ") + strings.Join(modelParts, sep)

	hint := peKeyStyle.Render("enter") + FooterDimStyle.Render(" send  ") +
		peKeyStyle.Render("esc") + FooterDimStyle.Render(" cancel") + "\n" + modelHint

	content := header + "\n\n" + body + "\n\n" + hint

	return PromptEditorOverlayStyle.Width(width - 4).Render(content)
}
