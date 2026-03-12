package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// User message navigation for DetailModel.

func (m *DetailModel) SetUserMessages(msgs []string) {
	m.userMessages = msgs
	m.recomputeOffsets()
	if m.pendingMsgReset {
		m.pendingMsgReset = false
		m.msgCursor = len(msgs) - 1
		if m.msgCursor < 0 {
			m.msgCursor = 0
		}
	}
}

// recomputeOffsets rebuilds msgOffsets from the current content and userMessages.
func (m *DetailModel) recomputeOffsets() {
	m.msgOffsets = findMsgLineOffsets(m.content, m.userMessages)
}

// NavigateMsg moves the message cursor by delta (+1 = next, -1 = prev) and scrolls
// the viewport to that message's line in the pane capture.
func (m *DetailModel) NavigateMsg(delta int) {
	if len(m.userMessages) == 0 {
		return
	}
	m.msgCursor = min(max(m.msgCursor+delta, 0), len(m.userMessages)-1)
	if m.msgCursor < len(m.msgOffsets) && m.msgOffsets[m.msgCursor] >= 0 {
		m.viewport.SetYOffset(m.msgOffsets[m.msgCursor])
	}
}

// findMsgLineOffsets maps each user message to a line number in the terminal capture.
// Searches in order so that Claude quoting earlier messages doesn't trick the matcher.
// Returns -1 for messages not found in the capture (e.g. scrolled out of history).
func findMsgLineOffsets(content string, messages []string) []int {
	offsets := make([]int, len(messages))
	for i := range offsets {
		offsets[i] = -1
	}
	if content == "" || len(messages) == 0 {
		return offsets
	}

	contentLines := strings.Split(content, "\n")
	searchFrom := 0

	for mi, msg := range messages {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		// Use only the first line of the message (multiline messages wrap in the terminal)
		firstLine := msg
		if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
			firstLine = msg[:idx]
		}
		firstLine = strings.TrimSpace(firstLine)
		if firstLine == "" {
			continue
		}
		// Limit to first 50 runes — long enough to be specific, short enough to avoid wrapping issues
		needle := firstNRunes(firstLine, 50)

		for li := searchFrom; li < len(contentLines); li++ {
			// Strip ANSI escape codes before comparing
			if strings.Contains(ansi.Strip(contentLines[li]), needle) {
				offsets[mi] = li
				searchFrom = li + 1
				break
			}
		}
	}

	return offsets
}
