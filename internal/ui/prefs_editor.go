package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxCompletions = 6

// PrefsEditorModel is a raw-text editor with key name completion for the prefs file.
type PrefsEditorModel struct {
	input       textarea.Model
	active      bool
	regKeys     []string          // PrefRegistry key names
	regLabels   map[string]string // key -> human label
	completions []completion      // current fuzzy matches (sorted by score desc)
	compCursor  int
	compVisible bool
}

type completion struct {
	key   string
	label string
	score int
}

func NewPrefsEditorModel(keys []string, labels map[string]string) PrefsEditorModel {
	ta := textarea.New()
	ta.Placeholder = "key=value"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4096
	ta.SetWidth(50)
	ta.SetHeight(12)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	return PrefsEditorModel{
		input:     ta,
		regKeys:   keys,
		regLabels: labels,
	}
}

func (m *PrefsEditorModel) Activate(content string) {
	m.active = true
	m.input.SetValue(content)
	m.input.Focus()
	m.input.CursorEnd()
	m.compVisible = false
	m.compCursor = 0
}

func (m *PrefsEditorModel) Deactivate() {
	m.active = false
	m.input.Blur()
	m.compVisible = false
}

func (m PrefsEditorModel) Active() bool { return m.active }
func (m PrefsEditorModel) Value() string { return m.input.Value() }

func (m PrefsEditorModel) CompletionVisible() bool { return m.compVisible }

func (m *PrefsEditorModel) CompletionUp() {
	if m.compCursor > 0 {
		m.compCursor--
	}
}

func (m *PrefsEditorModel) CompletionDown() {
	if m.compCursor < len(m.completions)-1 {
		m.compCursor++
	}
}

func (m *PrefsEditorModel) DismissCompletion() {
	m.compVisible = false
	m.compCursor = 0
}

// UpdateTextarea forwards a key event to the textarea and refreshes completions.
func (m *PrefsEditorModel) UpdateTextarea(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refreshCompletion()
	return cmd
}

// ApplyCompletion replaces the partial key on the current line with the selected completion + "=".
func (m *PrefsEditorModel) ApplyCompletion() {
	if !m.compVisible || len(m.completions) == 0 {
		return
	}
	sel := m.completions[m.compCursor]

	// Get the full text, figure out which line we're on
	val := m.input.Value()
	row := m.input.Line()
	lines := strings.Split(val, "\n")
	if row >= len(lines) {
		return
	}

	// Replace current line's content before "=" (or the whole line if no "=") with key=
	line := lines[row]
	if idx := strings.Index(line, "="); idx >= 0 {
		lines[row] = sel.key + "=" + line[idx+1:]
	} else {
		lines[row] = sel.key + "="
	}

	totalLines := len(lines)
	newVal := strings.Join(lines, "\n")
	m.input.SetValue(newVal)
	// SetValue puts cursor at end of text; navigate back to the target row
	for i := totalLines - 1; i > row; i-- {
		m.input.CursorUp()
	}
	m.input.CursorEnd()
	m.compVisible = false
	m.compCursor = 0
}

// refreshCompletion updates the dropdown based on the current line's text before "=".
func (m *PrefsEditorModel) refreshCompletion() {
	val := m.input.Value()
	row := m.input.Line()
	lines := strings.Split(val, "\n")
	if row >= len(lines) {
		m.compVisible = false
		return
	}

	line := lines[row]
	// Extract the partial key: everything before "=" (or the whole line if no "=")
	partial := line
	if idx := strings.Index(line, "="); idx >= 0 {
		// cursor is after "=", no completion
		col := m.input.LineInfo().CharOffset
		if col > idx {
			m.compVisible = false
			return
		}
		partial = line[:idx]
	}

	partial = strings.TrimSpace(partial)
	if len(partial) == 0 {
		m.compVisible = false
		return
	}

	// Fuzzy match against registry keys
	var matches []completion
	for _, k := range m.regKeys {
		score := fuzzyScore(k, partial)
		if score >= 0 {
			matches = append(matches, completion{key: k, label: m.regLabels[k], score: score})
		}
	}

	// Sort by score descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	if len(matches) > maxCompletions {
		matches = matches[:maxCompletions]
	}

	m.completions = matches
	m.compVisible = len(matches) > 0
	if m.compCursor >= len(matches) {
		m.compCursor = max(len(matches)-1, 0)
	}
}

// View renders the prefs editor overlay.
func (m PrefsEditorModel) View(width int) string {
	if !m.active {
		return ""
	}

	contentWidth := max(min(60, width*70/100), 30)

	// Adapt textarea width
	innerWidth := contentWidth - 6 // subtract border+padding
	if innerWidth > 0 {
		m.input.SetWidth(innerWidth)
	}

	title := PrefsEditorTitleStyle.Render("Preferences")
	sep := PaletteSepStyle.Render(strings.Repeat("─", contentWidth-4))

	body := m.input.View()

	var parts []string
	parts = append(parts, title)
	parts = append(parts, sep)
	parts = append(parts, body)

	// Completion dropdown — only render actual matches
	if m.compVisible && len(m.completions) > 0 {
		parts = append(parts, sep)
		for i, c := range m.completions {
			prefix := "  "
			if i == m.compCursor {
				prefix = "> "
			}
			entry := prefix + FooterKeyStyle.Render(c.key)
			if c.label != "" {
				entry += "  " + FooterDimStyle.Render(c.label)
			}
			if i == m.compCursor {
				parts = append(parts, PaletteSelectedStyle.Render(entry))
			} else {
				parts = append(parts, entry)
			}
		}
	}

	content := strings.Join(parts, "\n")
	// Fixed height so the overlay doesn't jump when completions appear/disappear.
	// title(1) + sep(1) + textarea(12) + sep(1) + maxCompletions(6) = 21
	fixedH := 14 + maxCompletions + 1
	return PrefsEditorOverlayStyle.Width(contentWidth).Height(fixedH).Render(content)
}
