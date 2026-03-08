package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

func (m Model) renderFooter() string {
	switch m.state {
	case StateFiltering:
		return m.filter.View()
	case StateDeferPrompt:
		return m.deferPrompt.View()
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
