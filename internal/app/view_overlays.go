package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

// formatMessageEntry formats a single message log entry as a styled line.
func formatMessageEntry(entry MessageLogEntry) string {
	ts := ui.FooterDimStyle.Render(entry.Time.Format("15:04:05"))
	style := ui.FlashInfoStyle
	if entry.IsError {
		style = ui.FlashErrorStyle
	}
	return ts + " " + style.Render(entry.Text)
}

// renderMessageLog returns the full message history overlay.
func (m Model) renderMessageLog() string {
	title := ui.HelpTitleStyle.Render("Messages")
	dismiss := ui.FooterDimStyle.Render("! or esc to close")
	if len(m.messageLog) == 0 {
		body := title + "\n\n" + ui.FooterDimStyle.Render("no messages yet") + "\n\n" + dismiss
		return ui.HelpOverlayStyle.Render(body)
	}
	entries := m.messageLog
	if len(entries) > 20 {
		entries = entries[len(entries)-20:]
	}
	var lines []string
	for _, entry := range entries {
		lines = append(lines, formatMessageEntry(entry))
	}
	body := title + "\n\n" + strings.Join(lines, "\n") + "\n\n" + dismiss
	return ui.HelpOverlayStyle.Render(body)
}

// renderMessageToast renders the active toast queue. Entries are explicitly popped
// by ClearToastMsg ticks — no TTL filtering needed here.
func (m Model) renderMessageToast() string {
	if len(m.toastQueue) == 0 {
		return ""
	}
	var lines []string
	for _, entry := range m.toastQueue {
		lines = append(lines, formatMessageEntry(entry))
	}
	return ui.ToastStyle.Render(strings.Join(lines, "\n"))
}

// overlayPrompt composites the prompt editor onto content, anchored at (row, col)
// where col is right after the "📁 project" label — same positioning for both the
// new-session and new-backlog overlays.
func (m Model) overlayPrompt(content, project string, row, innerWidth int) string {
	col := lipgloss.Width(ui.IconFolder+" "+project) + 3 // 1 left pad + 1 right pad + 1 gap
	overlayWidth := min(innerWidth-col, 72)
	overlayView := m.promptEditor.View(project, overlayWidth)
	return ui.OverlayAt(content, overlayView, row, col)
}

// renderBacklogEditor renders the backlog textarea editor inline in the detail panel.
func (m Model) renderBacklogEditor(project string, width, height int) string {
	var modeLabel string
	switch m.promptEditor.Mode() {
	case ui.ModeNewBacklog:
		modeLabel = "New backlog"
	case ui.ModeEditBacklog:
		modeLabel = "Edit backlog"
	default:
		modeLabel = "Backlog"
	}

	header := ui.PromptEditorTitleStyle.Render(modeLabel + ": " + project)

	// Size the textarea to fill available space
	editorWidth := width - 4
	if editorWidth < 20 {
		editorWidth = 20
	}
	editorHeight := height - 6 // header + hints + padding
	if editorHeight < 3 {
		editorHeight = 3
	}
	m.promptEditor.SetSize(editorWidth, editorHeight)

	body := m.promptEditor.ViewTextarea()

	hint := ui.FooterKeyStyle.Render("enter") + ui.FooterDimStyle.Render(" save  ") +
		ui.FooterKeyStyle.Render("alt+enter") + ui.FooterDimStyle.Render(" newline  ") +
		ui.FooterKeyStyle.Render("esc") + ui.FooterDimStyle.Render(" cancel")

	return header + "\n\n" + body + "\n\n" + hint
}

// renderBacklogPreview renders the full backlog item body as plain text for the detail panel.
func (m Model) renderBacklogPreview(backlog claude.Backlog) string {
	header := ui.PromptEditorTitleStyle.Render(ui.IconBacklog + " " + backlog.DisplayTitle())
	project := ui.ItemDetailStyle.Render(ui.IconFolder + " " + backlog.Project)
	age := ui.ItemDetailStyle.Render("created " + ui.FormatAge(backlog.CreatedAt) + " ago")

	body := backlog.Body
	if body == "" {
		body = ui.ItemDetailStyle.Render("(empty)")
	}

	return header + "\n" + project + "  " + age + "\n\n" + body
}

// renderSearchBar renders the search/filter bar that replaces the usage stats label.
func (m Model) renderSearchBar(width int) string {
	filterView := m.search.View()
	usageLabel := m.usageBar.LabelView()
	usageLabelWidth := lipgloss.Width(usageLabel)
	filterWidth := lipgloss.Width(filterView)
	gap := width - filterWidth - usageLabelWidth
	if gap < 2 {
		return ui.BorderLabelStyle.Width(width).Render(filterView)
	}
	return filterView + strings.Repeat(" ", gap) + usageLabel
}

// renderFooter renders the context-sensitive footer bar.
func (m Model) renderFooter(width int) string {
	switch m.state {
	case StateMacro:
		h := ui.FooterKeyStyle.Render("<key>") + " run  " +
			ui.FooterKeyStyle.Render("alt+<key>") + " edit  " +
			ui.FooterKeyStyle.Render("=") + " create  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateMacroEdit:
		h := ui.FooterKeyStyle.Render("ctrl+s") + " save  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateNoteEdit:
		h := ui.FooterKeyStyle.Render("esc") + " save"
		return ui.FooterStyle.Width(width).Render(h)
	case StatePalette:
		var h string
		if m.palette.IsLuaMode() {
			h = ui.FooterKeyStyle.Render("enter") + " run lua  " +
				ui.FooterKeyStyle.Render("esc") + " cancel  " +
				ui.FooterDimStyle.Render("(: to enter lua mode)")
		} else {
			h = ui.FooterKeyStyle.Render("enter") + " execute  " +
				ui.FooterKeyStyle.Render("↑/↓") + " navigate  " +
				ui.FooterKeyStyle.Render("esc") + " cancel  " +
				ui.FooterDimStyle.Render(": lua")
		}
		return ui.FooterStyle.Width(width).Render(h)
	case StatePrefsEditor:
		h := ui.FooterKeyStyle.Render("ctrl+s") + " save  " +
			ui.FooterKeyStyle.Render("tab") + " complete  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateSearching:
		h := ui.FooterKeyStyle.Render("C-j/k") + ui.FooterDimStyle.Render(" navigate  ") +
			ui.FooterKeyStyle.Render("enter") + ui.FooterDimStyle.Render(" confirm  ") +
			ui.FooterKeyStyle.Render("esc") + ui.FooterDimStyle.Render(" clear")
		return ui.FooterStyle.Width(width).Render(h)
	case StatePromptRelay:
		h := ui.FooterKeyStyle.Render("enter") + " send  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateNewSessionPrompt:
		h := ui.FooterKeyStyle.Render("enter") + " send  " +
			ui.FooterKeyStyle.Render("esc") + " cancel  " +
			ui.FooterDimStyle.Render("alt+") +
			ui.FooterKeyStyle.Render("o") + "pus " +
			ui.FooterKeyStyle.Render("s") + "onnet " +
			ui.FooterKeyStyle.Render("h") + "aiku"
		return ui.FooterStyle.Width(width).Render(h)
	case StateQueueRelay:
		h := ui.FooterKeyStyle.Render("enter") + " append  " +
			ui.FooterKeyStyle.Render("↑↓") + " select  " +
			ui.FooterKeyStyle.Render("ctrl+d") + " remove  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateBacklogPrompt:
		h := ui.FooterKeyStyle.Render("enter") + " save  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateBacklogDeleteConfirm:
		titleStr := lipgloss.NewStyle().Bold(true).Render(m.deleteTargetBacklog.DisplayTitle())
		prompt := ui.FooterDimStyle.Render("Delete backlog ") +
			titleStr +
			ui.FooterDimStyle.Render(" ? ") +
			ui.FooterKeyStyle.Render("[y]") + "es " +
			ui.FooterKeyStyle.Render("[n]") + "o"
		return ui.FooterStyle.Width(width).Render(prompt)
	case StateKillConfirm:
		avatarColor := ui.AvatarColor(m.killTargetColorIdx)
		avatarStr := ui.AvatarStyle(m.killTargetColorIdx).Render(ui.AvatarGlyph(m.killTargetAnimalIdx))
		titleStr := lipgloss.NewStyle().Bold(true).Foreground(avatarColor).Render(m.killTargetTitle)
		prompt := ui.FooterDimStyle.Render("Kill ") +
			avatarStr + " " + titleStr +
			ui.FooterDimStyle.Render(" ? ") +
			ui.FooterKeyStyle.Render("[y]") + "es " +
			ui.FooterKeyStyle.Render("[n]") + "o"
		return ui.FooterStyle.Width(width).Render(prompt)
	case StateMinimapSettings:
		h := ui.FooterKeyStyle.Render("M") + " cycle  " +
			ui.FooterKeyStyle.Render("+/-") + " scale  " +
			ui.FooterKeyStyle.Render("esc") + " close"
		return ui.FooterStyle.Width(width).Render(h)
	default:
		hints := m.renderNormalFooterHints()
		if m.renaming {
			hints += "  " + ui.SummaryStyle.Render("renaming…")
		}
		return ui.FooterStyle.Width(width).Render(hints)
	}
}
