package app

import (
	"encoding/json"
	"fmt"
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

	// Label line: usage stats right-aligned below the top border
	labelLine := ui.BorderLabelStyle.Width(innerWidth).Render(m.usageBar.LabelView())

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
		minimapView = m.minimap.View()
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
	previewContent := m.preview.View()
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

	// Help overlay centered
	if m.showHelp {
		content = ui.OverlayCentered(content, m.renderHelpOverlay(), innerWidth)
	}

	// Command palette overlay centered
	if m.state == StatePalette {
		content = ui.OverlayCentered(content, m.palette.View(innerWidth), innerWidth)
	}

	// New session prompt editor overlay — positioned right next to the project name label
	if m.state == StateNewSessionPrompt {
		// Column: right after "📁 project-name" text + padding + small gap
		labelWidth := lipgloss.Width(ui.IconFolder+" "+m.newSessionProject) + 3 // 1 left pad + 1 right pad + 1 gap
		overlayWidth := min(innerWidth-labelWidth, 72)
		overlayView := m.promptEditor.View(m.newSessionProject, overlayWidth)
		row := m.list.SelectedProjectRow()
		if row < 0 {
			row = 0
		}
		content = ui.OverlayAt(content, overlayView, row, labelWidth)
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

	parts = append(parts, hint("d", "kill"))

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
		hint("g", "group by project"),
		hint("t", "toggle transcript"),
		hint("z", "fullscreen toggle"),
	}
	col3Parts = append(col3Parts, chordHints...)
	col3Parts = append(col3Parts, "", ui.FooterDimStyle.Render("press ? or esc to close"))
	col3 := strings.Join(col3Parts, "\n")

	columns := lipgloss.JoinHorizontal(lipgloss.Top, col1, "    ", col2, "    ", col3)
	body := title + "\n\n" + columns
	return ui.HelpOverlayStyle.Render(body)
}

func (m Model) renderFooter(width int) string {
	switch m.state {
	case StatePalette:
		h := ui.FooterKeyStyle.Render("enter") + " execute  " +
			ui.FooterKeyStyle.Render("↑/↓") + " navigate  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateSearching:
		filterView := m.search.View()
		hint := ui.FooterKeyStyle.Render("C-j/k") + ui.FooterDimStyle.Render(" navigate  ") +
			ui.FooterKeyStyle.Render("enter") + ui.FooterDimStyle.Render(" confirm  ") +
			ui.FooterKeyStyle.Render("esc") + ui.FooterDimStyle.Render(" clear")
		hintWidth := lipgloss.Width(hint)
		filterWidth := lipgloss.Width(filterView)
		gap := width - filterWidth - hintWidth
		if gap < 2 {
			return filterView
		}
		return filterView + strings.Repeat(" ", gap) + hint
	case StatePromptRelay:
		h := ui.FooterKeyStyle.Render("enter") + " send  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateNewSessionPrompt:
		h := ui.FooterKeyStyle.Render("enter") + " send  " +
			ui.FooterKeyStyle.Render("alt+enter") + " newline  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateQueueRelay:
		h := ui.FooterKeyStyle.Render("enter") + " append  " +
			ui.FooterKeyStyle.Render("↑↓") + " select  " +
			ui.FooterKeyStyle.Render("ctrl+d") + " remove  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
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
