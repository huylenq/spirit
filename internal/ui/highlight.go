package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// fuzzyFindOne runs sahilm/fuzzy against a single text string.
// Returns matched byte indices and quality score (-1 if no match).
// The library penalizes by (numMatched - textByteLen) for ranking across
// texts of different lengths. We remove that penalty to get a quality score
// based purely on match-position bonuses (word boundary, consecutive, etc.).
func fuzzyFindOne(text, pattern string) ([]int, int) {
	if pattern == "" {
		return nil, 0
	}
	if text == "" {
		return nil, -1
	}

	matches := fuzzy.Find(pattern, []string{text})
	if len(matches) == 0 {
		return nil, -1
	}

	m := matches[0]
	quality := m.Score + len(text) - len(m.MatchedIndexes)
	return m.MatchedIndexes, quality
}

// fuzzyMatch returns matched byte indices and whether the match passes quality threshold.
// Prefers word-boundary and consecutive matches; rejects scattered low-quality
// subsequences that the old greedy algorithm would have accepted as false positives.
func fuzzyMatch(text, pattern string) ([]int, bool) {
	indices, quality := fuzzyFindOne(text, pattern)
	return indices, quality >= 0
}

// fuzzyScore returns the quality score for a fuzzy match, or -1 if no match.
// Higher scores indicate better match quality (word boundaries, consecutive, etc.).
func fuzzyScore(text, pattern string) int {
	_, quality := fuzzyFindOne(text, pattern)
	if quality < 0 {
		return -1
	}
	return quality
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
