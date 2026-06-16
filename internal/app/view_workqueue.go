package app

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ui"
)

// refreshSessions updates both the sidebar and work queue with the current
// session list. Use this instead of calling sidebar.SetItems directly.
func (m *Model) refreshSessions() {
	m.sidebar.SetItems(m.sessions)
	m.syncWorkQueue()
}

// selectByPaneID selects a session in both the sidebar and work queue.
// Returns true if the sidebar found the pane.
func (m *Model) selectByPaneID(paneID string) bool {
	m.workQueue.SelectByPaneID(paneID)
	return m.sidebar.SelectByPaneID(paneID)
}

// syncWorkQueue updates the work queue model with the current session list
// and autojump target. Respects focus mode by only passing flagged sessions.
func (m *Model) syncWorkQueue() {
	autoJumpID := ""
	if m.autoJumpOn {
		autoJumpID = m.sidebar.AutoJumpTargetFromCursor()
	}
	sessions := m.sessions
	if m.sidebar.FocusMode() {
		filtered := make([]claude.ClaudeSession, 0, len(sessions))
		for _, s := range sessions {
			if m.sidebar.IsEffectivelyFlagged(s) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}
	m.workQueue.SetItems(sessions, autoJumpID)
}

// reconcileWorkQueueSelection rebuilds the queue and points the cursor at the
// sidebar's selection if it's in the queue, else the top of the queue.
func (m *Model) reconcileWorkQueueSelection() tea.Cmd {
	m.syncWorkQueue()
	sel, hasSel := m.sidebar.SelectedItem()
	if !hasSel || !m.workQueue.SelectByPaneID(sel.PaneID) {
		m.workQueue.SelectTop()
	}
	return m.syncWorkQueueSelection()
}

// syncWorkQueueSelection syncs the sidebar's selection to the work queue's
// current cursor position so the detail panel shows the right session.
func (m *Model) syncWorkQueueSelection() tea.Cmd {
	s, ok := m.workQueue.SelectedItem()
	if !ok {
		return nil
	}
	if !m.sidebar.SelectByPaneID(s.PaneID) {
		return nil
	}
	sel, ok := m.sidebar.SelectedItem()
	if !ok {
		return nil
	}
	return tea.Batch(m.fetchForSelection(sel, true)...)
}

// allQuietCounts returns the counts for the all-quiet detail view.
func (m Model) allQuietCounts() ui.AllQuietCounts {
	return ui.AllQuietCounts{
		ClaudingSessions: m.sidebar.ClaudingSessions(),
		Later:            m.sidebar.LaterCount(),
		Backlog:          m.sidebar.BacklogCount(),
	}
}

// renderAllQuietPanel renders the full-bleed all-quiet detail panel at the
// given width. DetailPanelStyle pads 1 col each side, so the scene is rendered
// at detailWidth-2 to center within the full panel rather than the stale
// (with-sidebar) detail width cached on the detail model.
func (m Model) renderAllQuietPanel(detailWidth, contentHeight int) string {
	return ui.DetailPanelStyle.
		Width(detailWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		Render(m.detail.ViewAllQuietSized(detailWidth-2, contentHeight, m.allQuietCounts()))
}

// renderDockedCopilot renders the copilot docked panel if visible, or empty string.
func (m Model) renderDockedCopilot(copilotDockedW, contentHeight int) string {
	if copilotDockedW <= 0 {
		return ""
	}
	focused := m.state == StateCopilot || m.state == StateCopilotConfirm
	inputView := m.copilotInput.View()
	return ui.RenderCopilotPanel(
		m.copilot.Messages(), inputView,
		copilotDockedW, contentHeight,
		m.copilot.ScrollOffset(), m.copilot.Streaming(),
		m.copilot.StreamingCursor(),
		m.copilot.PendingTool(),
		focused,
	)
}

// viewSidebarLayout renders the traditional sidebar + detail panel layout.
func (m Model) viewSidebarLayout(innerWidth, contentHeight int) string {
	copilotDockedW := m.copilotDockedWidth()

	// All-quiet: hide the sidebar so the mobile animation gets the full width.
	if m.sidebar.IsAllQuiet() {
		detailPanel := m.renderAllQuietPanel(innerWidth-copilotDockedW, contentHeight)
		copilotDockedPanel := m.renderDockedCopilot(copilotDockedW, contentHeight)
		if copilotDockedPanel != "" {
			return lipgloss.JoinHorizontal(lipgloss.Top, detailPanel, copilotDockedPanel)
		}
		return detailPanel
	}

	sidebarWidth := m.sidebarPanelWidth()
	detailWidth := innerWidth - sidebarWidth - copilotDockedW

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
	} else if m.sidebar.IsAllQuiet() {
		detailContent = m.detail.ViewAllQuiet(m.allQuietCounts())
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

	copilotDockedPanel := m.renderDockedCopilot(copilotDockedW, contentHeight)
	if copilotDockedPanel != "" {
		return lipgloss.JoinHorizontal(lipgloss.Top, sidebarPanel, rightColumn, copilotDockedPanel)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarPanel, rightColumn)
}

// viewWorkQueueLayout renders the work queue strip + full-width detail layout.
func (m Model) viewWorkQueueLayout(innerWidth, contentHeight int) string {
	copilotDockedW := m.copilotDockedWidth()
	detailWidth := innerWidth - copilotDockedW

	// Work queue strip (fixed height)
	workQueueView := m.workQueue.View(&m.sidebar)

	// Detail panel fills the remaining height
	detailH := contentHeight - ui.WorkQueueHeight
	var detailContent string
	if _, ok := m.workQueue.SelectedItem(); !ok {
		detailContent = m.detail.ViewAllQuiet(m.allQuietCounts())
	} else {
		detailContent = m.detail.View()
	}
	detailPanel := ui.DetailPanelStyle.
		Width(detailWidth).
		Height(detailH).
		MaxHeight(detailH).
		Render(detailContent)

	// Stack: work queue on top, detail below
	mainColumn := workQueueView + "\n" + detailPanel

	copilotDockedPanel := m.renderDockedCopilot(copilotDockedW, contentHeight)
	if copilotDockedPanel != "" {
		return lipgloss.JoinHorizontal(lipgloss.Top, mainColumn, copilotDockedPanel)
	}
	return mainColumn
}
