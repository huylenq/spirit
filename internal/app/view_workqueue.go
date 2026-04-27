package app

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
// and autojump target.
func (m *Model) syncWorkQueue() {
	autoJumpID := ""
	if m.autoJumpOn {
		autoJumpID = m.sidebar.AutoJumpTargetFromCursor()
	}
	m.workQueue.SetItems(m.sessions, autoJumpID)
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
		Clauding: m.sidebar.ClaudingCount(),
		Later:    m.sidebar.LaterCount(),
		Backlog:  m.sidebar.BacklogCount(),
	}
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
	sidebarWidth := m.sidebarPanelWidth()
	copilotDockedW := m.copilotDockedWidth()
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
	if m.sidebar.IsAllQuiet() {
		detailContent = m.detail.ViewAllQuiet(m.allQuietCounts())
	} else if _, ok := m.workQueue.SelectedItem(); !ok {
		detailContent = m.detail.EmptyView(detailWidth, detailH)
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
