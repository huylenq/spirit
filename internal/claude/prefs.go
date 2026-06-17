package claude

import (
	"os"
	"path/filepath"
	"strings"
)

// PrefsPath returns the path to spirit's prefs file.
func PrefsPath() string {
	return filepath.Join(StatusDir(), "prefs")
}

// LoadPrefs reads ~/.spirit/prefs as a map. Empty file or missing file
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

// projectCodePrefix namespaces per-project code assignments inside the flat
// prefs file (e.g. "projectcode.spirit=SPR").
const projectCodePrefix = "projectcode."

// ProjectCodeKey returns the prefs key for a project's code assignment.
func ProjectCodeKey(project string) string {
	return projectCodePrefix + project
}

// ProjectCodes returns the project→code map read from prefs (project basename →
// 3-char code), skipping blank values. Empty/missing prefs returns an empty
// (non-nil) map.
func ProjectCodes() map[string]string {
	out := map[string]string{}
	for k, v := range LoadPrefs() {
		if project, ok := strings.CutPrefix(k, projectCodePrefix); ok && v != "" {
			out[project] = v
		}
	}
	return out
}

// SuggestProjectCode derives a default 3-char uppercase code from a project
// basename: an acronym across word boundaries (-, _, space, camelCase) when the
// name has multiple tokens, else the first three letters. Returns "" for an
// empty/codeless name.
func SuggestProjectCode(project string) string {
	tokens := splitProjectTokens(project)
	var code strings.Builder
	if len(tokens) >= 2 {
		for _, t := range tokens {
			code.WriteByte(t[0])
			if code.Len() == 3 {
				break
			}
		}
	} else if len(tokens) == 1 {
		t := tokens[0]
		for i := 0; i < len(t) && code.Len() < 3; i++ {
			code.WriteByte(t[i])
		}
	}
	return strings.ToUpper(code.String())
}

// splitProjectTokens splits a basename into alphanumeric tokens on separators
// (-, _, space, .) and camelCase transitions, dropping empties.
func splitProjectTokens(project string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(project)
	for i, r := range runes {
		switch {
		case r == '-' || r == '_' || r == ' ' || r == '.':
			flush()
		case r >= 'A' && r <= 'Z' && i > 0 && runes[i-1] >= 'a' && runes[i-1] <= 'z':
			// camelCase boundary
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}
