package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// PaletteItem is a snapshot of a command for display in the palette.
type PaletteItem struct {
	Name    string
	Hotkey  string
	Enabled bool
	Index   int // index into the original commands slice
}

// PaletteModel is a searchable command palette overlay.
type PaletteModel struct {
	input    textinput.Model
	active   bool
	items    []PaletteItem
	filtered []PaletteItem
	cursor   int
}

func NewPaletteModel() PaletteModel {
	ti := textinput.New()
	ti.Placeholder = "type to filter..."
	ti.Prompt = ": "
	ti.PromptStyle = FilterPromptStyle
	ti.CharLimit = 64
	return PaletteModel{input: ti}
}

func (m *PaletteModel) Activate(items []PaletteItem) {
	m.active = true
	m.items = items
	m.filtered = items
	m.cursor = 0
	m.input.SetValue("")
	m.input.Focus()
}

func (m *PaletteModel) Deactivate() {
	m.active = false
	m.items = nil
	m.filtered = nil
	m.cursor = 0
	m.input.Blur()
	m.input.SetValue("")
}

func (m PaletteModel) Active() bool {
	return m.active
}

// Filter re-evaluates the item list based on current input text.
func (m *PaletteModel) Filter() {
	query := strings.ToLower(m.input.Value())
	if query == "" {
		m.filtered = m.items
	} else {
		m.filtered = nil
		for _, item := range m.items {
			if strings.Contains(strings.ToLower(item.Name), query) {
				m.filtered = append(m.filtered, item)
			}
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

func (m *PaletteModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *PaletteModel) MoveDown() {
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
	}
}

// SelectedIndex returns the original command index of the cursor item.
func (m PaletteModel) SelectedIndex() (int, bool) {
	if len(m.filtered) == 0 {
		return 0, false
	}
	return m.filtered[m.cursor].Index, true
}

// SelectedEnabled returns whether the cursor item is enabled.
func (m PaletteModel) SelectedEnabled() bool {
	if m.cursor >= len(m.filtered) {
		return false
	}
	return m.filtered[m.cursor].Enabled
}

func (m *PaletteModel) TextInput() *textinput.Model {
	return &m.input
}

func (m PaletteModel) View(width int) string {
	if !m.active {
		return ""
	}

	contentWidth := min(50, width*60/100)
	if contentWidth < 30 {
		contentWidth = 30
	}

	var lines []string
	lines = append(lines, m.input.View())
	lines = append(lines, PaletteSepStyle.Render(strings.Repeat("─", contentWidth)))

	maxVisible := 15
	for i, item := range m.filtered {
		if i >= maxVisible {
			break
		}
		lines = append(lines, renderPaletteItem(item, i == m.cursor, contentWidth))
	}

	if len(m.filtered) == 0 {
		lines = append(lines, PaletteDisabledStyle.Render("  no matching commands"))
	}

	body := strings.Join(lines, "\n")
	return PaletteOverlayStyle.Render(body)
}

func renderPaletteItem(item PaletteItem, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}

	hotkey := item.Hotkey
	nameSpace := width - 2 - len(hotkey) - 1 // prefix width, hotkey, min gap
	name := item.Name
	if len(name) > nameSpace {
		name = name[:nameSpace-1] + "…"
	}

	gap := width - 2 - len(name) - len(hotkey)
	if gap < 1 {
		gap = 1
	}

	row := prefix + name + strings.Repeat(" ", gap) + hotkey

	switch {
	case !item.Enabled:
		return PaletteDisabledStyle.Render(row)
	case selected:
		return PaletteSelectedStyle.Render(row)
	default:
		// Dim the hotkey, leave the rest unstyled
		return prefix + name + strings.Repeat(" ", gap) + lipgloss.NewStyle().Foreground(ColorMuted).Render(hotkey)
	}
}
