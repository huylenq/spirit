package ui

import (
	"sort"
	"strings"

	"github.com/huylenq/spirit/internal/claude"
)

// Backlog-related methods for SidebarModel.

// IsBacklogSelected returns true if the cursor is in the backlog zone.
func (m SidebarModel) IsBacklogSelected() bool {
	return !m.deselected && len(m.filteredBacklog) > 0 && m.cursor >= len(m.filtered)
}

// SelectedBacklog returns the backlog item at the cursor, if in the backlog zone.
func (m SidebarModel) SelectedBacklog() (claude.Backlog, bool) {
	if !m.IsBacklogSelected() {
		return claude.Backlog{}, false
	}
	idx := m.cursor - len(m.filtered)
	if idx >= len(m.filteredBacklog) {
		return claude.Backlog{}, false
	}
	return m.filteredBacklog[idx], true
}

// SetBacklog stores backlog items, sorts by project then CreatedAt, applies narrow.
func (m *SidebarModel) SetBacklog(backlogs []claude.Backlog) {
	// Preserve selected backlog item across refresh
	var selectedBacklogID string
	if m.IsBacklogSelected() {
		if backlog, ok := m.SelectedBacklog(); ok {
			selectedBacklogID = backlog.ID
		}
	}

	m.backlogs = backlogs
	m.applyNarrowBacklog()
	m.rebuildProjects()

	if selectedBacklogID != "" {
		m.selectByBacklogID(selectedBacklogID)
	}
}

// selectByBacklogID sets the cursor to the backlog item with the given ID. Returns true if found.
func (m *SidebarModel) selectByBacklogID(id string) bool {
	for i, backlog := range m.filteredBacklog {
		if backlog.ID == id {
			m.cursor = len(m.filtered) + i
			m.deselected = false
			return true
		}
	}
	return false
}

// applyNarrowBacklog filters backlog items by the current narrow query and
// sorts them by project, then CreatedAt.
func (m *SidebarModel) applyNarrowBacklog() {
	if !m.backlogExpanded {
		m.filteredBacklog = nil
		return
	}
	if m.narrow == "" {
		m.filteredBacklog = make([]claude.Backlog, len(m.backlogs))
		copy(m.filteredBacklog, m.backlogs)
	} else {
		f := strings.ToLower(m.narrow)
		m.filteredBacklog = nil
		for _, backlog := range m.backlogs {
			title := strings.ToLower(backlog.DisplayTitle())
			body := strings.ToLower(backlog.Body)
			if strings.Contains(title, f) || strings.Contains(body, f) {
				m.filteredBacklog = append(m.filteredBacklog, backlog)
			}
		}
	}
	sort.SliceStable(m.filteredBacklog, func(i, j int) bool {
		a, b := m.filteredBacklog[i], m.filteredBacklog[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
}

// SelectByBacklogID sets the cursor to the backlog item with the given ID.
func (m *SidebarModel) SelectByBacklogID(id string) bool {
	return m.selectByBacklogID(id)
}
