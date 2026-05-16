package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
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
	// Keep scroll position valid and cursor visible. The window indexes past
	// messages only — the current-turn block is always pinned at the bottom.
	pastCount := len(msgs) - 1
	if pastCount > maxOutlineMessages {
		m.outlineScrollTop = min(m.outlineScrollTop, pastCount-maxOutlineMessages)
	} else {
		m.outlineScrollTop = 0
	}
	m.ensureOutlineVisible(m.msgCursor)
}

// SetCurrentTurn updates the current-turn event stream and rebuilds the
// per-event line offsets used for navigation.
func (m *DetailModel) SetCurrentTurn(t claude.CurrentTurn) {
	m.currentTurn = t
	m.recomputeEventOffsets(m.wrapForViewport())
	// Clamp cursor in case the event count shrank (e.g., session switch).
	maxIdx := m.navStopCount() - 1
	if maxIdx < 0 {
		m.msgCursor = 0
	} else if m.msgCursor > maxIdx {
		m.msgCursor = maxIdx
	}
}

// recomputeOffsets rebuilds msgOffsets and eventSubOffsets. Offsets are in
// wrapped-content line indices — `viewport.YOffset` indexes the wrapped
// output, not the raw captured pane, so any mismatch makes navigation jump to
// the wrong line.
func (m *DetailModel) recomputeOffsets() {
	wrapped := m.wrapForViewport()
	m.msgOffsets = findMsgLineOffsets(wrapped, m.userMessages)
	m.recomputeEventOffsets(wrapped)
}

// recomputeEventOffsets anchors current-turn events to `⏺` markers in the
// (already-wrapped) content, starting just after the last user-message line.
// Each event gets a slice of sub-block offsets (one entry per merged source
// block) so ctrl+h/ctrl+l can step through individual edits inside a merged
// file event.
func (m *DetailModel) recomputeEventOffsets(searchContent string) {
	lastUserLine := -1
	if n := len(m.msgOffsets); n > 0 {
		lastUserLine = m.msgOffsets[n-1]
	}
	m.eventSubOffsets = findEventSubOffsets(searchContent, lastUserLine, m.currentTurn.Events)
}

// navStopCount is the total number of cursor positions: every user message
// plus every event in the current turn.
func (m *DetailModel) navStopCount() int {
	return len(m.userMessages) + len(m.currentTurn.Events)
}

// NavigateMsg moves the message cursor by delta and scrolls the viewport to
// the target user message or event line.
func (m *DetailModel) NavigateMsg(delta int) {
	total := m.navStopCount()
	if total == 0 {
		return
	}
	m.NavigateMsgTo(min(max(m.msgCursor+delta, 0), total-1))
}

// NavigateMsgTo navigates directly to a specific cursor index. Indices in
// [0, len(userMessages)) target user messages; [len(userMessages), navStopCount())
// target events. Moving to a different cursor stop resets the sub-cursor.
func (m *DetailModel) NavigateMsgTo(idx int) {
	total := m.navStopCount()
	if idx < 0 || idx >= total {
		return
	}
	if idx != m.msgCursor {
		m.startCursorPulse()
	}
	m.msgCursor = idx
	m.subCursor = 0
	m.ensureOutlineVisible(idx)

	lineOffset := -1
	if idx < len(m.userMessages) {
		if idx < len(m.msgOffsets) {
			lineOffset = m.msgOffsets[idx]
		}
	} else {
		evIdx := idx - len(m.userMessages)
		if evIdx < len(m.eventSubOffsets) && len(m.eventSubOffsets[evIdx]) > 0 {
			lineOffset = m.eventSubOffsets[evIdx][0]
		}
	}
	if lineOffset >= 0 {
		m.viewport.SetYOffset(lineOffset)
		m.stickyBottom = m.viewport.AtBottom()
	}
}

// NavigateSubMsg steps the sub-cursor within the focused event (e.g. through
// consecutive edits to the same file that got merged into one outline row).
// No-op when the cursor isn't on an event, or the focused event has only one
// sub-block. Delta is +1 (next) or -1 (prev), clamped within bounds.
func (m *DetailModel) NavigateSubMsg(delta int) {
	if m.msgCursor < len(m.userMessages) {
		return
	}
	evIdx := m.msgCursor - len(m.userMessages)
	if evIdx >= len(m.eventSubOffsets) {
		return
	}
	subs := m.eventSubOffsets[evIdx]
	if len(subs) < 2 {
		return
	}
	next := m.subCursor + delta
	if next < 0 {
		next = 0
	}
	if next >= len(subs) {
		next = len(subs) - 1
	}
	if next == m.subCursor {
		return
	}
	m.subCursor = next
	m.startCursorPulse()
	if subs[next] >= 0 {
		m.viewport.SetYOffset(subs[next])
		m.stickyBottom = m.viewport.AtBottom()
	}
}

// FocusedSubInfo reports the focused event's sub-cursor position (1-based) and
// its sub-block total. Returns (0, 0) when the cursor isn't on a merged event
// (so the renderer can skip the `(p/N)` indicator).
func (m *DetailModel) FocusedSubInfo() (pos, total int) {
	if m.msgCursor < len(m.userMessages) {
		return 0, 0
	}
	evIdx := m.msgCursor - len(m.userMessages)
	if evIdx >= len(m.eventSubOffsets) {
		return 0, 0
	}
	n := len(m.eventSubOffsets[evIdx])
	if n < 2 {
		return 0, 0
	}
	return m.subCursor + 1, n
}

// ensureOutlineVisible adjusts outlineScrollTop so that idx is within the
// visible window of past messages. The current-turn message (last index) is
// always rendered, so no scrolling is needed when the cursor lands there.
func (m *DetailModel) ensureOutlineVisible(idx int) {
	lastIdx := len(m.userMessages) - 1
	if idx >= lastIdx {
		return
	}
	if idx < m.outlineScrollTop {
		m.outlineScrollTop = idx
	} else if idx >= m.outlineScrollTop+maxOutlineMessages {
		m.outlineScrollTop = idx - maxOutlineMessages + 1
	}
	m.outlineScrollTop = max(0, m.outlineScrollTop)
}

// outlinePastWindow returns the visible past-message range [start, end) for
// the chat outline scroll-back. The current turn (last index) is excluded —
// it's always rendered separately below the scroll-back.
func (m *DetailModel) outlinePastWindow() (visStart, visEnd int) {
	pastCount := len(m.userMessages) - 1
	if pastCount <= 0 {
		return 0, 0
	}
	visStart = m.outlineScrollTop
	if visStart > pastCount {
		visStart = pastCount
	}
	visEnd = min(visStart+maxOutlineMessages, pastCount)
	return
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

	// Outline panel starts at detail-view row 3:
	// header=2 rows (line1, sessionTitle) + contentBox top border=1 row.
	const outlineStartRow = 3
	if localY < outlineStartRow {
		return -1
	}

	outlineRow := localY - outlineStartRow
	var contentRow int
	if m.chatOutlineMode == chatOutlineDockedLeft {
		// No outline border — outlineRow 0 is already content (title)
		contentRow = outlineRow
	} else {
		if outlineRow == 0 {
			return -1 // outline panel top border
		}
		contentRow = outlineRow - 1
	}
	// contentRow: 0=title, 1=blank, 2+=messages

	if contentRow < 2 {
		return -1
	}

	// Mirror renderChatOutline() line counting to map contentRow → message index.
	innerWidth := panelWidth - 4
	if innerWidth < 5 {
		innerWidth = 5
	}
	msgWidth := max(1, innerWidth-outlineIndicatorWidth())
	row := 2

	visStart, visEnd := m.outlinePastWindow()
	if visStart > 0 {
		row++ // "↑ N more" line
	}
	for i := visStart; i < visEnd; i++ {
		if row > contentRow {
			return -1
		}
		if contentRow == row {
			return i
		}
		row++
	}
	lastIdx := len(m.userMessages) - 1
	if visEnd < lastIdx {
		if contentRow == row {
			return -1 // "↓ N more" line
		}
		row++
	}
	// Separator row (only rendered when there's scroll-back) maps to nothing.
	if lastIdx > 0 {
		if contentRow == row {
			return -1
		}
		row++
	}
	// Current user message spans up to outlineLastUserMaxLines rows; every
	// row maps to the same lastIdx cursor stop.
	flat := stripOutlinePrefix(strings.ReplaceAll(m.userMessages[lastIdx], "\n", " "))
	wrapped := strings.Split(WordWrapContent(flat, msgWidth), "\n")
	if len(wrapped) > outlineLastUserMaxLines {
		wrapped = wrapped[:outlineLastUserMaxLines]
	}
	for range wrapped {
		if contentRow == row {
			return lastIdx
		}
		row++
	}
	// Current-turn events: 1 row per event, in order.
	for ei := range m.currentTurn.Events {
		if contentRow == row {
			return lastIdx + 1 + ei
		}
		row++
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
		// Strip type-prefix glyphs (bash/plan/slash) — they exist in the outline
		// data but not in the terminal capture, so searching with them fails.
		msg = stripOutlinePrefix(msg)
		// Interruption messages are stored as "[Request interrupted by user]" in the
		// transcript but Claude Code renders them as "Interrupted" in the terminal.
		if strings.HasPrefix(msg, "[Request interrupted") {
			msg = "Interrupted"
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

// findEventSubOffsets returns per-event slices of `⏺ ` line offsets in the
// captured pane content, after the last user-message line. Each inner slice
// contains one entry per merged source block (length == BlockCount), so
// ctrl+h/ctrl+l can step through merged edits. Missing offsets (event scrolled
// past scrollback) are -1.
//
// The captured pane prints `⏺` for every assistant block AND every tool call
// (Bash, Read, Grep, ...), but `events` only contains navigable kinds (text,
// Edit, Write, MultiEdit). Markers that don't match the expected event kind
// are skipped, so non-navigable tool calls in the stream don't desync the
// offset alignment.
func findEventSubOffsets(content string, lastUserLine int, events []claude.TurnEvent) [][]int {
	out := make([][]int, len(events))
	for i, ev := range events {
		out[i] = make([]int, eventBlockCount(ev))
		for j := range out[i] {
			out[i][j] = -1
		}
	}
	if content == "" || len(events) == 0 || lastUserLine < 0 {
		return out
	}
	contentLines := strings.Split(content, "\n")
	if lastUserLine >= len(contentLines) {
		return out
	}

	eventIdx := 0
	consumed := 0
	need := eventBlockCount(events[0])
	for li := lastUserLine + 1; li < len(contentLines) && eventIdx < len(events); li++ {
		stripped := strings.TrimSpace(ansi.Strip(contentLines[li]))
		if !strings.HasPrefix(stripped, "⏺") {
			continue
		}
		if !markerMatchesEventKind(stripped, events[eventIdx].Kind) {
			continue
		}
		if consumed < len(out[eventIdx]) {
			out[eventIdx][consumed] = li
		}
		consumed++
		if consumed >= need {
			eventIdx++
			consumed = 0
			if eventIdx < len(events) {
				need = eventBlockCount(events[eventIdx])
			}
		}
	}
	return out
}

// markerMatchesEventKind reports whether a `⏺ ...` line from the captured pane
// corresponds to the given event kind. Text events match lines that aren't a
// `ToolName(...)` tool-call marker; file events match the specific tool names
// Claude Code prints for that kind (e.g. Edit renders as `Update(path)`).
func markerMatchesEventKind(line string, kind claude.TurnEventKind) bool {
	toolName, isTool := parseMarkerToolName(line)
	switch kind {
	case claude.TurnEventText:
		return !isTool
	case claude.TurnEventEdit, claude.TurnEventMultiEdit:
		if !isTool {
			return false
		}
		return toolName == "Update" || toolName == "Edit" || toolName == "MultiEdit"
	case claude.TurnEventWrite:
		return isTool && toolName == "Write"
	}
	return false
}

// parseMarkerToolName extracts the tool name from a `⏺ Name(...)` line. It
// returns (name, true) only when the content immediately after `⏺` is a
// capitalized identifier followed by `(` — the shape Claude Code uses to
// print every tool call. For prose text after `⏺`, returns ("", false).
func parseMarkerToolName(line string) (string, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(line, "⏺"))
	if s == "" {
		return "", false
	}
	if s[0] < 'A' || s[0] > 'Z' {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' {
			if i == 0 {
				return "", false
			}
			return s[:i], true
		}
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return "", false
		}
	}
	return "", false
}

func eventBlockCount(e claude.TurnEvent) int {
	if e.BlockCount < 1 {
		return 1
	}
	return e.BlockCount
}
