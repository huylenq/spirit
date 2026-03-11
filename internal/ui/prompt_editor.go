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
	peKeyStyle         = lipgloss.NewStyle().Foreground(ColorGreen)
	peActiveStyle      = lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	peBadgeStyle       = lipgloss.NewStyle().Foreground(ColorMuted)
	peBacklogKeyStyle  = lipgloss.NewStyle().Foreground(ColorBacklog)
)

// PromptEditorMode distinguishes how the prompt editor is being used.
type PromptEditorMode int

const (
	ModeNewSession PromptEditorMode = iota
	ModeNewBacklog
	ModeEditBacklog
	ModeSubmitBacklog
)

// PromptEditorModel wraps a textarea for multiline prompt input (new session).
type PromptEditorModel struct {
	input         textarea.Model
	active        bool
	selectedModel string // "" = default (no --model flag)
	planMode      bool   // true = pass --plan flag to claude
	mode          PromptEditorMode
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
	m.mode = ModeNewSession
	m.input.SetValue("")
	m.input.Focus()
	m.selectedModel = ""
	m.planMode = false
}

// ActivateForBacklog opens the editor in new-backlog mode.
func (m *PromptEditorModel) ActivateForBacklog() {
	m.active = true
	m.mode = ModeNewBacklog
	m.input.SetValue("")
	m.input.Placeholder = "Backlog…"
	m.input.Focus()
	m.selectedModel = ""
}

// ActivateForBacklogEdit opens the editor in edit-backlog mode with pre-filled body.
func (m *PromptEditorModel) ActivateForBacklogEdit(body string) {
	m.active = true
	m.mode = ModeEditBacklog
	m.input.SetValue(body)
	m.input.Placeholder = "Backlog…"
	m.input.Focus()
	m.selectedModel = ""
}

// ActivateForBacklogSubmit opens the editor in submit-backlog mode (becomes a session).
func (m *PromptEditorModel) ActivateForBacklogSubmit(body string) {
	m.active = true
	m.mode = ModeSubmitBacklog
	m.input.SetValue(body)
	m.input.Placeholder = "Submit as prompt…"
	m.input.Focus()
	m.selectedModel = ""
}

func (m *PromptEditorModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.input.SetValue("")
	m.input.Placeholder = "Initial prompt (optional)…"
	m.selectedModel = ""
	m.planMode = false
	m.mode = ModeNewSession
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

func (m PromptEditorModel) Mode() PromptEditorMode {
	return m.mode
}

// IsBacklogMode reports whether the editor is in any backlog-related mode.
func (m PromptEditorModel) IsBacklogMode() bool {
	return m.mode == ModeNewBacklog || m.mode == ModeEditBacklog || m.mode == ModeSubmitBacklog
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

// TogglePlan toggles plan mode (--plan flag) on/off.
func (m *PromptEditorModel) TogglePlan() { m.planMode = !m.planMode }

func (m PromptEditorModel) PlanMode() bool { return m.planMode }

// ViewTextarea returns just the raw textarea view without any overlay chrome.
func (m *PromptEditorModel) ViewTextarea() string {
	return m.input.View()
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

	var headerPrefix string
	switch m.mode {
	case ModeNewBacklog:
		headerPrefix = "New backlog: "
	case ModeEditBacklog:
		headerPrefix = "Edit backlog: "
	case ModeSubmitBacklog:
		headerPrefix = "Submit backlog: "
	default:
		headerPrefix = "New session: "
	}

	titleStyle := PromptEditorTitleStyle
	keyStyle := peKeyStyle
	overlayStyle := PromptEditorOverlayStyle
	if m.IsBacklogMode() {
		titleStyle = BacklogPromptEditorTitleStyle
		keyStyle = peBacklogKeyStyle
		overlayStyle = BacklogPromptEditorOverlayStyle
	}

	header := titleStyle.Render(headerPrefix + title)
	if m.selectedModel != "" {
		header += "  " + peBadgeStyle.Render("["+m.selectedModel+"]")
	}
	if m.planMode {
		header += "  " + peBadgeStyle.Render("[plan]")
	}

	body := m.input.View()

	showModelHint := m.mode == ModeNewSession || m.mode == ModeSubmitBacklog

	var hint string
	if m.mode == ModeNewBacklog || m.mode == ModeEditBacklog {
		hint = keyStyle.Render("enter") + FooterDimStyle.Render(" save  ") +
			keyStyle.Render("esc") + FooterDimStyle.Render(" cancel")
	} else {
		var planToggle string
		if showModelHint {
			if m.planMode {
				planToggle = "  " + peActiveStyle.Render("alt+p") + " " + peActiveStyle.Render(IconCheckOn+" plan")
			} else {
				planToggle = "  " + keyStyle.Render("alt+p") + FooterDimStyle.Render(" "+IconCheckOff+" plan")
			}
		}
		hint = keyStyle.Render("enter") + FooterDimStyle.Render(" send  ") +
			keyStyle.Render("esc") + FooterDimStyle.Render(" cancel") + planToggle
	}

	if showModelHint {
		var modelParts []string
		for _, name := range ModelOptions {
			if name == m.selectedModel {
				modelParts = append(modelParts, peActiveStyle.Render(IconRadioOn+" "+name))
			} else {
				modelParts = append(modelParts, FooterDimStyle.Render(IconRadioOff+" ")+peKeyStyle.Render(string(name[0]))+FooterDimStyle.Render(name[1:]))
			}
		}
		sep := FooterDimStyle.Render(" · ")
		modelHint := peKeyStyle.Render("alt+ ") + strings.Join(modelParts, sep)
		hint += "\n" + modelHint
	}

	content := header + "\n\n" + body + "\n\n" + hint

	return overlayStyle.Width(width - 4).Render(content)
}
