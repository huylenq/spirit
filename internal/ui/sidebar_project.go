package ui

import "github.com/huylenq/claude-mission-control/internal/claude"

// Project-level query methods for SidebarModel.

// SelectedProject returns the currently selected project entry when at project level.
func (m SidebarModel) SelectedProject() (projectEntry, bool) {
	if m.selectionLevel != LevelProject || len(m.projects) == 0 {
		return projectEntry{}, false
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor], true
	}
	return projectEntry{}, false
}

// FirstSessionInProject returns the first session matching a project entry.
func (m SidebarModel) FirstSessionInProject(pe projectEntry) (claude.ClaudeSession, bool) {
	for _, s := range m.filtered {
		if pe.matches(s) {
			return s, true
		}
	}
	return claude.ClaudeSession{}, false
}

// SelectedProjectSession returns the first session in the currently selected project.
// Convenience method collapsing SelectedProject + FirstSessionInProject.
func (m SidebarModel) SelectedProjectSession() (claude.ClaudeSession, bool) {
	pe, ok := m.SelectedProject()
	if !ok {
		return claude.ClaudeSession{}, false
	}
	return m.FirstSessionInProject(pe)
}

// SessionsInProject returns all sessions matching a project entry.
func (m SidebarModel) SessionsInProject(pe projectEntry) []claude.ClaudeSession {
	var result []claude.ClaudeSession
	for _, s := range m.filtered {
		if pe.matches(s) {
			result = append(result, s)
		}
	}
	return result
}
