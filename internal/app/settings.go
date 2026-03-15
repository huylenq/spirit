package app

import (
	"strconv"
	"strings"

	"github.com/huylenq/claude-mission-control/internal/ui"
)

// SettingKind classifies how a setting is displayed and edited.
type SettingKind int

const (
	SettingBool SettingKind = iota
	SettingInt
	SettingEnum
)

// SettingDef defines a single user-configurable setting.
type SettingDef struct {
	Key     string
	Label   string
	Kind    SettingKind
	Default string
	Options []string // SettingEnum: valid values to cycle through
	Min     int      // SettingInt: minimum
	Max     int      // SettingInt: maximum
}

// SettingsRegistry is the ordered list of all settings shown in the overlay (P key).
var SettingsRegistry = []SettingDef{
	{Key: "groupByProject", Label: "Group by project", Kind: SettingBool, Default: "false"},
	{Key: "minimap", Label: "Show minimap", Kind: SettingBool, Default: "false"},
	{Key: "minimapMode", Label: "Minimap mode", Kind: SettingEnum, Default: "auto", Options: []string{"auto", "docked", "float", "smart"}},
	{Key: "minimapMaxH", Label: "Minimap max height", Kind: SettingInt, Default: "14", Min: 5, Max: 30},
	{Key: "minimapCollapse", Label: "Minimap collapse", Kind: SettingBool, Default: "false"},
	{Key: "chatOutlineMode", Label: "Chat outline mode", Kind: SettingEnum, Default: "overlay", Options: []string{"overlay", "docked", "docked-left", "hidden"}},
	{Key: "chatOutlineWidth", Label: "Chat outline width", Kind: SettingInt, Default: "40", Min: 20, Max: 120},
	{Key: "sidebarWidthPct", Label: "Sidebar width %", Kind: SettingInt, Default: "30", Min: 10, Max: 60},
	{Key: "autoSynthesize", Label: "Auto-synthesize on idle", Kind: SettingBool, Default: "true"},
	{Key: "autoJump", Label: "Auto-jump after send", Kind: SettingBool, Default: "true"},
}

// Flag returns the current boolean value of a setting.
// Checks prefs file first, falls back to registry default.
func Flag(key string) bool {
	prefs := loadPrefs()
	if v, ok := prefs[key]; ok {
		return v == "true"
	}
	for _, s := range SettingsRegistry {
		if s.Key == key {
			return s.Default == "true"
		}
	}
	return false
}

// settingVal returns a setting's current value from prefs, falling back to default.
func settingVal(key string, prefs map[string]string) string {
	if v, ok := prefs[key]; ok && v != "" {
		return v
	}
	for _, s := range SettingsRegistry {
		if s.Key == key {
			return s.Default
		}
	}
	return ""
}

// ensureSettingDefaults writes defaults for settings missing from the prefs file.
// Called once at TUI startup so the daemon always has explicit values to readPref().
func ensureSettingDefaults() {
	prefs := loadPrefs()
	if prefs == nil {
		prefs = map[string]string{}
	}
	changed := false
	for _, s := range SettingsRegistry {
		if _, ok := prefs[s.Key]; !ok {
			prefs[s.Key] = s.Default
			changed = true
		}
	}
	if changed {
		savePrefs(prefs)
	}
}

// cycleEnum returns the next or previous option for an enum setting.
func cycleEnum(options []string, current string, forward bool) string {
	for i, o := range options {
		if o == current {
			if forward {
				return options[(i+1)%len(options)]
			}
			return options[(i-1+len(options))%len(options)]
		}
	}
	if len(options) > 0 {
		return options[0]
	}
	return current
}

// applySettingToModel updates live model state from the given prefs map.
// Accepts an already-loaded prefs map to avoid redundant disk reads.
func (m *Model) applySettingToModel(key string, prefs map[string]string) {
	switch key {
	case "groupByProject":
		m.sidebar.SetGroupByProject(prefs[key] == "true")
	case "minimap":
		m.showMinimap = prefs[key] == "true"
	case "minimapMode":
		if v := prefs[key]; v != "" {
			m.minimapMode = v
		}
	case "minimapMaxH":
		if n, err := strconv.Atoi(prefs[key]); err == nil {
			m.minimapMaxH = n
		}
	case "minimapCollapse":
		m.minimapCollapse = prefs[key] == "true"
	case "chatOutlineMode":
		if v := prefs[key]; v != "" {
			m.chatOutlineMode = v
			m.detail.SetChatOutlineMode(v)
		}
	case "chatOutlineWidth":
		if n, err := strconv.Atoi(prefs[key]); err == nil {
			m.detail.SetChatOutlineWidth(n)
		}
	case "sidebarWidthPct":
		if n, err := strconv.Atoi(prefs[key]); err == nil {
			m.sidebarWidthPct = n
		}
	case "autoJump":
		v := prefs[key] == "true"
		m.autoJumpOn = v
		m.sidebar.ShowAutoJump = v
	// autoSynthesize: daemon-only (read via readPref on each synthesis attempt), no model state to update
	}
	m.applyLayout()
}

// renderSettingsOverlay renders the settings list overlay.
func (m Model) renderSettingsOverlay() string {
	title := ui.HelpTitleStyle.Render("Settings")
	prefs := loadPrefs()
	var lines []string
	for i, s := range SettingsRegistry {
		lines = append(lines, renderSettingLine(s, prefs, i == m.settingsCursor))
	}
	body := title + "\n\n" + strings.Join(lines, "\n")
	return ui.HelpOverlayStyle.Render(body)
}

// renderSettingLine renders a single setting row with type-appropriate display.
func renderSettingLine(s SettingDef, prefs map[string]string, selected bool) string {
	val := settingVal(s.Key, prefs)
	prefix := "  "
	if selected {
		prefix = "▸ "
	}

	switch s.Kind {
	case SettingBool:
		icon := ui.IconCheckOff
		if val == "true" {
			icon = ui.IconCheckOn
		}
		line := prefix + icon + " " + s.Label
		if selected {
			return ui.FooterKeyStyle.Render(line)
		}
		return line

	case SettingEnum:
		var opts []string
		for _, o := range s.Options {
			if o == val {
				opts = append(opts, ui.FooterKeyStyle.Render(o))
			} else {
				opts = append(opts, ui.FooterDimStyle.Render(o))
			}
		}
		valueStr := strings.Join(opts, ui.FooterDimStyle.Render(" · "))
		label := prefix + s.Label
		if selected {
			return ui.FooterKeyStyle.Render(label) + "  " + valueStr
		}
		return label + "  " + valueStr

	case SettingInt:
		label := prefix + s.Label
		if selected {
			return ui.FooterKeyStyle.Render(label) + "  " + ui.FooterKeyStyle.Render(val)
		}
		return label + "  " + ui.FooterDimStyle.Render(val)
	}

	return prefix + s.Label
}
