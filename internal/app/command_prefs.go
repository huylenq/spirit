package app

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) execPrefsEditor() (Model, tea.Cmd) {
	m.state = StatePrefsEditor
	m.prefsEditor.Activate(prefsFileContent())
	return m, nil
}

// applyPrefsFromText parses raw text, persists via savePrefs, applies all known keys
// to the live model, and returns the count of unknown keys.
func (m *Model) applyPrefsFromText(text string) int {
	prefs := parsePrefsText(text)
	savePrefs(prefs)

	// Count unknown keys
	unknowns := len(prefs)
	for _, def := range PrefRegistry {
		if _, ok := prefs[def.Key]; ok {
			unknowns--
		}
	}

	// Apply each known key to live model state
	m.sidebar.SetGroupByProject(prefs["groupByProject"] == "true")
	m.sidebar.SetBacklogExpanded(prefs["backlogExpanded"] == "true")
	m.sidebar.SetClaudingExpanded(prefs["claudingCollapsed"] != "true")
	m.showMinimap = prefs["minimap"] == "true"
	if v := prefs["minimapMode"]; v != "" {
		m.minimapMode = v
	}
	if n, err := strconv.Atoi(prefs["minimapMaxH"]); err == nil {
		m.minimapMaxH = n
	}
	m.minimapCollapse = prefs["minimapCollapse"] == "true"
	if n, err := strconv.Atoi(prefs["sidebarWidthPct"]); err == nil {
		m.sidebarWidthPct = n
	}
	m.applyLayout()
	return unknowns
}

