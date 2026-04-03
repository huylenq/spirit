package app

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/ui"
)

const debugMinimap = false

var autoJumpDimStyle = lipgloss.NewStyle().Foreground(ui.ColorMuted)
var autoJumpOnStyle = lipgloss.NewStyle().Foreground(ui.ColorAutoJump)
var focusModeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))

// autoJumpIndicator renders the autojump glyph for the header label line.
// Solid flash when ON, hollow outline when OFF. Shows text briefly after toggling.
func (m Model) autoJumpIndicator() string {
	on := m.autoJumpOn
	if on {
		glyph := autoJumpOnStyle.Render(ui.IconBolt)
		if time.Now().Before(m.autoJumpTextUntil) {
			return glyph + " " + autoJumpOnStyle.Render("AUTOJUMP ON")
		}
		return glyph
	}
	glyph := autoJumpDimStyle.Render(ui.IconBoltOutline)
	if time.Now().Before(m.autoJumpTextUntil) {
		return glyph + " " + autoJumpDimStyle.Render("AUTOJUMP OFF")
	}
	return glyph
}

// viewInner renders the content area (sidebar + detail panels) without borders, label, or footer.
// Used by the destroyer to snapshot the current TUI state for decomposition into particles.
func (m Model) viewInner() string {
	innerWidth := m.innerWidth()
	contentHeight := m.contentHeight() - 1 // subtract divider line, matching normal View() path
	sidebarWidth := m.sidebarPanelWidth()
	detailWidth := innerWidth - sidebarWidth - m.copilotDockedWidth()

	sidebarContent := m.sidebar.View()
	sidebarPanel := ui.SidebarPanelStyle.
		Width(sidebarWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(sidebarContent)

	var detailContent string
	if m.sidebar.IsAllQuiet() {
		detailContent = m.detail.ViewAllQuiet(m.allQuietCounts())
	} else {
		detailContent = m.detail.View()
	}
	detailPanel := ui.DetailPanelStyle.
		Width(detailWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(detailContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarPanel, detailPanel)
}

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
		left := m.autoJumpIndicator()
		if m.sidebar.FocusMode() {
			left += " " + focusModeStyle.Render(ui.IconFlag+" FOCUS")
		}
		right := m.usageBar.LabelView()
		leftW := lipgloss.Width(left)
		rightW := lipgloss.Width(right)
		gap := innerWidth - leftW - rightW - 1 // -1 for right padding
		if gap < 0 {
			gap = 0
		}
		labelLine = " " + left + strings.Repeat(" ", gap) + right + " "
	}

	// Footer: always 1 line
	footer := m.renderFooter(innerWidth)

	// Content area: total height minus top border, label, footer (and bottom border when not fullscreen)
	contentHeight := m.contentHeight()

	// Destroyer mode: replace entire content area with particle physics
	if m.state == StateDestroyer && m.destroyer != nil {
		// Subtract 1 for the divider line (same as the normal path at line ~110)
		destroyerH := contentHeight - 1
		content := m.destroyer.View()
		// Pad to fill content height
		lines := strings.Split(content, "\n")
		for len(lines) < destroyerH {
			lines = append(lines, strings.Repeat(" ", innerWidth))
		}
		content = strings.Join(lines[:destroyerH], "\n")

		var inner string
		divider := ui.FooterDivider(innerWidth)
		inner = labelLine + "\n" + content + "\n" + divider + "\n" + footer

		if m.inFullscreenPopup {
			return topBorder + "\n" + inner
		}
		bordered := ui.AddSideBorders(inner, innerWidth)
		bottomBorder := ui.BottomBorder(m.width)
		return topBorder + "\n" + bordered + "\n" + bottomBorder
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

	// Main content: either sidebar+detail (default) or workqueue+detail
	var content string
	if m.viewMode == ViewWorkQueue {
		content = m.viewWorkQueueLayout(innerWidth, contentHeight)
	} else {
		content = m.viewSidebarLayout(innerWidth, contentHeight)
	}

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

	// Settings overlay centered
	if m.state == StatePrefsEditor {
		content = ui.OverlayCentered(content, m.renderSettingsOverlay(), innerWidth)
	}

	// Path input overlay for A (new session at typed path) — same pivot as new-session prompt
	if m.state == StateNewSessionPathInput {
		row := max(m.sidebar.SelectedProjectRow(), 0)
		col := m.sidebarPanelWidth()
		overlayWidth := min(innerWidth-col, 60)
		content = ui.OverlayAt(content, m.renderPathInputOverlay(overlayWidth), row, col)
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

	// Copilot floating overlay (highest z-order — renders on top of everything)
	// Only rendered in float mode. Docked mode is rendered as a layout column above.
	if m.copilotVisible && m.copilotMode == CopilotModeFloat {
		adjustMode := m.state == StateAdjustCopilot
		row, col, overlayW, maxOverlayH := m.copilotFloatGeometry(innerWidth, contentHeight)

		focused := m.state == StateCopilot || m.state == StateCopilotConfirm
		inputView := ""
		if !adjustMode {
			inputView = m.copilotInput.View() // always show (dimmed when unfocused)
		}
		overlay := ui.RenderCopilotOverlay(
			m.copilot.Messages(), inputView,
			overlayW, maxOverlayH,
			m.copilot.ScrollOffset(), m.copilot.Streaming(),
			m.copilot.StreamingCursor(),
			m.copilot.PendingTool(),
			focused || adjustMode,
			adjustMode,
		)

		// Refine row clamp using actual rendered height (may be shorter than maxOverlayH).
		overlayH := lipgloss.Height(overlay)
		row = min(row, contentHeight-overlayH)
		row = max(row, 0)

		content = ui.OverlayAt(content, overlay, row, col)
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
