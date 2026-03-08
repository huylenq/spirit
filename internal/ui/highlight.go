package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// fuzzyMatch checks if all characters of patternLower appear in order in text (case-insensitive).
// Returns the byte indices of matched characters and whether the match succeeded.
func fuzzyMatch(text, patternLower string) ([]int, bool) {
	if patternLower == "" {
		return nil, true
	}
	if text == "" {
		return nil, false
	}

	lower := strings.ToLower(text)
	var indices []int
	pos := 0
	for _, pr := range patternLower {
		found := strings.IndexRune(lower[pos:], pr)
		if found < 0 {
			return nil, false
		}
		byteIdx := pos + found
		indices = append(indices, byteIdx)
		pos = byteIdx + utf8.RuneLen(pr)
	}
	return indices, true
}

// highlightMatch renders text with fuzzy-matched characters bold+underlined.
// Each run of matched/unmatched characters gets exactly one Render call.
func highlightMatch(text, query string, baseStyle lipgloss.Style) string {
	if query == "" || text == "" {
		return baseStyle.Render(text)
	}

	indices, ok := fuzzyMatch(text, query)
	if !ok || len(indices) == 0 {
		return baseStyle.Render(text)
	}

	matchStyle := baseStyle.Bold(true).Underline(true)

	matchSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		matchSet[idx] = true
	}

	var b strings.Builder
	var run strings.Builder
	inMatch := false

	for i, r := range text {
		isMatch := matchSet[i]
		if isMatch != inMatch && run.Len() > 0 {
			if inMatch {
				b.WriteString(matchStyle.Render(run.String()))
			} else {
				b.WriteString(baseStyle.Render(run.String()))
			}
			run.Reset()
			inMatch = isMatch
		} else if run.Len() == 0 {
			inMatch = isMatch
		}
		run.WriteRune(r)
	}
	if run.Len() > 0 {
		if inMatch {
			b.WriteString(matchStyle.Render(run.String()))
		} else {
			b.WriteString(baseStyle.Render(run.String()))
		}
	}

	return b.String()
}

// matchesNarrow reports whether text fuzzy-matches query (case-insensitive).
func matchesNarrow(text, query string) bool {
	_, ok := fuzzyMatch(text, query)
	return ok
}
