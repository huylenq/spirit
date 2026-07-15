package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/huylenq/spirit/internal/tmux"
	"github.com/huylenq/spirit/internal/ui"
)

const (
	contentStartRow   = 2                      // rows 0-1 are top border + label
	doubleClickWindow = 400 * time.Millisecond // max gap between clicks for double-click
	wheelScrollLines  = 3                      // lines to scroll per wheel tick
)

type copilotDragMode int

type copilotResizeEdge uint8

const (
	copilotDragNone copilotDragMode = iota
	copilotDragMove
	copilotDragResizeFloat
	copilotDragResizeDocked
)

const (
	copilotEdgeTop copilotResizeEdge = 1 << iota
	copilotEdgeBottom
	copilotEdgeLeft
	copilotEdgeRight
)

// mousePanel identifies which UI panel a mouse coordinate falls in.
type mousePanel int

const (
	panelNone    mousePanel = iota
	panelSidebar            // sidebar (left)
	panelDetail             // content preview (right)
	panelMinimap            // minimap overlay (bottom-left corner of list)
)

// focusNonClaudePane deselects the list, captures the non-Claude pane content
// for preview, and switches tmux to the minimap's currently selected pane.
func (m *Model) focusNonClaudePane() tea.Cmd {
	m.sidebar.Deselect()
	info, ok := m.minimap.SelectedPaneInfo()
	if !ok {
		m.detail.ClearSession()
		return nil
	}
	m.nonClaudePane = &info
	return tea.Batch(
		capturePreview(info.PaneID),
		switchPaneQuiet(info.SessionName, info.WindowIndex, info.PaneIndex),
	)
}

// hitTestPanel determines which panel a terminal coordinate belongs to.
func (m Model) hitTestPanel(x, y int) mousePanel {
	contentHeight := m.contentHeight()
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1 // left border
	}

	// Content area: rows [contentStartRow, contentStartRow+contentHeight)
	if y < contentStartRow || y >= contentStartRow+contentHeight {
		return panelNone
	}

	// Check minimap first — it overlays the bottom-left of the list
	if m.showMinimap {
		mmW, mmH := m.minimap.ViewSize()
		if mmH > 0 && mmW > 0 {
			mmTermRow := contentStartRow + contentHeight - mmH
			if x >= colOffset && x < colOffset+mmW && y >= mmTermRow {
				return panelMinimap
			}
		}
	}

	// Work queue mode: no sidebar panel — everything is detail
	if m.viewMode == ViewWorkQueue {
		return panelDetail
	}

	// Split on list width boundary
	innerWidth := m.width
	if !m.inFullscreenPopup {
		innerWidth -= 2
	}
	sidebarWidth := max(innerWidth*m.sidebarWidthPct/100, 20)

	if x-colOffset < sidebarWidth {
		return panelSidebar
	}
	return panelDetail
}

// sidebarDragEdge returns the terminal x column of the sidebar's right border.
// Returns -1 in work queue mode (no sidebar to drag).
func (m Model) sidebarDragEdge() int {
	if m.viewMode == ViewWorkQueue {
		return -1
	}
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	return colOffset + m.sidebarPanelWidth() - 1
}

// handleMouseClick dispatches a left-click to the appropriate panel handler.
func (m Model) handleMouseClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Check for sidebar edge drag before panel dispatch (not in work queue mode).
	edge := m.sidebarDragEdge()
	if edge >= 0 && msg.X >= edge-1 && msg.X <= edge+1 &&
		msg.Y >= contentStartRow && msg.Y < contentStartRow+m.contentHeight() {
		m.sidebarDragging = true
		m.sidebarDragStartX = msg.X
		m.sidebarDragStartPct = m.sidebarWidthPct
		return m, nil
	}

	switch m.hitTestPanel(msg.X, msg.Y) {
	case panelMinimap:
		return m.handleMinimapClick(msg)
	case panelSidebar:
		return m.handleListClick(msg)
	case panelDetail:
		return m.handleDetailClick(msg)
	}
	return m, nil
}

// hitTestCopilot reports whether terminal coordinate (x, y) falls inside the copilot panel.
// For float mode it recomputes the same geometry as view.go; for docked mode it checks the
// rightmost copilotDockedWidth columns of the content area.
func (m Model) hitTestCopilot(x, y int) bool {
	if !m.copilotVisible {
		return false
	}
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	innerWidth := m.innerWidth()
	contentHeight := m.contentHeight()

	if m.copilotMode == CopilotModeDocked {
		copilotW := m.copilotDockedWidth()
		if copilotW == 0 {
			return false
		}
		panelH := m.panelContentHeight(contentHeight)
		copilotColStart := colOffset + innerWidth - copilotW
		return x >= copilotColStart && y >= contentStartRow && y < contentStartRow+panelH
	}

	// Float mode: use shared geometry helper (maxOverlayH as conservative height bound).
	row, col, overlayW, maxOverlayH := m.copilotFloatGeometry(innerWidth, contentHeight)
	termRow := contentStartRow + row
	termCol := colOffset + col
	return x >= termCol && x < termCol+overlayW && y >= termRow && y < termRow+maxOverlayH
}

// copilotFloatBounds returns the rendered floating Lulu panel bounds in terminal coordinates.
func (m Model) copilotFloatBounds() (row, col, width, height int) {
	innerWidth := m.innerWidth()
	contentHeight := m.copilotRenderedContentHeight()
	localRow, localCol, overlayW, maxOverlayH := m.copilotFloatGeometry(innerWidth, contentHeight)
	adjustMode := m.state == StateAdjustCopilot
	inputView := ""
	if !adjustMode {
		inputView = m.copilotInput.View()
	}
	overlay := ui.RenderCopilotOverlay(
		m.copilot.Messages(), inputView,
		overlayW, maxOverlayH,
		m.copilot.ScrollOffset(), m.copilot.Streaming(),
		m.copilot.StreamingCursor(),
		m.state == StateCopilot || adjustMode,
		adjustMode, m.copilotDH != 0, m.copilot.ModelID(), m.copilot.ModeID(), m.copilot.SessionID(),
	)
	height = lipgloss.Height(overlay)
	localRow = max(min(localRow, contentHeight-height), 0)
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	return contentStartRow + localRow, colOffset + localCol, overlayW, height
}

// copilotRenderedContentHeight mirrors View's footer/minimap adjustments so mouse
// bounds address the same terminal rows as the rendered floating overlay.
func (m Model) copilotRenderedContentHeight() int {
	height := m.contentHeight()
	if m.viewMode != ViewWorkQueue && m.sidebar.IsAllQuiet() {
		return height + 1
	}
	if m.shouldDockMinimap() {
		if view := m.minimap.ViewDocked(m.innerWidth()); view != "" {
			return height - lipgloss.Height(view)
		}
	}
	return height - 1
}

// beginCopilotMouseDrag starts a move/resize gesture or focuses a clicked Lulu panel.
func (m Model) beginCopilotMouseDrag(msg tea.MouseMsg) (Model, bool) {
	if !m.copilotVisible {
		return m, false
	}
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	if m.copilotMode == CopilotModeDocked {
		edge := colOffset + m.innerWidth() - m.copilotDockedWidth()
		if msg.Y >= contentStartRow && msg.Y < contentStartRow+m.contentHeight() && msg.X >= edge-1 && msg.X <= edge+1 {
			m.copilotDragMode = copilotDragResizeDocked
			m.copilotDragStartX = msg.X
			m.copilotDragStartW = m.copilotDockedWidth()
			return m, true
		}
		if !m.hitTestCopilot(msg.X, msg.Y) {
			return m, false
		}
	} else {
		row, col, width, height := m.copilotFloatBounds()
		if msg.X < col || msg.X >= col+width || msg.Y < row || msg.Y >= row+height {
			return m, false
		}
		m.copilotDragStartX, m.copilotDragStartY = msg.X, msg.Y
		m.copilotDragStartOffX, m.copilotDragStartOffY = m.copilotOffX, m.copilotOffY
		m.copilotDragStartDW, m.copilotDragStartDH = m.copilotDW, m.copilotDH
		m.copilotDragStartW, m.copilotDragStartH = width, height
		m.copilotDragEdges = 0
		if msg.X == col {
			m.copilotDragEdges |= copilotEdgeLeft
		}
		if msg.X == col+width-1 {
			m.copilotDragEdges |= copilotEdgeRight
		}
		// Top corners resize; the rest of the top border/title moves the panel.
		if msg.Y == row && m.copilotDragEdges != 0 {
			m.copilotDragEdges |= copilotEdgeTop
			m.copilotDragMode = copilotDragResizeFloat
			return m, true
		}
		if msg.Y <= row+1 {
			m.copilotDragMode = copilotDragMove
			return m, true
		}
		if msg.Y == row+height-1 {
			m.copilotDragEdges |= copilotEdgeBottom
		}
		if m.copilotDragEdges != 0 {
			m.copilotDragMode = copilotDragResizeFloat
			return m, true
		}
	}

	// Clicking the body focuses Lulu without passing the click through to the panel below.
	if m.state == StateNormal {
		m.state = StateCopilot
		if m.copilotInput.Active() {
			m.focusCopilot()
		} else {
			m.copilotInput.Activate()
		}
	}
	return m, true
}

func (m Model) handleCopilotDragMotion(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	dx := msg.X - m.copilotDragStartX
	dy := msg.Y - m.copilotDragStartY
	switch m.copilotDragMode {
	case copilotDragMove:
		m.copilotOffX = m.copilotDragStartOffX + dx
		m.copilotOffY = m.copilotDragStartOffY + dy
	case copilotDragResizeFloat:
		innerW := m.innerWidth()
		baseW := max(min(copilotFloatMaxW, innerW-4), 20)
		newW := m.copilotDragStartW
		if m.copilotDragEdges&copilotEdgeLeft != 0 {
			newW = m.copilotDragStartW - dx
		} else if m.copilotDragEdges&copilotEdgeRight != 0 {
			newW = m.copilotDragStartW + dx
		}
		newW = max(min(newW, innerW-4), 20)
		if m.copilotDragEdges&(copilotEdgeLeft|copilotEdgeRight) != 0 {
			m.copilotDW = newW - baseW
		}
		if m.copilotDragEdges&copilotEdgeRight != 0 {
			// Keep the opposite (left) edge fixed while the right edge follows the pointer.
			m.copilotOffX = m.copilotDragStartOffX + newW - m.copilotDragStartW
		}

		contentH := m.copilotRenderedContentHeight()
		maxH := max(contentH-2, copilotFloatMinH)
		baseH := min(max(contentH-2*copilotFloatMargT, copilotFloatMinH), maxH)
		newH := m.copilotDragStartH
		if m.copilotDragEdges&copilotEdgeTop != 0 {
			newH = m.copilotDragStartH - dy
		} else if m.copilotDragEdges&copilotEdgeBottom != 0 {
			newH = m.copilotDragStartH + dy
		}
		newH = max(min(newH, maxH), copilotFloatMinH)
		if m.copilotDragEdges&(copilotEdgeTop|copilotEdgeBottom) != 0 {
			m.copilotDH = newH - baseH
		}
		if m.copilotDragEdges&copilotEdgeTop != 0 {
			m.copilotOffY = m.copilotDragStartOffY + m.copilotDragStartH - newH
		}
	case copilotDragResizeDocked:
		newW := m.copilotDragStartW - dx
		m.copilotDockedW = max(min(newW, m.innerWidth()/2), minCopilotDockedW)
		m.applyLayoutFast()
	}
	return m, nil
}

func (m Model) handleCopilotDragRelease() (tea.Model, tea.Cmd) {
	mode := m.copilotDragMode
	m.copilotDragMode = copilotDragNone
	switch mode {
	case copilotDragMove, copilotDragResizeFloat:
		savePrefInt("copilotOffX", m.copilotOffX)
		savePrefInt("copilotOffY", m.copilotOffY)
		savePrefInt("copilotDW", m.copilotDW)
		savePrefInt("copilotDH", m.copilotDH)
	case copilotDragResizeDocked:
		m.applyLayout()
		savePrefInt("copilotDockedW", m.copilotDockedW)
	}
	return m, nil
}

// handleMouseWheel scrolls the panel under the cursor.
func (m Model) handleMouseWheel(msg tea.MouseMsg, dir int) (tea.Model, tea.Cmd) {
	// Copilot scroll takes priority — works regardless of focus state.
	if m.hitTestCopilot(msg.X, msg.Y) {
		if dir < 0 {
			m.copilot.ScrollUp(wheelScrollLines)
		} else {
			m.copilot.ScrollDown(wheelScrollLines)
		}
		return m, nil
	}

	// In backlog edit mode, scroll the textarea by forwarding cursor key messages.
	if m.state == StateBacklogPrompt && m.hitTestPanel(msg.X, msg.Y) == panelDetail {
		keyType := tea.KeyUp
		if dir > 0 {
			keyType = tea.KeyDown
		}
		var cmds []tea.Cmd
		for range wheelScrollLines {
			if cmd := m.promptEditor.Update(tea.KeyMsg{Type: keyType}); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}

	switch m.hitTestPanel(msg.X, msg.Y) {
	case panelDetail:
		if m.sidebar.IsBacklogSelected() {
			m.backlogScroll = max(m.backlogScroll+dir*wheelScrollLines, 0)
			return m, nil
		}
		m.detail.ScrollLines(dir * wheelScrollLines)
		return m, nil
	case panelSidebar:
		if m.sidebar.SelectionLevel() == ui.LevelProject {
			if dir > 0 {
				m.sidebar.MoveDownProject()
			} else {
				m.sidebar.MoveUpProject()
			}
			if s, ok := m.sidebar.SelectedProjectSession(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
		} else {
			if dir > 0 {
				m.sidebar.MoveDown()
			} else {
				m.sidebar.MoveUp()
			}
			if s, ok := m.sidebar.SelectedItem(); ok {
				return m, tea.Batch(m.fetchForSelection(s, true)...)
			}
		}
		return m, nil
	}
	return m, nil
}

// minimapGridCoords translates terminal-space mouse coordinates to minimap grid coordinates.
// Returns (gridX, gridY, ok). ok is false if minimap is hidden or has no size.
func (m Model) minimapGridCoords(termX, termY int) (int, int, bool) {
	if !m.showMinimap {
		return 0, 0, false
	}
	_, mmH := m.minimap.ViewSize()
	if mmH == 0 {
		return 0, 0, false
	}
	contentHeight := m.contentHeight()
	mmTermCol := 0
	if !m.inFullscreenPopup {
		mmTermCol = 1
	}
	mmTermRow := contentStartRow + contentHeight - mmH
	// Skip: minimap left border (1), top border + session label + window labels (3)
	return termX - mmTermCol - 1, termY - mmTermRow - 3, true
}

// handleMinimapClick handles left-clicks on the minimap overlay.
func (m Model) handleMinimapClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	gridX, gridY, ok := m.minimapGridCoords(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	paneID, isClaude := m.minimap.PaneAtGridCoord(gridX, gridY)
	if paneID == "" {
		return m, nil
	}
	now := time.Now()
	// Double-click on same pane → switch to it (like Enter)
	if paneID == m.lastClickPaneID && now.Sub(m.lastClickTime) < doubleClickWindow {
		m.lastClickPaneID = ""
		m.lastClickTime = time.Time{}
		if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == paneID {
			if s.LaterID != "" {
				m.client.Unlater(s.LaterID) //nolint:errcheck
			}
			tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
			return m, tea.Quit
		}
		// Non-Claude pane double-click → switch via minimap info
		if info, ok := m.minimap.SelectedPaneInfo(); ok && info.PaneID == paneID {
			tmux.SwitchToPane(info.SessionName, info.WindowIndex, info.PaneIndex, info.PaneID)
			return m, tea.Quit
		}
		return m, nil
	}
	// Single click → select
	m.lastClickPaneID = paneID
	m.lastClickTime = now
	if paneID == m.minimap.SelectedPaneID() {
		return m, nil
	}
	m.recordJump()
	m.minimap.UpdateSelected(paneID)
	if isClaude && m.selectByPaneID(paneID) {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, false)...)
		}
	} else if !isClaude {
		return m, m.focusNonClaudePane()
	}
	return m, nil
}

// detailLocalX converts a terminal x coordinate to detail-view-local x.
func (m Model) detailLocalX(termX int) int {
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	if m.viewMode == ViewWorkQueue {
		return termX - colOffset // no sidebar offset in work queue mode
	}
	return termX - colOffset - m.sidebarPanelWidth() - 1
}

// handleDetailClick handles left-clicks on the detail panel (e.g. chat outline messages).
func (m Model) handleDetailClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Click on backlog preview → enter edit mode
	if m.sidebar.IsBacklogSelected() {
		return m.execEditBacklog()
	}

	localX := m.detailLocalX(msg.X)
	localY := msg.Y - contentStartRow
	if m.viewMode == ViewWorkQueue {
		localY -= ui.WorkQueueHeight
	}

	// Check if click is on the outline panel drag edge
	if dragEdge := m.detail.ChatOutlineDragEdge(); dragEdge >= 0 {
		if localX >= dragEdge-1 && localX <= dragEdge+1 {
			m.outlineDragging = true
			m.outlineDragStartX = msg.X
			contentWidth := m.detail.Width() - 4
			m.outlineDragStartW = m.detail.EffectivePanelWidth(contentWidth)
			return m, nil
		}
	}

	if idx := m.detail.ChatOutlineMsgAt(localX, localY); idx >= 0 {
		m.detail.NavigateMsgTo(idx)
		return m, maybeCursorPulseTickCmd(&m)
	}
	return m, nil
}

// handleOutlineDragMotion updates the outline panel width during a drag.
func (m Model) handleOutlineDragMotion(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	delta := msg.X - m.outlineDragStartX
	var newWidth int
	switch m.chatOutlineMode {
	case ChatOutlineDockedLeft:
		// Dragging right = wider
		newWidth = m.outlineDragStartW + delta
	default:
		// Docked-right and overlay: dragging left = wider
		newWidth = m.outlineDragStartW - delta
	}
	m.detail.SetChatOutlineWidthFast(newWidth)
	return m, nil
}

// handleOutlineDragRelease finalizes the drag and persists the new width.
func (m Model) handleOutlineDragRelease() (tea.Model, tea.Cmd) {
	m.outlineDragging = false
	w := m.detail.ChatOutlineWidthOverride()
	if w > 0 {
		m.detail.SetChatOutlineWidth(w) // final reflow at the settled width
		savePrefInt("chatOutlineWidth", w)
	}
	return m, nil
}

// handleSidebarDragMotion updates the sidebar width during a drag.
func (m Model) handleSidebarDragMotion(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	innerW := m.innerWidth()
	if innerW <= 0 {
		return m, nil
	}
	delta := msg.X - m.sidebarDragStartX
	newPct := m.sidebarDragStartPct + delta*100/innerW
	m.sidebarWidthPct = max(min(newPct, maxSidebarWidthPct), minSidebarWidthPct)
	m.applyLayoutFast()
	return m, nil
}

// handleSidebarDragRelease finalizes the sidebar drag and persists the new width.
func (m Model) handleSidebarDragRelease() (tea.Model, tea.Cmd) {
	m.sidebarDragging = false
	m.applyLayout()
	savePrefInt("sidebarWidthPct", m.sidebarWidthPct)
	return m, nil
}

// handleListClick handles left-clicks on the session sidebar panel.
func (m Model) handleListClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	listLocalY := msg.Y - contentStartRow
	paneID := m.sidebar.PaneIDAtLine(listLocalY)
	if paneID == "" {
		// Check if the click landed on a backlog item.
		if id := m.sidebar.BacklogIDAtLine(listLocalY); id != "" {
			m.backlogScroll = 0
			m.sidebar.SelectByBacklogID(id)
		}
		return m, nil
	}

	now := time.Now()
	// Double-click on same pane → switch (same as Enter)
	if paneID == m.lastClickPaneID && now.Sub(m.lastClickTime) < doubleClickWindow {
		m.lastClickPaneID = ""
		m.lastClickTime = time.Time{}
		m.selectByPaneID(paneID)
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.IsPhantom {
				laterID, cwd := s.LaterID, s.CWD
				tmuxSession := m.origPane.Session
				return m, func() tea.Msg {
					if err := m.client.OpenLater(laterID, cwd, tmuxSession); err != nil {
						return flashErrorMsg("open failed: " + err.Error())
					}
					return tea.QuitMsg{}
				}
			}
			if s.LaterID != "" {
				m.client.Unlater(s.LaterID) //nolint:errcheck
			}
			tmux.SwitchToPane(s.TmuxSession, s.TmuxWindow, s.TmuxPane, s.PaneID)
			return m, tea.Quit
		}
		return m, nil
	}

	// Single click → select
	m.lastClickPaneID = paneID
	m.lastClickTime = now

	// Skip re-fetch if already selected
	if s, ok := m.sidebar.SelectedItem(); ok && s.PaneID == paneID {
		return m, nil
	}

	m.recordJump()
	if m.selectByPaneID(paneID) {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, true)...)
		}
	}
	return m, nil
}
