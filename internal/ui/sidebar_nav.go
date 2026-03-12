package ui

// Navigation methods for SidebarModel: cursor movement, selection level switching.

// totalItems returns the combined count of sessions + backlog items for cursor range.
func (m SidebarModel) totalItems() int {
	return len(m.filtered) + len(m.filteredBacklog)
}

// Deselect marks the list as having no active selection (minimap on non-Claude pane).
func (m *SidebarModel) Deselect() {
	m.deselected = true
}

// Reselect restores the list selection after Deselect.
func (m *SidebarModel) Reselect() {
	m.deselected = false
}

func (m *SidebarModel) SelectByPaneID(paneID string) bool {
	for i, s := range m.filtered {
		if s.PaneID == paneID {
			m.cursor = i
			m.deselected = false
			return true
		}
	}
	return false
}

func (m *SidebarModel) MoveUp() {
	m.deselected = false
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *SidebarModel) MoveDown() {
	m.deselected = false
	if m.cursor < m.totalItems()-1 {
		m.cursor++
	}
}

func (m *SidebarModel) MoveToTop() {
	m.deselected = false
	m.cursor = 0
}

func (m *SidebarModel) MoveToBottom() {
	m.deselected = false
	total := m.totalItems()
	if total > 0 {
		m.cursor = total - 1
	}
}

// SelectionLevel returns the current navigation level.
func (m SidebarModel) SelectionLevel() SelectionLevel {
	return m.selectionLevel
}

// SelectedProjectRow returns the line index of the selected project header
// within the list's rendered output. Returns -1 if no project is selected.
func (m SidebarModel) SelectedProjectRow() int {
	return m.selectedProjectRow
}

// SelectedItemRow returns the line index of the selected session item
// within the list's rendered output. Returns -1 if no session item is selected.
func (m SidebarModel) SelectedItemRow() int {
	return m.selectedItemRow
}

// EnterProjectLevel switches to project-level navigation.
// The project cursor is set to the project entry matching the currently selected session.
func (m *SidebarModel) EnterProjectLevel() {
	if len(m.projects) == 0 {
		return
	}
	m.selectionLevel = LevelProject
	// Derive project cursor from current backlog item
	if m.IsBacklogSelected() {
		if backlog, ok := m.SelectedBacklog(); ok {
			for i, p := range m.projects {
				if p.Name == backlog.Project && p.StatusOrder == OrderBacklog {
					m.projectCursor = i
					return
				}
			}
		}
	}
	// Derive project cursor from current session
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		s := m.filtered[m.cursor]
		order := -1
		if !m.groupByProject {
			order = sessionOrder(s)
		} else if sessionOrder(s) == OrderLater {
			order = OrderLater
		}
		for i, p := range m.projects {
			if p.Name == s.Project && p.StatusOrder == order {
				m.projectCursor = i
				return
			}
		}
	}
	m.projectCursor = 0
}

// EnterSessionLevel switches to session-level navigation.
// The cursor moves to the first session matching the selected project entry.
func (m *SidebarModel) EnterSessionLevel() {
	if m.selectionLevel != LevelProject {
		return
	}
	m.selectionLevel = LevelSession
	if pe, ok := m.SelectedProject(); ok {
		// If this is a backlog project, jump to first backlog item in that project
		if pe.StatusOrder == OrderBacklog {
			for i, backlog := range m.filteredBacklog {
				if backlog.Project == pe.Name {
					m.cursor = len(m.filtered) + i
					m.deselected = false
					return
				}
			}
		}
		for i, s := range m.filtered {
			if pe.matches(s) {
				m.cursor = i
				m.deselected = false
				return
			}
		}
	}
}

// MoveUpProject moves the project cursor up.
func (m *SidebarModel) MoveUpProject() {
	if m.projectCursor > 0 {
		m.projectCursor--
	}
}

// MoveDownProject moves the project cursor down.
func (m *SidebarModel) MoveDownProject() {
	if m.projectCursor < len(m.projects)-1 {
		m.projectCursor++
	}
}
