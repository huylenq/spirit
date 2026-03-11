package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

const debugMinimap = false

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	if m.err != nil && len(m.sessions) == 0 {
		return ui.EmptyStyle.Render("Reconnecting to daemon... (" + m.err.Error() + ")")
	}

	innerWidth := m.innerWidth()

	// Top border: usage bar as the frame's top edge
	// With corners when bordered, without corners in fullscreen
	topBorder := m.usageBar.TopBorderView(m.width, !m.inFullscreenPopup)

	// Label line: search bar replaces usage stats during search mode
	var labelLine string
	if m.state == StateSearching {
		labelLine = m.renderSearchBar(innerWidth)
	} else {
		labelLine = ui.BorderLabelStyle.Width(innerWidth).Render(m.usageBar.LabelView())
	}

	// Footer: always 1 line
	footer := m.renderFooter(innerWidth)

	// Content area: total height minus top border, label, footer (and bottom border when not fullscreen)
	contentHeight := m.height - 3
	if !m.inFullscreenPopup {
		contentHeight -= 1
	}

	// If minimap should be docked, render it first to reserve vertical space
	minimapDocked := false
	var minimapView string
	if m.shouldDockMinimap() {
		minimapView = m.minimap.ViewDocked(innerWidth)
		if minimapView != "" {
			minimapDocked = true
			contentHeight -= lipgloss.Height(minimapView)
		}
	}

	// List panel
	listWidth := m.listPanelWidth()
	previewWidth := innerWidth - listWidth

	listContent := m.list.View()
	listPanel := ui.ListPanelStyle.
		Width(listWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(listContent)

	// Set inline relay prompt on preview when active
	switch m.state {
	case StatePromptRelay:
		m.preview.SetRelayView(m.relay.View())
	case StateQueueRelay:
		m.preview.SetRelayView(m.queueRelay.View())
	default:
		m.preview.SetRelayView("")
	}

	// Queue section below preview (always visible when items pending, interactive in queue mode)
	var queueView string
	var queueHeight int
	if s, ok := m.list.SelectedItem(); ok {
		showQueue := len(s.QueuePending) > 0
		if showQueue {
			queueView = m.renderQueueSection(s, previewWidth)
			queueHeight = lipgloss.Height(queueView)
		}
	}

	// Preview panel (reduced height when queue section visible)
	previewH := contentHeight - queueHeight
	var previewContent string
	if m.state == StateBacklogPrompt && !m.backlogOverlay {
		project := ""
		if m.activeBacklogCWD != "" {
			project = filepath.Base(m.activeBacklogCWD)
		}
		previewContent = m.renderBacklogEditor(project, previewWidth, previewH)
	} else if backlog, ok := m.list.SelectedBacklog(); ok {
		previewContent = m.renderBacklogPreview(backlog)
	} else {
		previewContent = m.preview.View()
	}
	previewPanel := ui.PreviewPanelStyle.
		Width(previewWidth).
		Height(previewH).
		MaxHeight(previewH).
		Render(previewContent)

	// Combine preview + queue section in right column
	rightColumn := previewPanel
	if queueView != "" {
		rightColumn = previewPanel + "\n" + queueView
	}

	// Main content: list | right column (preview + optional queue)
	content := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, rightColumn)

	// Minimap: docked at bottom in fullscreen (inserted into layout below),
	// overlaid in normal mode
	if !minimapDocked && m.showMinimap {
		minimapStr := m.minimap.View()
		if minimapStr != "" {
			if debugMinimap {
				debugInfo := m.minimap.DebugInfo()
				debugStyled := lipgloss.NewStyle().
					Foreground(lipgloss.Color("#888888")).
					Render(debugInfo)
				minimapStr = debugStyled + "\n" + minimapStr
			}
			content = ui.OverlayBottomLeft(content, minimapStr)
		}
	}

	// Debug overlay: effects log (left) + session info (right)
	if m.debugMode {
		effectsPanel := m.renderEffectsPanel()
		sessionPanel := m.renderSessionPanel()
		combined := lipgloss.JoinHorizontal(lipgloss.Bottom, effectsPanel, " ", sessionPanel)
		if combined != "" {
			content = ui.OverlayBottomRight(content, combined, innerWidth)
		}
	}

	// Spirit animal overlay centered (lower z-order than help)
	if m.showSpiritAnimal {
		if s, ok := m.list.SelectedItem(); ok {
			overlay := ui.RenderSpiritOverlay(s.AvatarAnimalIdx, s.AvatarColorIdx, m.width, m.height)
			content = ui.OverlayCentered(content, overlay, innerWidth)
		}
	}

	// Help overlay centered
	if m.showHelp {
		content = ui.OverlayCentered(content, m.renderHelpOverlay(), innerWidth)
	}

	// Message log: full history (!) or auto-toast for suppressed messages
	if !m.debugMode {
		if m.showMessageLog {
			content = ui.OverlayBottomRight(content, m.renderMessageLog(), innerWidth)
		} else if toast := m.renderMessageToast(); toast != "" {
			content = ui.OverlayBottomRight(content, toast, innerWidth)
		}
	}

	// Command palette overlay centered
	if m.state == StatePalette {
		content = ui.OverlayCentered(content, m.palette.View(innerWidth), innerWidth)
	}

	// Preferences editor overlay centered
	if m.state == StatePrefsEditor {
		content = ui.OverlayCentered(content, m.prefsEditor.View(innerWidth), innerWidth)
	}

	// Prompt editor overlays (new session / new backlog from session context)
	if m.state == StateNewSessionPrompt {
		row := max(m.list.SelectedProjectRow(), 0)
		content = m.overlayPrompt(content, m.newSessionProject, row, innerWidth)
	}
	if m.state == StateBacklogPrompt && m.backlogOverlay {
		project := filepath.Base(m.activeBacklogCWD)
		row := m.list.SelectedItemRow()
		if row < 0 {
			row = m.list.SelectedProjectRow()
		}
		row = max(row, 0)
		content = m.overlayPrompt(content, project, row, innerWidth)
	}

	if m.flashMsg != "" {
		style := ui.FlashInfoStyle
		if m.flashIsError {
			style = ui.FlashErrorStyle
		}
		footer = style.Width(innerWidth).Render(m.flashMsg)
	} else if m.pendingChord != "" {
		footer = ui.FooterStyle.Width(innerWidth).Render(m.renderChordHints())
	}

	// Assemble inner content — manual join avoids JoinVertical width normalization
	var inner string
	if minimapDocked {
		inner = labelLine + "\n" + content + "\n" + minimapView + "\n" + footer
	} else {
		inner = labelLine + "\n" + content + "\n" + footer
	}

	if m.inFullscreenPopup {
		return topBorder + "\n" + inner
	}

	bordered := ui.AddSideBorders(inner, innerWidth)
	bottomBorder := ui.BottomBorder(m.width)
	return topBorder + "\n" + bordered + "\n" + bottomBorder
}

// renderChordHints shows the pending chord prefix and available continuations.
func (m Model) renderChordHints() string {
	prefix := ui.FooterKeyStyle.Render(m.pendingChord + "-")
	continuations := ChordsWithPrefix(m.pendingChord)
	var parts []string
	for _, c := range continuations {
		// Show the remaining keys after the prefix
		remaining := c.Keys[len(m.pendingChord):]
		parts = append(parts, ui.FooterKeyStyle.Render(remaining)+" "+c.Help)
	}
	return prefix + "  " + strings.Join(parts, "  ")
}

func (m Model) renderEffectsPanel() string {
	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("EFFECTS"))

	if len(m.globalEffects) == 0 {
		lines = append(lines, ui.ItemDetailStyle.Render("(no handled effects)"))
	} else {
		for _, ev := range m.globalEffects {
			avatar := ui.AvatarStyle(ev.ColorIdx).Render(ui.AvatarGlyph(ev.AnimalIdx))
			effect := ev.Effect
			suffix := ""
			if strings.HasSuffix(effect, claude.HookEffectDedupSuffix) {
				effect = strings.TrimSuffix(effect, claude.HookEffectDedupSuffix)
				suffix += ui.ItemDetailStyle.Render(claude.HookEffectDedupSuffix)
			}
			if ev.Count > 1 {
				suffix += ui.ItemDetailStyle.Render(fmt.Sprintf(" ×%d", ev.Count))
			}
			lines = append(lines,
				avatar+" "+ui.ItemDetailStyle.Render(ev.Time+" "+ev.HookType+": ")+ui.TranscriptMsgStyle.Render(effect)+suffix)
		}
	}

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderSessionPanel() string {
	s, ok := m.list.SelectedItem()
	if !ok {
		return ""
	}

	line := func(label, v string) string {
		if v == "" {
			v = "(empty)"
		}
		return ui.ItemDetailStyle.Render(label+": ") + ui.TranscriptMsgStyle.Render(v)
	}

	var lines []string
	lines = append(lines, ui.DebugTitleStyle.Render("SESSION"))
	lines = append(lines, line("PaneID", s.PaneID))
	lines = append(lines, line("SessionID", s.SessionID))
	lines = append(lines, line("Status", s.Status.String()))
	lines = append(lines, line("CustomTitle", s.CustomTitle))
	lines = append(lines, line("Headline", s.Headline))
	lines = append(lines, line("FirstMsg", debugTruncate(s.FirstMessage, 40)))
	lines = append(lines, line("LastUserMsg", debugTruncate(s.LastUserMessage, 40)))
	lines = append(lines, line("PermMode", s.PermissionMode))
	lines = append(lines, line("Project", s.Project))
	lines = append(lines, line("CWD", s.CWD))
	lines = append(lines, line("GitBranch", s.GitBranch))

	// Usage bar info
	lines = append(lines, ui.ItemDetailStyle.Render("--- usage bar ---"))
	if m.usageBar.HasData() {
		lines = append(lines, line("SessionPct", fmt.Sprintf("%d%%", m.usageBar.SessionPct())))
		lines = append(lines, line("Resets", m.usageBar.Resets()))
		lines = append(lines, line("RippleActive", fmt.Sprintf("%v", m.usageBar.RippleActive())))
	} else {
		lines = append(lines, ui.ItemDetailStyle.Render("(no usage data yet)"))
	}

	// Synthesize cache info
	if s.SessionID != "" {
		cached := claude.ReadCachedSummary(s.SessionID)
		sMod, tMod, fresh := claude.SummaryCacheInfo(s.SessionID)
		lines = append(lines, ui.ItemDetailStyle.Render("--- synthesize result cache ---"))
		if cached != nil {
			const jsonWrap = 50
			data, _ := json.MarshalIndent(cached, "", "  ")
			for _, jsonLine := range strings.Split(string(data), "\n") {
				for len(jsonLine) > jsonWrap {
					lines = append(lines, ui.HighlightJSON(jsonLine[:jsonWrap]))
					jsonLine = "    " + jsonLine[jsonWrap:] // indent continuation
				}
				lines = append(lines, ui.HighlightJSON(jsonLine))
			}
		} else {
			lines = append(lines, ui.ItemDetailStyle.Render("(no cached synthesize)"))
		}
		freshStr := "stale"
		if fresh {
			freshStr = "fresh"
		}
		if sMod == "" {
			freshStr = "n/a"
		}
		lines = append(lines, line("SynthMod", sMod))
		lines = append(lines, line("TranscriptMod", tMod))
		lines = append(lines, line("CacheFresh", freshStr))
	}

	// Jump trail
	sessionByPane := make(map[string]claude.ClaudeSession)
	for _, sess := range m.sessions {
		sessionByPane[sess.PaneID] = sess
	}
	lines = append(lines, ui.ItemDetailStyle.Render("--- jump trail ---"))
	lines = append(lines, line("Cursor", fmt.Sprintf("%d/%d", m.jumpCursor, len(m.jumpTrail))))
	for i, pid := range m.jumpTrail {
		marker := ui.ItemDetailStyle.Render("  ")
		if i == m.jumpCursor {
			marker = ui.ItemDetailStyle.Render("> ")
		}
		var avatar string
		if sess, ok := sessionByPane[pid]; ok {
			avatar = ui.AvatarStyle(sess.AvatarColorIdx).Render(ui.AvatarGlyph(sess.AvatarAnimalIdx))
		} else {
			avatar = ui.ItemDetailStyle.Render("?")
		}
		lines = append(lines, marker+ui.ItemDetailStyle.Render(fmt.Sprintf("[%d] ", i))+avatar)
	}
	if m.jumpCursor >= len(m.jumpTrail) {
		lines = append(lines, ui.ItemDetailStyle.Render("> (head)"))
	}

	return ui.DebugOverlayStyle.Render(strings.Join(lines, "\n"))
}

func debugTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// renderQueueSection renders the queue items below the preview panel.
// Always visible when items are pending; interactive when in StateQueueRelay.
func (m Model) renderQueueSection(s claude.ClaudeSession, width int) string {
	items := s.QueuePending
	inQueueMode := m.state == StateQueueRelay
	innerWidth := width - 2 // padding

	var lines []string

	// Header
	header := fmt.Sprintf("❮ queued (%d)", len(items))
	lines = append(lines, ui.QueuePromptStyle.Render(header))

	// Items (capped at ~30% of preview height, scrollable later if needed)
	maxItems := max((m.height-6)*30/100, 3)
	for i, msg := range items {
		if i >= maxItems {
			lines = append(lines, ui.ItemDetailStyle.Render(fmt.Sprintf("  …+%d more", len(items)-maxItems)))
			break
		}
		prefix := fmt.Sprintf("  %d. ", i+1)
		maxMsgWidth := innerWidth - lipgloss.Width(prefix)
		truncated := ansi.Truncate(msg, maxMsgWidth, "…")
		if inQueueMode && i == m.queueCursor {
			// Highlighted item
			lines = append(lines, ui.SelectedBgStyle.Render(prefix+truncated+strings.Repeat(" ", max(innerWidth-lipgloss.Width(prefix+truncated), 0))))
		} else {
			lines = append(lines, ui.ItemDetailStyle.Render(prefix+truncated))
		}
	}

	return strings.Join(lines, "\n")
}

// hint formats a single key hint for the footer bar.
func hint(k, desc string) string {
	return ui.FooterKeyStyle.Render(k) + " " + desc
}

// renderNormalFooterHints builds a context-sensitive footer based on the selected session.
func (m Model) renderNormalFooterHints() string {
	var parts []string

	// Backlog-specific footer
	if m.list.IsBacklogSelected() {
		parts = append(parts, hint("j/k", "nav"))
		parts = append(parts, hint("enter", "submit"), hint("b", "edit"), hint("e", "$EDITOR"), hint("d", "delete"))
		parts = append(parts, hint("?", "help"), hint("q", "quit"))
		return strings.Join(parts, "  ")
	}

	// Project-level footer
	if m.list.SelectionLevel() == ui.LevelProject {
		if _, ok := m.list.SelectedProject(); ok {
			parts = append(parts, hint("j/k", "nav"), hint("b", "new backlog"), hint("l", "enter"))
			parts = append(parts, hint("?", "help"), hint("q", "quit"))
			return strings.Join(parts, "  ")
		}
	}

	s, hasSelection := m.list.SelectedItem()

	// Always show nav
	parts = append(parts, hint("j/k", "nav"))

	if !hasSelection {
		parts = append(parts, hint("/", "search"), hint("g", "group"), hint("m", "minimap"), hint("?", "help"), hint("q", "quit"))
		return strings.Join(parts, "  ")
	}

	parts = append(parts, hint("enter", "switch"), hint(">", "send"), hint("<", "queue"))

	if s.LaterBookmarkID != "" {
		parts = append(parts, hint("w", "unlater"))
	} else {
		switch s.Status {
		case claude.StatusUserTurn:
			if !s.CommitDonePending {
				parts = append(parts, hint("c", "commit"), hint("C", "commit+done"))
			}
			parts = append(parts, hint("w", "later"), hint("W", "later+kill"))
		case claude.StatusAgentTurn:
			parts = append(parts, hint("w", "later"), hint("W", "later+kill"))
		}
	}

	parts = append(parts, hint("d", "kill"), hint("b", "new backlog"))

	if m.showMinimap {
		parts = append(parts, hint("H/J/K/L", "spatial"))
	}

	parts = append(parts, hint("?", "help"), hint("q", "quit"))
	return strings.Join(parts, "  ")
}

// renderHelpOverlay returns a styled help overlay showing all keybindings.
func (m Model) renderHelpOverlay() string {
	title := ui.HelpTitleStyle.Render("Keybindings")
	nav := ui.HelpGroupStyle.Render("Navigation")
	actions := ui.HelpGroupStyle.Render("Actions")
	toggles := ui.HelpGroupStyle.Render("Toggles & Copy")

	col1 := strings.Join([]string{
		nav,
		hint("j/k", "up/down"),
		hint("enter", "switch to pane"),
		hint("/", "search"),
		hint("ctrl+d/u", "scroll preview"),
		hint("ctrl+j/k", "next/prev message"),
		hint("alt+h/l", "resize list"),
	}, "\n")

	col2 := strings.Join([]string{
		actions,
		hint(">", "send to session"),
		hint("<", "queue message"),
		hint("w", "later"),
		hint("W", "later + kill"),
		hint("d", "kill + close pane"),
		hint("s", "synthesize"),
		hint("S", "synthesize all"),
		hint("R", "rename window"),
		hint("c", "commit"),
		hint("C", "commit + done"),
		hint("r", "refresh preview"),
	}, "\n")

	chordHints := make([]string, 0, len(Chords))
	for _, c := range Chords {
		// Format "ys" as "y s" for readability
		keys := strings.Join(strings.Split(c.Keys, ""), " ")
		chordHints = append(chordHints, hint(keys, c.Help))
	}

	col3Parts := []string{
		toggles,
		hint("m", "minimap"),
		hint("M", "minimap settings"),
		hint("P", "preferences"),
		hint("g", "group by project"),
		hint("t", "toggle transcript"),
		hint("z", "fullscreen toggle"),
		hint("!", "message log"),
	}
	col3Parts = append(col3Parts, chordHints...)
	col3Parts = append(col3Parts, "", ui.FooterDimStyle.Render("press ? or esc to close"))
	col3 := strings.Join(col3Parts, "\n")

	columns := lipgloss.JoinHorizontal(lipgloss.Top, col1, "    ", col2, "    ", col3)
	body := title + "\n\n" + columns
	return ui.HelpOverlayStyle.Render(body)
}

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

// renderBacklogEditor renders the backlog textarea editor inline in the preview panel.
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

// renderBacklogPreview renders the full backlog item body as plain text for the preview panel.
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

func (m Model) renderFooter(width int) string {
	switch m.state {
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
