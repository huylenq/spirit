package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

const debugMinimap = false

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	if m.err != nil {
		return ui.EmptyStyle.Render("Error: " + m.err.Error())
	}

	innerWidth := m.width
	if !m.inFullscreenPopup {
		innerWidth -= 2 // inside left/right border chars
	}

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

	// List panel
	listWidth := max(innerWidth*m.listWidthPct/100, 20)
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

	// Preview panel
	previewContent := m.preview.View()
	previewPanel := ui.PreviewPanelStyle.
		Width(previewWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(previewContent)

	// Main content: list | preview
	content := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, previewPanel)

	// Overlay minimap at bottom-left if enabled
	if m.showMinimap {
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

	// Debug overlay at bottom-right
	if m.debugMode {
		if debugStr := m.renderDebugOverlay(); debugStr != "" {
			content = ui.OverlayBottomRight(content, debugStr, innerWidth)
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
	inner := labelLine + "\n" + content + "\n" + footer

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

func (m Model) renderDebugOverlay() string {
	s, ok := m.list.SelectedItem()
	if !ok {
		return ""
	}

	title := ui.DebugTitleStyle.Render("DEBUG")
	muted := lipgloss.NewStyle().Foreground(ui.ColorMuted)
	val := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#e5e7eb"})

	line := func(label, v string) string {
		if v == "" {
			v = "(empty)"
		}
		return muted.Render(label+": ") + val.Render(v)
	}

	var lines []string
	lines = append(lines, title)
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
	lines = append(lines, muted.Render("--- usage bar ---"))
	if m.usageBar.HasData() {
		lines = append(lines, line("SessionPct", fmt.Sprintf("%d%%", m.usageBar.SessionPct())))
		lines = append(lines, line("Resets", m.usageBar.Resets()))
		lines = append(lines, line("RippleActive", fmt.Sprintf("%v", m.usageBar.RippleActive())))
	} else {
		lines = append(lines, muted.Render("(no usage data yet)"))
	}

	// Summary cache info
	if s.SessionID != "" {
		cached := claude.ReadCachedSummary(s.SessionID)
		sMod, tMod, fresh := claude.SummaryCacheInfo(s.SessionID)
		lines = append(lines, muted.Render("--- summary cache ---"))
		if cached != nil {
			lines = append(lines, line("Objective", debugTruncate(cached.Objective, 40)))
			lines = append(lines, line("CacheHL", debugTruncate(cached.Headline, 40)))
			lines = append(lines, line("ProblemType", cached.ProblemType))
			lines = append(lines, line("InputWords", fmt.Sprintf("%d", cached.InputWords)))
		} else {
			lines = append(lines, muted.Render("(no cached summary)"))
		}
		freshStr := "stale"
		if fresh {
			freshStr = "fresh"
		}
		if sMod == "" {
			freshStr = "n/a"
		}
		lines = append(lines, line("SummaryMod", sMod))
		lines = append(lines, line("TranscriptMod", tMod))
		lines = append(lines, line("CacheFresh", freshStr))
	}

	body := strings.Join(lines, "\n")
	return ui.DebugOverlayStyle.Render(body)
}

func debugTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
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

	switch s.Status {
	case claude.StatusDone:
		if !s.CommitDonePending {
			parts = append(parts, hint("c", "commit"), hint("C", "commit+done"))
		}
		parts = append(parts, hint("w", "later"), hint("W", "later+kill"))
	case claude.StatusWorking:
		parts = append(parts, hint("w", "later"), hint("W", "later+kill"))
	case claude.StatusLater:
		parts = append(parts, hint("w", "unlater"))
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
	case StateQueueRelay:
		h := ui.FooterKeyStyle.Render("enter") + " queue  " +
			ui.FooterKeyStyle.Render("esc") + " cancel"
		return ui.FooterStyle.Width(width).Render(h)
	case StateKillConfirm:
		prompt := ui.FooterDimStyle.Render("Kill ") +
			ui.FooterDangerStyle.Render(m.killTargetTitle) +
			ui.FooterDimStyle.Render(" ? ") +
			ui.FooterKeyStyle.Render("[y]") + "es " +
			ui.FooterKeyStyle.Render("[n]") + "o"
		return ui.FooterStyle.Width(width).Render(prompt)
	default:
		hints := m.renderNormalFooterHints()
		if m.renaming {
			hints += "  " + ui.SummaryStyle.Render("renaming…")
		}
		return ui.FooterStyle.Width(width).Render(hints)
	}
}
