package ui

import (
	"encoding/json"
	"strings"
)

// Scroll and navigation methods for DetailModel.

func (m *DetailModel) scrollDown(n int) {
	if m.showDiffs {
		m.diffScroll += n
		return
	}
	if m.showRawTranscript {
		total := len(m.transcriptEntries)
		for range n {
			if m.transcriptCursor < total-1 {
				m.transcriptCursor++
			}
		}
		m.ensureTranscriptCursorVisible()
		return
	}
	if m.showHooks {
		total := len(m.hookFiltered)
		for range n {
			if m.hookCursor < total-1 {
				m.hookCursor++
			}
		}
		m.ensureHookCursorVisible()
		return
	}
	m.viewport.LineDown(n)
}

func (m *DetailModel) scrollUp(n int) {
	if m.showDiffs {
		m.diffScroll -= n
		if m.diffScroll < 0 {
			m.diffScroll = 0
		}
		return
	}
	if m.showRawTranscript {
		for range n {
			if m.transcriptCursor > 0 {
				m.transcriptCursor--
			}
		}
		m.ensureTranscriptCursorVisible()
		return
	}
	if m.showHooks {
		for range n {
			if m.hookCursor > 0 {
				m.hookCursor--
			}
		}
		if m.hookCursor < m.hookScroll {
			m.hookScroll = m.hookCursor
		}
		return
	}
	m.viewport.LineUp(n)
}

// ensureTranscriptCursorVisible adjusts transcriptScroll so the cursor is in view,
// accounting for expanded entries consuming extra lines.
func (m *DetailModel) ensureTranscriptCursorVisible() {
	avail := m.transcriptVisLines()
	if avail < 1 {
		return
	}
	// If cursor is above scroll, just scroll up to cursor
	if m.transcriptCursor < m.transcriptScroll {
		m.transcriptScroll = m.transcriptCursor
		return
	}
	// Count visual lines from scroll to cursor (inclusive)
	usedLines := 0
	for i := m.transcriptScroll; i <= m.transcriptCursor && i < len(m.transcriptEntries); i++ {
		usedLines++ // summary line
		if m.transcriptExpanded[i] {
			usedLines += m.expandedLineCount(i)
		}
	}
	// If cursor line extends past visible area, scroll forward
	for usedLines > avail && m.transcriptScroll < m.transcriptCursor {
		// Remove lines consumed by the top entry
		usedLines-- // summary line
		if m.transcriptExpanded[m.transcriptScroll] {
			usedLines -= m.expandedLineCount(m.transcriptScroll)
		}
		m.transcriptScroll++
	}
}

func (m *DetailModel) halfPage() int {
	h := m.viewport.Height / 2
	if h < 1 {
		h = 1
	}
	return h
}

func (m *DetailModel) fullPage() int {
	h := m.viewport.Height - 3
	if h < 1 {
		h = 1
	}
	return h
}

// ScrollDown scrolls half a page down (ctrl+d).
func (m *DetailModel) ScrollDown() { m.scrollDown(m.halfPage()) }

// ScrollUp scrolls half a page up (ctrl+u).
func (m *DetailModel) ScrollUp() { m.scrollUp(m.halfPage()) }

// ScrollPageDown scrolls a full page down (ctrl+f).
func (m *DetailModel) ScrollPageDown() { m.scrollDown(m.fullPage()) }

// ScrollPageUp scrolls a full page up (ctrl+b).
func (m *DetailModel) ScrollPageUp() { m.scrollUp(m.fullPage()) }

// ScrollLines scrolls the preview by n lines (positive = down, negative = up).
func (m *DetailModel) ScrollLines(n int) {
	if n > 0 {
		m.scrollDown(n)
	} else if n < 0 {
		m.scrollUp(-n)
	}
}

// transcriptVisLines returns the number of visible lines for the transcript entry overlay.
func (m *DetailModel) transcriptVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// expandedLineCount returns the number of extra lines an expanded entry consumes.
func (m *DetailModel) expandedLineCount(idx int) int {
	json := m.getExpandedJSON(idx)
	if json == "" {
		return 0
	}
	return strings.Count(json, "\n") + 1
}

// getExpandedJSON returns the pretty-printed JSON for an entry, caching lazily.
func (m *DetailModel) getExpandedJSON(idx int) string {
	if idx < 0 || idx >= len(m.transcriptEntries) {
		return ""
	}
	if cached, ok := m.transcriptExpandedJSON[idx]; ok {
		return cached
	}
	raw := m.transcriptEntries[idx].RawJSON
	var v interface{}
	if json.Unmarshal([]byte(raw), &v) != nil {
		m.transcriptExpandedJSON[idx] = raw
		return raw
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		m.transcriptExpandedJSON[idx] = raw
		return raw
	}
	result := string(b)
	m.transcriptExpandedJSON[idx] = result
	return result
}
