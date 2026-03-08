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

	// Header: always 1 line
	header := ui.RenderHeader(m.sessions, m.width)

	// Footer: always 1 line
	footer := m.renderFooter()

	// Content area gets the remaining height
	contentHeight := m.height - 2 // 1 header + 1 footer

	// List panel
	listWidth := max(m.width*m.listWidthPct/100, 20)
	previewWidth := m.width - listWidth

	listContent := m.list.View()
	listPanel := ui.ListPanelStyle.
		Width(listWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(listContent)

	// Set inline relay prompt on preview when active
	if m.state == StatePromptRelay {
		m.preview.SetRelayView(m.relay.View())
	} else {
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
			content = ui.OverlayBottomRight(content, debugStr, m.width)
		}
	}

	if m.flashMsg != "" {
		style := ui.FlashInfoStyle
		if m.flashIsError {
			style = ui.FlashErrorStyle
		}
		footer = style.Width(m.width).Render(m.flashMsg)
	} else if m.pendingChord != "" {
		footer = ui.FooterStyle.Width(m.width).Render(m.renderChordHints())
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
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

	// Summary cache info
	if s.SessionID != "" {
		cached := claude.ReadCachedSummary(s.SessionID)
		sMod, tMod, fresh := claude.SummaryCacheInfo(s.SessionID)
		lines = append(lines, muted.Render("--- summary cache ---"))
		if cached != nil {
			lines = append(lines, line("Objective", debugTruncate(cached.Objective, 40)))
			lines = append(lines, line("CacheHL", debugTruncate(cached.Headline, 40)))
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

func (m Model) renderFooter() string {
	switch m.state {
	case StateFiltering:
		return m.filter.View()
	case StateDeferPrompt:
		return m.deferPrompt.View()
	case StatePromptRelay:
		hint := ui.FooterKeyStyle.Render("enter") + " send  " +
			ui.FooterKeyStyle.Render("esc") + " cancel  " +
			ui.FooterKeyStyle.Render("ctrl+j/k") + " navigate"
		return ui.FooterStyle.Width(m.width).Render(hint)
	case StateKillConfirm:
		prompt := ui.FooterDimStyle.Render("Kill ") +
			ui.FooterDangerStyle.Render(m.killTargetTitle) +
			ui.FooterDimStyle.Render(" ? ") +
			ui.FooterKeyStyle.Render("[y]") + "es " +
			ui.FooterKeyStyle.Render("[n]") + "o"
		return ui.FooterStyle.Width(m.width).Render(prompt)
	default:
		hints := m.help.View(Keys)
		if m.renaming {
			hints += "  " + ui.SummaryStyle.Render("renaming…")
		}
		if n := m.list.SummaryLoadingCount(); n > 0 {
			hints += "  " + ui.SummaryStyle.Render(fmt.Sprintf("summarizing %d…", n))
		}
		return ui.FooterStyle.Width(m.width).Render(hints)
	}
}
