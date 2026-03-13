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
	m.NavigateMsgTo(min(max(m.msgCursor+delta, 0), len(m.userMessages)-1))
}

// NavigateMsgTo navigates directly to a specific message index.
func (m *DetailModel) NavigateMsgTo(idx int) {
	if idx < 0 || idx >= len(m.userMessages) {
		return
	}
	m.msgCursor = idx
	if idx < len(m.msgOffsets) && m.msgOffsets[idx] >= 0 {
		m.viewport.SetYOffset(m.msgOffsets[idx])
	}
}

// ChatOutlineMsgAt returns the user message index if the click at (localX, localY)
// falls within the chat outline panel, or -1 if it does not.
// localX and localY are coordinates relative to the detail view's rendered content
// (col 0 = first column of detail.View(), row 0 = first row of detail.View()).
func (m *DetailModel) ChatOutlineMsgAt(localX, localY int) int {
	if m.chatOutlineMode == chatOutlineHidden || len(m.userMessages) == 0 {
		return -1
	}

	contentWidth := m.width - 4
	panelWidth := m.effectivePanelWidth(contentWidth)

	// Determine the outline panel's left x within the detail view string.
	// Overlay: overlayAt places panel at col = (contentWidth+2) - panelWidth - 1
	// Docked:  rightCol starts at col 1 (contentBox border) + vpWidth + 1 (gap)
	var outlineLeft int
	switch m.chatOutlineMode {
	case chatOutlineOverlay:
		outlineLeft = contentWidth - panelWidth + 1
	case chatOutlineDocked:
		vpWidth := contentWidth - panelWidth - 3
		if vpWidth < 1 {
			vpWidth = 1
		}
		outlineLeft = vpWidth + 2
	case chatOutlineDockedLeft:
		outlineLeft = 1
	default:
		return -1
	}

	if localX < outlineLeft || localX >= outlineLeft+panelWidth {
		return -1
	}

	// Outline panel starts at detail-view row 4:
	// header=3 rows (line1, sessionTitle, blank) + contentBox top border=1 row.
	const outlineStartRow = 4
	if localY < outlineStartRow {
		return -1
	}

	outlineRow := localY - outlineStartRow
	if outlineRow == 0 {
		return -1 // top border of outline panel
	}
	contentRow := outlineRow - 1 // 0=title, 1=blank, 2+=messages

	if contentRow < 2 {
		return -1
	}

	// Mirror renderChatOutline() line counting to map contentRow → message index.
	innerWidth := panelWidth - 4
	if innerWidth < 5 {
		innerWidth = 5
	}
	msgWidth := innerWidth - 2
	if msgWidth < 1 {
		msgWidth = 1
	}
	row := 2
	for i, msg := range m.userMessages {
		if row > contentRow {
			return -1
		}
		flat := strings.ReplaceAll(msg, "\n", " ")
		if contentRow == row {
			return i
		}
		row++
		if ansi.StringWidth(flat) > msgWidth {
			if contentRow == row {
				return i
			}
			row++
		}
	}
	return -1
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
