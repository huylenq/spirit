package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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
