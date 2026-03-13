package search

import (
	"strings"
	"unicode"
)

var stopWords = map[string]struct{}{
	"the": {}, "is": {}, "at": {}, "which": {}, "on": {},
	"a": {}, "an": {}, "and": {}, "or": {}, "but": {},
	"in": {}, "with": {}, "to": {}, "for": {}, "of": {},
	"it": {}, "this": {}, "that": {}, "be": {}, "are": {},
	"was": {}, "were": {}, "been": {}, "being": {}, "have": {},
	"has": {}, "had": {}, "do": {}, "does": {}, "did": {},
	"will": {}, "would": {}, "could": {}, "should": {}, "may": {},
	"might": {}, "can": {}, "shall": {}, "not": {}, "no": {},
	"from": {}, "by": {}, "as": {}, "if": {}, "then": {},
	"than": {}, "so": {}, "such": {}, "each": {}, "every": {},
	"all": {}, "any": {}, "few": {}, "more": {}, "most": {},
	"other": {}, "some": {}, "only": {}, "own": {}, "same": {},
	"too": {}, "very": {}, "just": {}, "also": {}, "now": {},
	"here": {}, "there": {}, "when": {}, "where": {}, "how": {},
	"what": {}, "who": {}, "whom": {}, "why": {}, "into": {},
	"out": {}, "up": {}, "down": {}, "about": {}, "over": {},
	"after": {}, "before": {}, "between": {}, "under": {}, "again": {},
	"further": {}, "once": {},
}

// ExtractKeywords lowercases text, splits on whitespace/punctuation,
// removes stop words, and deduplicates.
func ExtractKeywords(text string) []string {
	// Split on non-alphanumeric characters
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	seen := make(map[string]struct{}, len(tokens))
	var keywords []string
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if _, isStop := stopWords[tok]; isStop {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		keywords = append(keywords, tok)
	}
	return keywords
}

// ScoreKeywordMatch returns the fraction of keywords found in content
// (case-insensitive substring match). Score = matchedKeywords / totalKeywords.
func ScoreKeywordMatch(content string, keywords []string) float64 {
	if len(keywords) == 0 {
		return 0
	}
	lower := strings.ToLower(content)
	matched := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			matched++
		}
	}
	return float64(matched) / float64(len(keywords))
}

// NormalizeScores applies min-max normalization to [0, 1] range.
// If all scores are equal, returns all 1.0.
func NormalizeScores(scores []float64) []float64 {
	if len(scores) == 0 {
		return nil
	}

	min, max := scores[0], scores[0]
	for _, s := range scores[1:] {
		if s < min {
			min = s
		}
		if s > max {
			max = s
		}
	}

	result := make([]float64, len(scores))
	if max == min {
		for i := range result {
			result[i] = 1.0
		}
		return result
	}

	span := max - min
	for i, s := range scores {
		result[i] = (s - min) / span
	}
	return result
}

// BM25RankToScore converts a rank position (0-based) to a score via 1.0 / (1.0 + rank).
func BM25RankToScore(rank int) float64 {
	return 1.0 / (1.0 + float64(rank))
}
