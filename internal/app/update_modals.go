package app

import (
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/huylenq/claude-mission-control/internal/ui"
)

func (m Model) handleKeySearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, Keys.Escape), key.Matches(msg, Keys.Enter):
		m.search.Confirm()
		m.state = StateNormal
		// Remember selection, clear filter, re-select (search & jump)
		ref := m.sidebar.CursorRef()
		m.sidebar.ClearNarrow()
		m.sidebar.SelectByRef(ref)
		// Trigger landing flash so the jumped-to item is visually highlighted
		m.sidebar.SetLandByRef(ref, ui.SearchFlashFrames)
		return m, nil
	case key.Matches(msg, Keys.MsgNext):
		m.sidebar.MoveDown()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(capturePreview(s.PaneID), m.fetchChatOutline(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
		}
		return m, nil
	case key.Matches(msg, Keys.MsgPrev):
		m.sidebar.MoveUp()
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(capturePreview(s.PaneID), m.fetchChatOutline(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
		}
		return m, nil
	default:
		// Forward to textinput
		ti := m.search.TextInput()
		newTI, cmd := ti.Update(msg)
		*ti = newTI
		m.sidebar.SetNarrow(m.search.Value())
		// Update preview for new selection
		if s, ok := m.sidebar.SelectedItem(); ok {
			return m, tea.Batch(cmd, capturePreview(s.PaneID), m.fetchChatOutline(s.PaneID, s.SessionID), m.fetchDiffStats(s.PaneID, s.SessionID), m.fetchCachedSummary(s.PaneID, s.SessionID))
		}
		return m, cmd
	}
}

func (m Model) handleKeyKillConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		return m, killPaneCmd(m.killTargetPaneID, m.killTargetSessionID, m.killTargetPID, m.killTargetLaterID)
	case "n", "esc":
		m.state = StateNormal
		m.killTargetPaneID = ""
		m.killTargetSessionID = ""
		m.killTargetPID = 0
		m.killTargetTitle = ""
		m.killTargetAnimalIdx = 0
		m.killTargetColorIdx = 0
		m.killTargetLaterID = ""
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleKeyMinimapSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.state = StateNormal
		m.flashMsg = ""
		m.flashExpiry = time.Time{}
		savePrefInt("minimapMaxH", m.minimapMaxH)
		return m, nil
	case "M":
		m.minimapMode = nextMinimapMode(m.minimapMode)
		savePrefString("minimapMode", m.minimapMode)
		m.applyLayout()
	case "+", "=":
		if m.minimapMaxH < 30 {
			m.minimapMaxH++
			m.applyLayout()
		}
	case "-":
		if m.minimapMaxH > 5 {
			m.minimapMaxH--
			m.applyLayout()
		}
	case "c":
		m.minimapCollapse = !m.minimapCollapse
		savePrefBool("minimapCollapse", m.minimapCollapse)
		m.applyLayout()
	default:
		// Exit and persist scale, then re-dispatch so the key isn't swallowed
		m.state = StateNormal
		m.flashMsg = ""
		m.flashExpiry = time.Time{}
		savePrefInt("minimapMaxH", m.minimapMaxH)
		return m.handleKey(msg)
	}
	return m, m.flashMinimapSettings()
}

func (m Model) handleKeySettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.settingsCursor < len(SettingsRegistry)-1 {
			m.settingsCursor++
		}
		return m, nil
	case "k", "up":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
		return m, nil
	case " ", "enter":
		if m.settingsCursor >= len(SettingsRegistry) {
			return m, nil
		}
		s := SettingsRegistry[m.settingsCursor]
		prefs := loadPrefs()
		if prefs == nil {
			prefs = map[string]string{}
		}
		switch s.Kind {
		case SettingBool:
			if settingVal(s.Key, prefs) == "true" {
				prefs[s.Key] = "false"
			} else {
				prefs[s.Key] = "true"
			}
		case SettingEnum:
			prefs[s.Key] = cycleEnum(s.Options, settingVal(s.Key, prefs), true)
		}
		savePrefs(prefs)
		m.applySettingToModel(s.Key, prefs)
		return m, nil
	case "l", "right", "+", "=":
		return m.adjustSetting(true)
	case "h", "left", "-":
		return m.adjustSetting(false)
	case "esc":
		m.state = StateNormal
		return m, nil
	default:
		return m, nil
	}
}

// adjustSetting increments/decrements an int or cycles an enum forward/backward.
func (m Model) adjustSetting(forward bool) (tea.Model, tea.Cmd) {
	if m.settingsCursor >= len(SettingsRegistry) {
		return m, nil
	}
	s := SettingsRegistry[m.settingsCursor]
	prefs := loadPrefs()
	if prefs == nil {
		prefs = map[string]string{}
	}
	switch s.Kind {
	case SettingInt:
		cur, _ := strconv.Atoi(settingVal(s.Key, prefs))
		if forward {
			if s.Max == 0 || cur < s.Max {
				prefs[s.Key] = strconv.Itoa(cur + 1)
			}
		} else {
			if cur > s.Min {
				prefs[s.Key] = strconv.Itoa(cur - 1)
			}
		}
	case SettingEnum:
		prefs[s.Key] = cycleEnum(s.Options, settingVal(s.Key, prefs), forward)
	}
	savePrefs(prefs)
	m.applySettingToModel(s.Key, prefs)
	return m, nil
}

// flashMinimapSettings shows the current minimap mode+scale in the flash bar with a 3s timeout.
func (m *Model) flashMinimapSettings() tea.Cmd {
	m.flashMsg = minimapModeFlash(m.minimapMode, m.minimapMaxH, m.minimapCollapse)
	m.flashIsError = false
	m.flashExpiry = time.Now().Add(3 * time.Second)
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return ClearFlashMsg{} })
}
