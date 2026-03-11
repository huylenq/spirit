package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PrefDef defines a single editable preference.
type PrefDef struct {
	Key   string
	Label string
}

// PrefRegistry is the ordered list of all editable preferences.
var PrefRegistry = []PrefDef{
	{Key: "groupByProject", Label: "Group by project"},
	{Key: "minimap", Label: "Show minimap"},
	{Key: "minimapMode", Label: "Minimap mode"},
	{Key: "minimapMaxH", Label: "Minimap max height"},
	{Key: "minimapCollapse", Label: "Minimap collapse"},
	{Key: "sidebarWidthPct", Label: "Sidebar width %"},
	{Key: "autoSynthesize", Label: "Auto-synthesize on idle"},
}

// prefsFileContent returns the raw prefs file content as a string.
func prefsFileContent() string {
	data, err := os.ReadFile(prefsPath())
	if err != nil {
		return ""
	}
	return string(data)
}

// parsePrefsText parses key=value pairs from raw text.
func parsePrefsText(text string) map[string]string {
	prefs := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			prefs[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return prefs
}

// prefRegistryKeys returns all PrefRegistry key names.
func prefRegistryKeys() []string {
	keys := make([]string, len(PrefRegistry))
	for i, def := range PrefRegistry {
		keys[i] = def.Key
	}
	return keys
}

// prefRegistryLabels returns a map of key -> human label for all PrefRegistry entries.
func prefRegistryLabels() map[string]string {
	labels := make(map[string]string, len(PrefRegistry))
	for _, def := range PrefRegistry {
		labels[def.Key] = def.Label
	}
	return labels
}

func prefsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "cmc", "prefs")
}

func loadPrefs() map[string]string {
	data, err := os.ReadFile(prefsPath())
	if err != nil {
		return nil
	}
	return parsePrefsText(string(data))
}

func savePrefs(prefs map[string]string) {
	var lines []string
	for k, v := range prefs {
		lines = append(lines, k+"="+v)
	}
	_ = os.WriteFile(prefsPath(), []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// migratePref renames oldKey to newKey in the prefs file if newKey is absent.
func migratePref(oldKey, newKey string) {
	prefs := loadPrefs()
	if prefs == nil {
		return
	}
	if _, hasNew := prefs[newKey]; hasNew {
		return
	}
	if v, hasOld := prefs[oldKey]; hasOld {
		delete(prefs, oldKey)
		prefs[newKey] = v
		savePrefs(prefs)
	}
}

func loadPrefBool(key string) bool {
	prefs := loadPrefs()
	return prefs[key] == "true"
}

func savePrefBool(key string, val bool) {
	prefs := loadPrefs()
	if prefs == nil {
		prefs = map[string]string{}
	}
	if val {
		prefs[key] = "true"
	} else {
		delete(prefs, key)
	}
	savePrefs(prefs)
}

func loadPrefInt(key string, defaultVal int) int {
	prefs := loadPrefs()
	if v, ok := prefs[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}

func savePrefInt(key string, val int) {
	prefs := loadPrefs()
	if prefs == nil {
		prefs = map[string]string{}
	}
	prefs[key] = strconv.Itoa(val)
	savePrefs(prefs)
}

func loadPrefString(key, defaultVal string) string {
	prefs := loadPrefs()
	if v, ok := prefs[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

func savePrefString(key, val string) {
	prefs := loadPrefs()
	if prefs == nil {
		prefs = map[string]string{}
	}
	prefs[key] = val
	savePrefs(prefs)
}
