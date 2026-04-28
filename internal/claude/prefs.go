package claude

import (
	"os"
	"path/filepath"
	"strings"
)

// PrefsPath returns the path to spirit's prefs file.
func PrefsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "spirit", "prefs")
}

// LoadPrefs reads ~/.cache/spirit/prefs as a map. Empty file or missing file
// returns an empty (non-nil) map. The format is plain key=value, one per line.
func LoadPrefs() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(PrefsPath())
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// ReadPref returns a single pref value, or "" if absent.
func ReadPref(key string) string {
	return LoadPrefs()[key]
}
