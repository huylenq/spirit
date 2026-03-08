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
	ti.Placeholder = "type to search..."
	ti.Prompt = "; "
	ti.PromptStyle = SearchPromptStyle
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

// Narrow re-evaluates the item list based on current input text using fuzzy matching.
func (m *PaletteModel) Narrow() {
	query := strings.ToLower(m.input.Value())
	if query == "" {
		m.filtered = m.items
	} else {
		m.filtered = nil
		for _, item := range m.items {
			if matchesNarrow(item.Name, query) {
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

	maxVisible := min(15, len(m.items)) // fixed to initial item count
	if maxVisible < 1 {
		maxVisible = 1
	}
	rendered := 0
	query := strings.ToLower(m.input.Value())
	for i, item := range m.filtered {
		if rendered >= maxVisible {
			break
		}
		lines = append(lines, renderPaletteItem(item, i == m.cursor, contentWidth, query))
		rendered++
	}
	// Pad with empty rows to keep overlay height stable
	for rendered < maxVisible {
		lines = append(lines, strings.Repeat(" ", contentWidth))
		rendered++
	}

	body := strings.Join(lines, "\n")
	return PaletteOverlayStyle.Render(body)
}

func renderPaletteItem(item PaletteItem, selected bool, width int, query string) string {
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

	padding := strings.Repeat(" ", gap)

	switch {
	case !item.Enabled:
		row := prefix + name + padding + hotkey
		return PaletteDisabledStyle.Render(row)
	case selected:
		nameRendered := highlightMatch(name, query, PaletteSelectedStyle)
		return PaletteSelectedStyle.Render(prefix) + nameRendered + PaletteSelectedStyle.Render(padding+hotkey)
	default:
		nameRendered := highlightMatch(name, query, lipgloss.NewStyle())
		return prefix + nameRendered + padding + lipgloss.NewStyle().Foreground(ColorMuted).Render(hotkey)
	}
}
