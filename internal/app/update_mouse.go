package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/claude-mission-control/internal/tmux"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

const (
	contentStartRow   = 2                      // rows 0-1 are top border + label
	doubleClickWindow = 400 * time.Millisecond // max gap between clicks for double-click
	wheelScrollLines  = 3                      // lines to scroll per wheel tick
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
	contentHeight := m.height - 3
	colOffset := 0
	if !m.inFullscreenPopup {
		contentHeight--
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

// handleMouseClick dispatches a left-click to the appropriate panel handler.
func (m Model) handleMouseClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
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

// handleMouseWheel scrolls the panel under the cursor.
func (m Model) handleMouseWheel(msg tea.MouseMsg, dir int) (tea.Model, tea.Cmd) {
	switch m.hitTestPanel(msg.X, msg.Y) {
	case panelDetail:
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
	contentHeight := m.height - 3
	mmTermCol := 0
	if !m.inFullscreenPopup {
		contentHeight--
		mmTermCol = 1
	}
	mmTermRow := 2 + contentHeight - mmH
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
			if s.LaterBookmarkID != "" {
				m.client.Unlater(s.LaterBookmarkID) //nolint:errcheck
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
	if isClaude && m.sidebar.SelectByPaneID(paneID) {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, false)...)
		}
	} else if !isClaude {
		return m, m.focusNonClaudePane()
	}
	return m, nil
}

// handleDetailClick handles left-clicks on the detail panel (e.g. chat outline messages).
func (m Model) handleDetailClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	colOffset := 0
	if !m.inFullscreenPopup {
		colOffset = 1
	}
	// Convert terminal coords to detail-view-local coords.
	// DetailPanelStyle has Padding(0,1), so the view content starts 1 col after the panel edge.
	localX := msg.X - colOffset - m.sidebarPanelWidth() - 1
	localY := msg.Y - contentStartRow
	if idx := m.detail.ChatOutlineMsgAt(localX, localY); idx >= 0 {
		m.detail.NavigateMsgTo(idx)
	}
	return m, nil
}

// handleListClick handles left-clicks on the session sidebar panel.
func (m Model) handleListClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	listLocalY := msg.Y - contentStartRow
	paneID := m.sidebar.PaneIDAtLine(listLocalY)
	if paneID == "" {
		// Check if the click landed on a backlog item.
		if id := m.sidebar.BacklogIDAtLine(listLocalY); id != "" {
			m.sidebar.SelectByBacklogID(id)
		}
		return m, nil
	}

	now := time.Now()
	// Double-click on same pane → switch (same as Enter)
	if paneID == m.lastClickPaneID && now.Sub(m.lastClickTime) < doubleClickWindow {
		m.lastClickPaneID = ""
		m.lastClickTime = time.Time{}
		m.sidebar.SelectByPaneID(paneID)
		if s, ok := m.sidebar.SelectedItem(); ok {
			if s.IsPhantom {
				bookmarkID, cwd := s.LaterBookmarkID, s.CWD
				tmuxSession := m.origPane.Session
				return m, func() tea.Msg {
					if err := m.client.OpenLater(bookmarkID, cwd, tmuxSession); err != nil {
						return flashErrorMsg("open failed: " + err.Error())
					}
					return tea.QuitMsg{}
				}
			}
			if s.LaterBookmarkID != "" {
				m.client.Unlater(s.LaterBookmarkID) //nolint:errcheck
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
	if m.sidebar.SelectByPaneID(paneID) {
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(m.fetchForSelection(s, true)...)
		}
	}
	return m, nil
}
