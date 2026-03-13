package search

import "strings"

const (
	DefaultMaxChars     = 1600 // ~400 tokens at 4:1 ratio
	DefaultOverlapChars = 320  // ~80 tokens
)

// ChunkText splits text into chunks of approximately maxChars characters with
// overlapChars carry-over between consecutive chunks. Splits on paragraph
// boundaries (\n\n) first, then line boundaries (\n), then at maxChars if no
// good boundary found.
func ChunkText(text string, maxChars, overlapChars int) []string {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if overlapChars < 0 {
		overlapChars = DefaultOverlapChars
	}
	if overlapChars >= maxChars {
		overlapChars = maxChars / 5
	}

	if len(text) <= maxChars {
		if len(text) == 0 {
			return nil
		}
		return []string{text}
	}

	var chunks []string
	pos := 0

	for pos < len(text) {
		end := pos + maxChars
		if end >= len(text) {
			chunks = append(chunks, text[pos:])
			break
		}

		// Try to find a paragraph boundary (\n\n) within the chunk
		splitAt := findLastBoundary(text[pos:end], "\n\n")
		if splitAt < 0 {
			// Try line boundary (\n)
			splitAt = findLastBoundary(text[pos:end], "\n")
		}
		if splitAt < 0 {
			// Hard split at maxChars
			splitAt = maxChars
		} else {
			// splitAt is relative to pos, advance past the boundary
			splitAt += len(findBoundaryAtPos(text[pos:end], splitAt))
		}

		chunks = append(chunks, text[pos:pos+splitAt])

		// Move forward, accounting for overlap
		advance := splitAt - overlapChars
		if advance <= 0 {
			advance = splitAt // Don't go backwards
		}
		pos += advance
	}

	return chunks
}

// findLastBoundary finds the last occurrence of sep in s and returns its index.
// Returns -1 if not found. Searches from end to find the best split point.
func findLastBoundary(s, sep string) int {
	// Don't split too early — require at least 25% of the chunk to be used
	minPos := len(s) / 4
	idx := strings.LastIndex(s, sep)
	if idx < minPos {
		return -1
	}
	return idx
}

// findBoundaryAtPos returns the boundary string found at the given position.
// Used to determine how many chars to skip past the boundary.
func findBoundaryAtPos(s string, pos int) string {
	if pos+2 <= len(s) && s[pos:pos+2] == "\n\n" {
		return "\n\n"
	}
	if pos+1 <= len(s) && s[pos:pos+1] == "\n" {
		return "\n"
	}
	return ""
}
