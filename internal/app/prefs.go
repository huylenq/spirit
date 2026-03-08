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
	prefs := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			prefs[k] = v
		}
	}
	return prefs
}

func savePrefs(prefs map[string]string) {
	var lines []string
	for k, v := range prefs {
		lines = append(lines, k+"="+v)
	}
	_ = os.WriteFile(prefsPath(), []byte(strings.Join(lines, "\n")+"\n"), 0644)
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
