package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// highlightFilter renders text with matched substrings bold+underlined.
// Each segment gets exactly one Render call — no nesting.
func highlightFilter(text, filterLower string, baseStyle lipgloss.Style) string {
	if filterLower == "" || text == "" {
		return baseStyle.Render(text)
	}

	lower := strings.ToLower(text)
	idx := strings.Index(lower, filterLower)
	if idx < 0 {
		return baseStyle.Render(text)
	}

	matchStyle := baseStyle.Bold(true).Underline(true)
	var b strings.Builder
	pos := 0
	for idx >= 0 {
		if idx > pos {
			b.WriteString(baseStyle.Render(text[pos:idx]))
		}
		end := idx + len(filterLower)
		b.WriteString(matchStyle.Render(text[idx:end]))
		pos = end
		next := strings.Index(lower[pos:], filterLower)
		if next < 0 {
			break
		}
		idx = pos + next
	}
	if pos < len(text) {
		b.WriteString(baseStyle.Render(text[pos:]))
	}
	return b.String()
}

// containsFilter reports whether text contains filterLower (case-insensitive).
func containsFilter(text, filterLower string) bool {
	return strings.Contains(strings.ToLower(text), filterLower)
}
