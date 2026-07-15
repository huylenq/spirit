package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The attention inbox (W7): a centered overlay listing unresolved attention
// items (with severity, description, any recommendation, and an audit summary)
// and reactive watches with their FSM state. Derived entirely from the ledger
// via the attention_list RPC; the app layer maps wire types onto the local row
// types below so this package stays daemon-free.

// AttentionItemRow is one unresolved attention item.
type AttentionItemRow struct {
	ID             string
	Category       string
	Severity       string // info | attend | urgent
	Status         string // open | delivered
	SessionLabel   string // display name or short id; may be empty
	Description    string
	Recommendation string
	AuditSummary   string // e.g. "watch 01ABC…: triggered → llm_run ok → delivered"
	UpdatedAt      time.Time
}

// WatchRow is one reactive watch.
type WatchRow struct {
	ID         string
	ScopeLabel string // session name, project, or "fleet"
	Condition  string
	Response   string
	State      string
	Firings    string // "2/20"
	Outcome    string
}

// AttentionModel is the inbox overlay state.
type AttentionModel struct {
	items   []AttentionItemRow
	watches []WatchRow
	cursor  int // 0..len(items)+len(watches)-1: items first, then watches
	loaded  bool
	err     string
}

// SetData replaces the inbox contents (fetched on open and after actions).
func (m *AttentionModel) SetData(items []AttentionItemRow, watches []WatchRow) {
	m.items = items
	m.watches = watches
	m.loaded = true
	m.err = ""
	m.clampCursor()
}

// SetError records a fetch failure to render in place of rows.
func (m *AttentionModel) SetError(err string) {
	m.loaded = true
	m.err = err
}

func (m *AttentionModel) rowCount() int { return len(m.items) + len(m.watches) }

func (m *AttentionModel) clampCursor() {
	if m.cursor >= m.rowCount() {
		m.cursor = m.rowCount() - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *AttentionModel) MoveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
}

// SelectedItem returns the attention item under the cursor, if the cursor is in
// the items section.
func (m *AttentionModel) SelectedItem() (AttentionItemRow, bool) {
	if m.cursor < len(m.items) {
		return m.items[m.cursor], true
	}
	return AttentionItemRow{}, false
}

// SelectedWatch returns the watch under the cursor, if the cursor is in the
// watches section.
func (m *AttentionModel) SelectedWatch() (WatchRow, bool) {
	if i := m.cursor - len(m.items); i >= 0 && i < len(m.watches) {
		return m.watches[i], true
	}
	return WatchRow{}, false
}

var (
	attentionBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("135")). // purple: attention, distinct from Lulu's indigo
				Padding(0, 1)
	attentionTitleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("135")).Bold(true)
	attentionSectionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	attentionDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	attentionUrgentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	attentionAttendStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	attentionInfoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	attentionSelStyle     = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	attentionRecStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("108")).Italic(true)
)

func severityGlyph(sev string) string {
	switch sev {
	case "urgent":
		return attentionUrgentStyle.Render("●")
	case "attend":
		return attentionAttendStyle.Render("●")
	default:
		return attentionInfoStyle.Render("○")
	}
}

// View renders the inbox overlay bounded by maxWidth/maxHeight.
func (m *AttentionModel) View(maxWidth, maxHeight int) string {
	w := min(90, maxWidth-6)
	if w < 40 {
		w = max(40, maxWidth-2)
	}
	inner := w - 4 // border + padding

	var lines []string
	title := attentionTitleStyle.Render("⚡ Attention")
	hint := attentionDimStyle.Render("j/k move · r resolve · c cancel watch · esc close")
	gap := inner - lipgloss.Width(title) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	lines = append(lines, title+strings.Repeat(" ", gap)+hint)
	lines = append(lines, attentionDimStyle.Render(strings.Repeat("─", inner)))

	if m.err != "" {
		lines = append(lines, attentionUrgentStyle.Render("error: "+m.err))
		return attentionBorderStyle.Render(strings.Join(lines, "\n"))
	}

	row := 0
	lines = append(lines, attentionSectionStyle.Render(fmt.Sprintf("Items (%d unresolved)", len(m.items))))
	if len(m.items) == 0 {
		lines = append(lines, attentionDimStyle.Render("  nothing needs you"))
	}
	for _, it := range m.items {
		selected := row == m.cursor
		lines = append(lines, m.renderItemRow(it, selected, inner)...)
		row++
	}

	lines = append(lines, "")
	lines = append(lines, attentionSectionStyle.Render(fmt.Sprintf("Watches (%d)", len(m.watches))))
	if len(m.watches) == 0 {
		lines = append(lines, attentionDimStyle.Render("  none — gw or /watch to create one"))
	}
	for _, wr := range m.watches {
		selected := row == m.cursor
		lines = append(lines, renderWatchRow(wr, selected, inner))
		row++
	}

	// Bound total height, keeping header lines.
	maxLines := maxHeight - 4
	if maxLines > 4 && len(lines) > maxLines {
		lines = append(lines[:maxLines-1], attentionDimStyle.Render(fmt.Sprintf("… %d more lines", len(lines)-maxLines+1)))
	}

	return attentionBorderStyle.Render(strings.Join(lines, "\n"))
}

func (m *AttentionModel) renderItemRow(it AttentionItemRow, selected bool, width int) []string {
	head := fmt.Sprintf("%s %s", severityGlyph(it.Severity), it.Category)
	if it.SessionLabel != "" {
		head += " " + attentionDimStyle.Render("["+it.SessionLabel+"]")
	}
	desc := it.Description
	if desc == "" {
		desc = "(no description)"
	}
	head += " " + desc
	if it.Status == "delivered" {
		head += " " + attentionDimStyle.Render("(seen by lulu)")
	}
	head = truncateLine(head, width)
	if selected {
		head = attentionSelStyle.Render(padLine(head, width))
	}
	out := []string{head}
	if it.Recommendation != "" {
		rec := truncateLine("   ↳ "+it.Recommendation, width)
		out = append(out, attentionRecStyle.Render(rec))
	}
	if it.AuditSummary != "" {
		out = append(out, attentionDimStyle.Render(truncateLine("   "+it.AuditSummary, width)))
	}
	return out
}

func renderWatchRow(wr WatchRow, selected bool, width int) string {
	line := fmt.Sprintf("%s %s → %s on %s  %s  %s",
		watchStateGlyph(wr.State), wr.Condition, wr.Response, wr.ScopeLabel, wr.Firings, attentionDimStyle.Render(wr.Outcome))
	line = truncateLine(line, width)
	if selected {
		line = attentionSelStyle.Render(padLine(line, width))
	}
	return line
}

func watchStateGlyph(state string) string {
	switch state {
	case "active":
		return attentionAttendStyle.Render("◉")
	case "triggered", "processing":
		return attentionUrgentStyle.Render("◉")
	case "failed":
		return attentionUrgentStyle.Render("✗")
	default: // expired / cancelled / delivered
		return attentionDimStyle.Render("◌")
	}
}

// WatchPickerView renders the tiny two-phase watch-creation prompt.
func WatchPickerView(title, options string) string {
	body := attentionTitleStyle.Render(title) + "\n" + options + "\n" + attentionDimStyle.Render("esc cancels")
	return attentionBorderStyle.Render(body)
}

func truncateLine(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	// ANSI-aware truncate: cheap loop from the rune side.
	runes := []rune(s)
	for lipgloss.Width(string(runes)) > width-1 && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func padLine(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
