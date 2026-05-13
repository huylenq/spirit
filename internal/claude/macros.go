package claude

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed builtin_macros/*.lua
var builtinMacroFS embed.FS

var (
	builtinMacrosOnce   sync.Once
	builtinMacrosCached []Macro
)

// BuiltinMacros returns the macros embedded in the binary at build time.
// Filename stem (e.g. "C" for "C.lua") is used as the key fallback if the
// "-- key:" header is missing. Result is computed once and cached.
func BuiltinMacros() []Macro {
	builtinMacrosOnce.Do(func() {
		entries, err := builtinMacroFS.ReadDir("builtin_macros")
		if err != nil {
			return
		}
		out := make([]Macro, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
				continue
			}
			data, err := builtinMacroFS.ReadFile("builtin_macros/" + e.Name())
			if err != nil {
				continue
			}
			script := string(data)
			key, name, _ := ParseMacroHeader(script)
			if key == "" {
				key = strings.TrimSuffix(e.Name(), ".lua")
			}
			if name == "" {
				name = key
			}
			out = append(out, Macro{Key: key, Name: name, Script: script, BuiltIn: true})
		}
		builtinMacrosCached = out
	})
	return builtinMacrosCached
}

// Macro represents a runnable Lua macro with a single-key trigger.
type Macro struct {
	Key     string // single character filename stem (e.g. "s")
	Name    string // display name from "-- name: ..." header
	Script  string // full Lua source
	BuiltIn bool   // true if embedded, false if user-defined
}

// MacroDir returns the directory where user macros are stored.
func MacroDir() string {
	return filepath.Join(StatusDir(), "macros")
}

// MacroFilePath returns the full path to a macro file for the given key.
func MacroFilePath(key string) string {
	return filepath.Join(MacroDir(), key+".lua")
}

// LoadMacros reads user macros from disk and merges with built-ins.
// User macros override built-ins with the same key.
// If builtins is nil, the embedded built-in macros are used.
func LoadMacros(builtins []Macro) []Macro {
	if builtins == nil {
		builtins = BuiltinMacros()
	}
	byKey := make(map[string]Macro)
	for _, m := range builtins {
		byKey[m.Key] = m
	}

	dir := MacroDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No user macros — return sorted built-ins
		return sortMacros(builtins)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
			continue
		}
		fileKey := strings.TrimSuffix(e.Name(), ".lua")
		if fileKey == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		script := string(data)
		_, name, _ := ParseMacroHeader(script)
		if name == "" {
			name = fileKey
		}
		byKey[fileKey] = Macro{Key: fileKey, Name: name, Script: script}
	}

	macros := make([]Macro, 0, len(byKey))
	for _, m := range byKey {
		macros = append(macros, m)
	}
	return sortMacros(macros)
}

// SaveMacro writes a macro to disk as <key>.lua with -- key: / -- name: headers.
func SaveMacro(key, name, body string) error {
	dir := MacroDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := "-- key: " + key + "\n-- name: " + name + "\n" + body
	return os.WriteFile(filepath.Join(dir, key+".lua"), []byte(script), 0o644)
}

// DeleteMacro removes a user macro file.
func DeleteMacro(key string) error {
	return os.Remove(filepath.Join(MacroDir(), key+".lua"))
}

// ParseMacroHeader extracts key, name, and body from a macro Lua source.
// Header lines are leading "-- key:" and "-- name:" comments; everything after is body.
func ParseMacroHeader(script string) (key, name, body string) {
	lines := strings.Split(script, "\n")
	bodyStart := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "-- key:"); ok {
			key = strings.TrimSpace(after)
			bodyStart = i + 1
		} else if after, ok := strings.CutPrefix(trimmed, "-- name:"); ok {
			name = strings.TrimSpace(after)
			bodyStart = i + 1
		} else {
			break
		}
	}
	if bodyStart < len(lines) {
		body = strings.Join(lines[bodyStart:], "\n")
	}
	return key, name, body
}

func sortMacros(macros []Macro) []Macro {
	sort.Slice(macros, func(i, j int) bool {
		return macros[i].Key < macros[j].Key
	})
	return macros
}
