package app

import (
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
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
	contentHeight := m.contentHeight()

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
	// Divider line between content and footer (shown when minimap isn't docked above footer)
	if !minimapDocked {
		contentHeight -= 1
	}

	// Set relay views before rendering panels (sidebar.View() and detail.View() consume them)
	var tagSessionID, tagInputView, tagBacklogID string
	if m.state == StateTagRelay {
		if s, ok := m.sidebar.SelectedItem(); ok {
			tagSessionID = s.SessionID
			tagInputView = m.tagRelay.View()
		} else if b, ok := m.sidebar.SelectedBacklog(); ok {
			tagBacklogID = b.ID
			tagInputView = m.tagRelay.View()
		}
	}
	m.sidebar.SetInlineTagInput(tagSessionID, tagInputView)
	m.sidebar.SetInlineTagBacklogInput(tagBacklogID, tagInputView)

	switch m.state {
	case StatePromptRelay:
		m.detail.SetRelayView(m.relay.View())
	case StateQueueRelay:
		m.detail.SetRelayView(m.queueRelay.View())
	default:
		m.detail.SetRelayView("")
	}

	// Sidebar panel
	sidebarWidth := m.sidebarPanelWidth()
	detailWidth := innerWidth - sidebarWidth

	sidebarContent := m.sidebar.View()
	sidebarPanel := ui.SidebarPanelStyle.
		Width(sidebarWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(sidebarContent)

	// Queue section below preview (always visible when items pending, interactive in queue mode)
	var queueView string
	var queueHeight int
	if s, ok := m.sidebar.SelectedItem(); ok {
		showQueue := len(s.QueuePending) > 0
		if showQueue {
			queueView = m.renderQueueSection(s, detailWidth)
			queueHeight = lipgloss.Height(queueView)
		}
	}

	// Detail panel (reduced height when queue section visible)
	detailH := contentHeight - queueHeight
	var detailContent string
	if m.state == StateBacklogPrompt && !m.backlogOverlay {
		project := ""
		if m.activeBacklogCWD != "" {
			project = filepath.Base(m.activeBacklogCWD)
		}
		detailContent = m.renderBacklogEditor(project, detailWidth, detailH)
	} else if backlog, ok := m.sidebar.SelectedBacklog(); ok {
		detailContent = m.renderBacklogPreview(backlog, detailWidth, detailH, m.backlogScroll)
	} else {
		detailContent = m.detail.View()
	}
	detailPanel := ui.DetailPanelStyle.
		Width(detailWidth).
		Height(detailH).
		MaxHeight(detailH).
		Render(detailContent)

	// Combine preview + queue section in right column
	rightColumn := detailPanel
	if queueView != "" {
		rightColumn = detailPanel + "\n" + queueView
	}

	// Main content: list | right column (preview + optional queue)
	content := lipgloss.JoinHorizontal(lipgloss.Top, sidebarPanel, rightColumn)

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
		usagePanel := m.renderUsageDebugPanel()
		synthPanel := m.renderSynthesizeDebugPanel()
		jumpPanel := m.renderJumpTrailPanel()
		combined := lipgloss.JoinHorizontal(lipgloss.Bottom, effectsPanel, " ", sessionPanel, " ", usagePanel, " ", synthPanel, " ", jumpPanel)
		if combined != "" {
			content = ui.OverlayBottomRight(content, combined, innerWidth)
		}
	}

	// Spirit animal overlay centered (lower z-order than help)
	if m.showSpiritAnimal {
		if s, ok := m.sidebar.SelectedItem(); ok {
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

	// Macro palette anchored next to sidebar selection, at the selected item's row
	if m.state == StateMacro {
		row := m.sidebar.SelectedItemRow()
		if row < 0 {
			row = 0
		}
		col := m.sidebarPanelWidth() // just after the sidebar's right border
		content = ui.OverlayAt(content, m.renderMacroPalette(), row, col)
	}

	// Macro editor overlay centered
	if m.state == StateMacroEdit {
		content = ui.OverlayCentered(content, m.macroEditor.View(innerWidth), innerWidth)
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
		row := max(m.sidebar.SelectedProjectRow(), 0)
		content = m.overlayPrompt(content, m.newSessionProject, row, innerWidth)
	}
	if m.state == StateBacklogPrompt && m.backlogOverlay {
		project := filepath.Base(m.activeBacklogCWD)
		row := m.sidebar.SelectedItemRow()
		if row < 0 {
			row = m.sidebar.SelectedProjectRow()
		}
		row = max(row, 0)
		content = m.overlayPrompt(content, project, row, innerWidth)
	}


	// Assemble inner content — manual join avoids JoinVertical width normalization
	var inner string
	if minimapDocked {
		inner = labelLine + "\n" + content + "\n" + minimapView + "\n" + footer
	} else {
		divider := ui.FooterDivider(innerWidth)
		inner = labelLine + "\n" + content + "\n" + divider + "\n" + footer
	}

	if m.inFullscreenPopup {
		return topBorder + "\n" + inner
	}

	bordered := ui.AddSideBorders(inner, innerWidth)
	bottomBorder := ui.BottomBorder(m.width)
	return topBorder + "\n" + bordered + "\n" + bottomBorder
}
