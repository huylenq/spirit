package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// Transcript display modes (must match constants in internal/app/model.go).
const (
	transcriptOverlay = "overlay"
	transcriptDocked  = "docked"
	transcriptHidden  = "hidden"
)

// diffFileStat is a pre-sorted, per-file diff entry cached on SetDiffStats.
type diffFileStat struct {
	name      string
	added     int
	removed   int
	footprint int
}

type DetailModel struct {
	viewport               viewport.Model
	session                *claude.ClaudeSession
	content                string
	userMessages           []string
	msgOffsets             []int // line index in content for each userMessage; -1 if not found
	msgCursor              int   // which user message we last navigated to
	pendingMsgReset        bool  // set on session switch; reset msgCursor when messages arrive
	diffStats              map[string]claude.FileDiffStat
	diffFiles              []diffFileStat // cached sorted file entries
	summary                *claude.SessionSummary
	memo                   string           // freeform note for this session (empty = no panel)
	memoEditor             MemoEditorModel  // inline textarea for editing the note
	memoEditing            bool             // true while the note is being edited
	relayView              string // when set, rendered inline after the ❯ prompt line
	hookEvents             []claude.HookEvent
	showHooks              bool
	transcriptMode         string // transcriptOverlay, transcriptDocked, transcriptHidden
	hookCursor             int
	hookExpanded           map[int]bool   // per-entry expansion (keyed by filtered index)
	hookExpandedJSON       map[int]string // lazy pretty-print cache
	hookScroll             int
	hookFilter             int                // 0=all, 1=handled only, 2=unhandled only
	hookFiltered           []claude.HookEvent // cached filtered+reversed slice
	transcriptEntries      []claude.TranscriptEntry
	transcriptCursor       int            // selected entry index
	transcriptScroll       int            // first visible entry index
	transcriptExpanded     map[int]bool   // which entries are expanded
	transcriptExpandedJSON map[int]string // lazy pretty-print cache
	transcriptMaxTypeW     int            // cached max width of Type column
	transcriptMaxCTypeW    int            // cached max width of ContentType column
	showRawTranscript      bool
	showDiffs              bool
	diffHunks              []claude.FileDiffHunk
	diffHunkFiles          []diffHunkFile
	diffScroll             int
	diffSimThreshold       float64 // similarity threshold for ~ vs separate -/+ lines
	width                  int
	height                 int
	ready                  bool
}

func NewDetailModel() DetailModel {
	return DetailModel{
		diffSimThreshold: defaultDiffSimThreshold,
		memoEditor:       NewMemoEditorModel(),
	}
}

func (m *DetailModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	headerHeight := 3 // title + branch/diff + blank line
	footerHeight := 2 // metadata line + blank
	borderHeight := 2 // top + bottom border of content box
	contentHeight := h - headerHeight - footerHeight - borderHeight
	if contentHeight < 1 {
		contentHeight = 1
	}
	vpWidth := m.effectiveVPWidth(w)
	if !m.ready {
		m.viewport = viewport.New(vpWidth, contentHeight)
		m.ready = true
		if m.content != "" {
			m.viewport.SetContent(wrapLines(trimTrailingBlanks(m.content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
			m.viewport.GotoBottom() // content arrived before size was known
		}
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = contentHeight
		if m.content != "" {
			m.viewport.SetContent(wrapLines(trimTrailingBlanks(m.content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
		}
	}
}

// calcPanelWidth returns the sidebar/overlay panel width for a given content width,
// clamped to [20, 50].
func calcPanelWidth(contentWidth int) int {
	w := contentWidth * 40 / 100
	if w < 20 {
		w = 20
	}
	if w > 50 {
		w = 50
	}
	return w
}

// hasSidebarContent reports whether any panel content (transcript, summary, memo)
// exists that would be shown in the sidebar/overlay.
func (m *DetailModel) hasSidebarContent() bool {
	return len(m.userMessages) > 0 || m.summary != nil || m.memo != "" || m.memoEditing
}

// effectiveVPWidth returns the viewport width accounting for transcript mode.
// In docked mode, the viewport is narrower to make room for the side panel.
func (m *DetailModel) effectiveVPWidth(w int) int {
	contentWidth := w - 4
	vpWidth := w - 6 // content box border (2) + outer padding (4)
	if vpWidth < 1 {
		vpWidth = 1
	}
	if m.transcriptMode == transcriptDocked && m.hasSidebarContent() {
		vpWidth = contentWidth - calcPanelWidth(contentWidth) - 3 // 1 gap + 2 for content border
		if vpWidth < 1 {
			vpWidth = 1
		}
	}
	return vpWidth
}

// effectiveDividerWidth returns the max width for reconstructing horizontal rule
// labels. In overlay mode the label must sit left of the overlay panel; in other
// modes it matches the viewport width.
func (m *DetailModel) effectiveDividerWidth(vpWidth int) int {
	if m.transcriptMode == transcriptOverlay && m.hasSidebarContent() {
		w := vpWidth - calcPanelWidth(m.width-4) - 1
		if w < 1 {
			return 1
		}
		return w
	}
	return vpWidth
}

// ClearSession resets the preview to the empty "Select a session" state.
func (m *DetailModel) ClearSession() {
	m.session = nil
	m.content = ""
	m.userMessages = nil
	m.diffFiles = nil
	m.summary = nil
}

// SetNonClaudePane shows a raw terminal capture for a non-Claude pane.
func (m *DetailModel) SetNonClaudePane(paneID string, paneTitle string, content string) {
	title := paneTitle
	if title == "" {
		title = paneID
	}
	isNew := m.session == nil || m.session.PaneID != paneID
	m.session = &claude.ClaudeSession{PaneID: paneID, Project: title}
	if isNew {
		m.userMessages = nil
		m.diffFiles = nil
		m.diffStats = nil
		m.summary = nil
	}
	if m.content == content {
		return // skip re-render when content unchanged (daemon re-captures every ~1s)
	}
	m.content = content
	if m.ready {
		m.viewport.SetContent(wrapLines(trimTrailingBlanks(content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
		if isNew {
			m.viewport.GotoBottom()
		}
	}
}

func (m *DetailModel) SetSession(s *claude.ClaudeSession, content string) {
	isNewSession := m.session == nil || m.session.PaneID != s.PaneID
	if isNewSession {
		// Don't clear diffStats, userMessages, or summary here — they're set
		// by independent async messages (DiffStatsReadyMsg, TranscriptReadyMsg,
		// SummaryReadyMsg) that may arrive before this call. Those handlers
		// already guard against cross-session contamination via PaneID checks.
		m.hookScroll = 0
		m.pendingMsgReset = true
	}
	m.session = s
	m.content = content
	m.recomputeOffsets()
	if m.ready {
		m.viewport.SetContent(wrapLines(trimTrailingBlanks(content), m.viewport.Width, m.effectiveDividerWidth(m.viewport.Width)))
		if isNewSession {
			m.viewport.GotoBottom()
		}
	}
}

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

func (m *DetailModel) SetDiffStats(stats map[string]claude.FileDiffStat) {
	m.diffStats = stats
	// Pre-sort file entries by footprint so View() doesn't re-sort every frame
	files := make([]diffFileStat, 0, len(stats))
	for f, ds := range stats {
		parts := strings.Split(f, "/")
		files = append(files, diffFileStat{
			name: parts[len(parts)-1], added: ds.Added, removed: ds.Removed,
			footprint: ds.Added + ds.Removed,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].footprint != files[j].footprint {
			return files[i].footprint > files[j].footprint
		}
		return files[i].name < files[j].name
	})
	m.diffFiles = files
}

func (m *DetailModel) SetSummary(s *claude.SessionSummary) {
	m.summary = s
}

func (m *DetailModel) SetMemo(memo string) {
	m.memo = memo
}

// StartMemoEdit activates inline editing of the note panel.
func (m *DetailModel) StartMemoEdit() {
	m.memoEditing = true
	m.memoEditor.Activate(m.memo)
}

// StopMemoEdit deactivates the inline editor without saving.
func (m *DetailModel) StopMemoEdit() {
	m.memoEditing = false
	m.memoEditor.Deactivate()
}

// MemoEditing reports whether the note panel is in edit mode.
func (m *DetailModel) MemoEditing() bool { return m.memoEditing }

// MemoValue returns the current textarea content (only meaningful while editing).
func (m *DetailModel) MemoValue() string { return m.memoEditor.Value() }

// UpdateMemoEditor forwards a message to the textarea and returns any cmd.
func (m *DetailModel) UpdateMemoEditor(msg tea.Msg) tea.Cmd {
	return m.memoEditor.Update(msg)
}

func (m *DetailModel) SetTranscriptMode(mode string) {
	m.transcriptMode = mode
	if m.ready {
		vpWidth := m.effectiveVPWidth(m.width)
		m.viewport.Width = vpWidth
		if m.content != "" {
			m.viewport.SetContent(wrapLines(trimTrailingBlanks(m.content), vpWidth, m.effectiveDividerWidth(vpWidth)))
		}
	}
}

func (m *DetailModel) SetShowHooks(show bool) {
	m.showHooks = show
	m.hookScroll = 0
	m.hookCursor = 0
	m.hookFilter = 0
	m.hookFiltered = nil
	if show {
		m.hookExpanded = make(map[int]bool)
		m.hookExpandedJSON = make(map[int]string)
	} else {
		m.hookEvents = nil
		m.hookExpanded = nil
		m.hookExpandedJSON = nil
	}
}

// CycleHookFilter cycles through hook filter modes: all → handled → unhandled → all.
func (m *DetailModel) CycleHookFilter() {
	m.hookFilter = (m.hookFilter + 1) % 3
	m.hookCursor = 0
	m.hookScroll = 0
	m.hookExpanded = make(map[int]bool)
	m.hookExpandedJSON = make(map[int]string)
	m.rebuildHookFiltered()
}

// rebuildHookFiltered rebuilds the cached filtered+reversed hook event list.
func (m *DetailModel) rebuildHookFiltered() {
	filtered := make([]claude.HookEvent, 0, len(m.hookEvents))
	for i := len(m.hookEvents) - 1; i >= 0; i-- {
		ev := m.hookEvents[i]
		handled := hookIsHandled(ev)
		switch m.hookFilter {
		case 1: // handled only
			if !handled {
				continue
			}
		case 2: // unhandled only
			if handled {
				continue
			}
		}
		filtered = append(filtered, ev)
	}
	m.hookFiltered = filtered
}

func (m *DetailModel) ToggleExpand() {
	if m.showRawTranscript {
		if m.transcriptExpanded == nil {
			m.transcriptExpanded = make(map[int]bool)
		}
		m.transcriptExpanded[m.transcriptCursor] = !m.transcriptExpanded[m.transcriptCursor]
		return
	}
	if !m.showHooks {
		return
	}
	if m.hookExpanded == nil {
		m.hookExpanded = make(map[int]bool)
	}
	m.hookExpanded[m.hookCursor] = !m.hookExpanded[m.hookCursor]
}

func (m *DetailModel) SetRelayView(v string) {
	m.relayView = v
}

func (m *DetailModel) SetHookEvents(events []claude.HookEvent) {
	m.hookEvents = events
	m.hookExpanded = make(map[int]bool)       // reset — filtered indices shift
	m.hookExpandedJSON = make(map[int]string) // invalidate cache
	m.rebuildHookFiltered()
}

func (m *DetailModel) SetTranscriptEntries(entries []claude.TranscriptEntry) {
	m.transcriptEntries = entries
	m.transcriptExpandedJSON = make(map[int]string)
	// Precompute column widths (minimum = header label width)
	m.transcriptMaxTypeW = len("TYPE")
	m.transcriptMaxCTypeW = len("CONTENT")
	for _, e := range entries {
		if len(e.Type) > m.transcriptMaxTypeW {
			m.transcriptMaxTypeW = len(e.Type)
		}
		if len(e.ContentType) > m.transcriptMaxCTypeW {
			m.transcriptMaxCTypeW = len(e.ContentType)
		}
	}
	// Don't reset cursor/scroll/expanded — preserve navigation state on refresh
}

func (m *DetailModel) SetShowRawTranscript(show bool) {
	m.showRawTranscript = show
	if !show {
		m.transcriptEntries = nil
		m.transcriptCursor = 0
		m.transcriptScroll = 0
		m.transcriptExpanded = nil
		m.transcriptExpandedJSON = nil
	} else {
		m.transcriptExpanded = make(map[int]bool)
		m.transcriptExpandedJSON = make(map[int]string)
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

func firstNRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// trimTrailingBlanks removes trailing lines that are visually empty
// (whitespace-only after stripping ANSI escape sequences).
// This prevents GotoBottom() from scrolling past all content into empty space
// when tmux captures include trailing blank lines for the full pane height.
func trimTrailingBlanks(content string) string {
	lines := strings.Split(content, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	if end == len(lines) {
		return content
	}
	return strings.Join(lines[:end], "\n")
}

// truncateLines clips each line to maxWidth, handling ANSI escape sequences correctly.
func truncateLines(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	style := lipgloss.NewStyle().MaxWidth(maxWidth)
	for i, line := range lines {
		lines[i] = style.Render(line) + "\033[m"
	}
	return strings.Join(lines, "\n")
}

// wrapLines hard-wraps content to maxWidth in a single Hardwrap pass (preserving
// ANSI state continuity). Lines that should not wrap (box-drawing, dividers,
// trailing-padding) are pre-truncated. divMaxWidth controls the width used for
// reconstructing horizontal-rule labels (to keep them visible alongside overlays).
func wrapLines(content string, maxWidth, divMaxWidth int) string {
	if maxWidth <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) <= maxWidth {
			continue // fits — no action needed
		}
		// Strip ANSI once for all checks below
		stripped := ansi.Strip(line)
		switch classifyLine(stripped) {
		case lineHRule:
			trimmed := strings.TrimSpace(stripped)
			lines[i] = rebuildHRuleLine(line, trimmed, stripped, divMaxWidth)
		case lineBox:
			lines[i] = ansi.Truncate(line, maxWidth, "") + "\033[m"
		default:
			if ansi.StringWidth(strings.TrimRight(stripped, " \t")) <= maxWidth {
				lines[i] = ansi.Truncate(line, maxWidth, "") + "\033[m"
			}
		}
	}
	return ansi.Hardwrap(strings.Join(lines, "\n"), maxWidth, false)
}

// isDividerRune reports whether r is a horizontal rule character.
func isDividerRune(r rune) bool {
	switch r {
	case '─', '━', '═', '╌', '┄', '┈', '—':
		return true
	}
	return false
}

// lineClass distinguishes lines that need special handling when wrapping.
type lineClass int

const (
	lineNormal lineClass = iota // wrap normally
	lineHRule                   // horizontal rule — rebuild at target width
	lineBox                     // box-drawing border/side — truncate
)

// classifyLine categorizes a line for wrap handling.
// The input should already be ANSI-stripped.
func classifyLine(stripped string) lineClass {
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return lineNormal
	}
	var first rune
	var last rune
	for _, r := range stripped {
		if first == 0 {
			first = r
		}
		last = r
	}
	// Box border top/bottom: starts with corner
	switch first {
	case '╭', '╰', '┌', '└':
		return lineBox
	}
	// Box sides / right corners: ends with │ or corner
	switch last {
	case '│', '┃', '╮', '╯', '┐', '┘':
		return lineBox
	}
	// Starts AND ends with a horizontal rule char (pure or labelled divider)
	if isDividerRune(first) && isDividerRune(last) {
		return lineHRule
	}
	return lineNormal
}

// skipCSI returns the byte offset past the CSI sequence starting at s[i],
// or i if s[i] does not start a CSI sequence (ESC [ ... final-byte).
func skipCSI(s string, i int) int {
	if i >= len(s) || s[i] != '\033' || i+1 >= len(s) || s[i+1] != '[' {
		return i
	}
	j := i + 2
	for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
		j++
	}
	if j < len(s) {
		j++ // include the final byte
	}
	return j
}

// extractLeadingANSI returns all CSI escape sequences that appear before
// the first printable byte in s, so they can be re-applied to reconstructed text.
func extractLeadingANSI(s string) string {
	i := 0
	for i < len(s) {
		j := skipCSI(s, i)
		if j == i {
			break // not a CSI — first printable byte
		}
		i = j
	}
	return s[:i]
}

// ansiStateAt collects all CSI escape sequences encountered while scanning s
// up to (and at) the n-th visible character. This gives the accumulated ANSI
// state (fg, bg, bold, etc.) that is active at that position.
func ansiStateAt(s string, n int) string {
	var buf strings.Builder
	visible := 0
	i := 0
	for i < len(s) && visible < n {
		if j := skipCSI(s, i); j != i {
			buf.WriteString(s[i:j])
			i = j
		} else {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			visible++
		}
	}
	// Also collect any ANSI right at position n (before the next visible char).
	for i < len(s) {
		if j := skipCSI(s, i); j != i {
			buf.WriteString(s[i:j])
			i = j
		} else {
			break
		}
	}
	return buf.String()
}

// rebuildHRuleLine reconstructs a horizontal rule line (pure or with embedded label)
// at newWidth. It preserves the divider character type, the dash color (via leading
// ANSI prefix), and the label's inherited ANSI state (fg, bg, bold) by scanning the
// original line. The right margin is always exactly 2 dashes.
// original is the raw line (with ANSI); trimmed is ANSI-stripped+TrimSpaced;
// fullStripped is ANSI-stripped WITHOUT TrimSpace (for position alignment).
func rebuildHRuleLine(original, trimmed, fullStripped string, newWidth int) string {
	var divChar rune
	for _, r := range trimmed {
		if isDividerRune(r) {
			divChar = r
			break
		}
	}
	if divChar == 0 {
		return strings.Repeat("─", newWidth)
	}

	prefix := extractLeadingANSI(original)

	plainLabel := strings.TrimSpace(strings.TrimFunc(trimmed, func(r rune) bool { return isDividerRune(r) }))
	if plainLabel == "" {
		return prefix + strings.Repeat(string(divChar), newWidth) + "\033[m"
	}

	// Right margin is always exactly 2 dashes; left side fills the rest.
	const rightMargin = 2
	labelWidth := ansi.StringWidth(plainLabel)
	left := newWidth - labelWidth - 2 - rightMargin // " label " = labelWidth+2
	if left < 1 {
		return prefix + strings.Repeat(string(divChar), newWidth) + "\033[m"
	}

	// Find label start position in the FULL (non-TrimSpaced) stripped string,
	// so it aligns with byte positions in original.
	labelStartPos := 0
	for _, r := range fullStripped {
		if isDividerRune(r) || r == ' ' {
			labelStartPos++
		} else {
			break
		}
	}

	// Collect accumulated ANSI state at the label position (captures inherited bg, fg, etc.).
	labelANSI := ansiStateAt(original, labelStartPos)

	leftDashes := strings.Repeat(string(divChar), left)
	rightDashes := strings.Repeat(string(divChar), rightMargin)

	return prefix + leftDashes + " " + labelANSI + plainLabel + "\033[m" + prefix + " " + rightDashes + "\033[m"
}

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

// Hook filter modes.
const (
	hookFilterAll       = 0
	hookFilterHandled   = 1
	hookFilterUnhandled = 2
	hookFilterCount     = 3 // for modular cycling
)

// hookIsHandled returns true if the hook event had a meaningful effect (not passthrough, not legacy).
func hookIsHandled(ev claude.HookEvent) bool {
	return ev.Effect != "" && ev.Effect != claude.HookEffectNone
}

// hookVisLines returns the number of visible lines for the hook event overlay.
func (m *DetailModel) hookVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// ensureHookCursorVisible adjusts hookScroll so the cursor is in view,
// accounting for expanded entries consuming extra lines.
func (m *DetailModel) ensureHookCursorVisible() {
	avail := m.hookVisLines()
	if avail < 1 {
		return
	}
	if m.hookCursor < m.hookScroll {
		m.hookScroll = m.hookCursor
		return
	}
	usedLines := 0
	for i := m.hookScroll; i <= m.hookCursor && i < len(m.hookFiltered); i++ {
		usedLines++
		if m.hookExpanded[i] {
			usedLines += m.hookExpandedLineCount(i)
		}
	}
	for usedLines > avail && m.hookScroll < m.hookCursor {
		usedLines--
		if m.hookExpanded[m.hookScroll] {
			usedLines -= m.hookExpandedLineCount(m.hookScroll)
		}
		m.hookScroll++
	}
}

// hookExpandedLineCount returns the number of extra lines an expanded hook entry consumes.
func (m *DetailModel) hookExpandedLineCount(idx int) int {
	s := m.getHookExpandedJSON(idx)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// getHookExpandedJSON returns the pretty-printed payload JSON for a hook entry, caching lazily.
func (m *DetailModel) getHookExpandedJSON(idx int) string {
	if idx < 0 || idx >= len(m.hookFiltered) {
		return ""
	}
	if cached, ok := m.hookExpandedJSON[idx]; ok {
		return cached
	}
	raw := m.hookFiltered[idx].Payload
	if raw == "" {
		m.hookExpandedJSON[idx] = ""
		return ""
	}
	var v interface{}
	if json.Unmarshal([]byte(raw), &v) != nil {
		m.hookExpandedJSON[idx] = raw
		return raw
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		m.hookExpandedJSON[idx] = raw
		return raw
	}
	result := string(b)
	m.hookExpandedJSON[idx] = result
	return result
}

func (m *DetailModel) View() string {
	if m.session == nil {
		return EmptyStyle.Width(m.width).Height(m.height).Render("Select a session to preview")
	}

	s := m.session

	avatarColor := AvatarColor(s.AvatarColorIdx)

	// Header line 1: project + diff stats + right-aligned git info
	projectLabel := DetailTitleStyle.Foreground(avatarColor).Render(s.Project + "/")
	gitInfo := ""
	if s.GitBranch != "" {
		gitInfo = DetailMetaStyle.Render(s.GitBranch + " " + IconGitBranch + " " +
			s.TmuxSession + ":" + fmt.Sprintf("%d.%s", s.TmuxWindow, s.PaneID))
	}
	gitInfoWidth := lipgloss.Width(gitInfo)
	projectWidth := lipgloss.Width(projectLabel)

	// Diff stats fill the gap between project and right-aligned git info
	diffStatsStr := ""
	if len(m.diffFiles) > 0 {
		// Available width for diffs: total - project - gitInfo - gaps
		rowWidth := m.width - projectWidth - gitInfoWidth - 6 // 2 gap left + 2 gap right + 2 padding
		if rowWidth < 10 {
			rowWidth = 10
		}

		var entries []string
		used := 0
		for i, fs := range m.diffFiles {
			entry := fs.name + " "
			addStr := fmt.Sprintf("+%d", fs.added)
			rmStr := fmt.Sprintf("-%d", fs.removed)
			plainWidth := lipgloss.Width(entry) + lipgloss.Width(addStr) + 1 + lipgloss.Width(rmStr)
			if used > 0 {
				plainWidth += 3 // separator " │ "
			}
			if used+plainWidth > rowWidth && len(entries) > 0 {
				remaining := len(m.diffFiles) - i
				if remaining > 0 {
					entries = append(entries, ItemDetailStyle.Render(fmt.Sprintf("…+%d", remaining)))
				}
				break
			}
			rendered := ItemDetailStyle.Render(entry) + DiffAddedStyle.Render(addStr) + " " + StatWorkingStyle.Render(rmStr)
			entries = append(entries, rendered)
			used += plainWidth
		}

		if len(entries) > 0 {
			sep := ItemDetailStyle.Render(" │ ")
			diffStatsStr = "  " + strings.Join(entries, sep)
		}
	}

	// Assemble line 1: project + diffs + gap + git info (right-aligned)
	leftPart := projectLabel + diffStatsStr
	leftWidth := lipgloss.Width(leftPart)
	gap := m.width - leftWidth - gitInfoWidth - 2
	if gap < 2 {
		gap = 2
	}
	line1 := leftPart + strings.Repeat(" ", gap) + gitInfo

	// Header line 2: avatar + mnemonic badge + session title
	avatar := AvatarStyle(s.AvatarColorIdx).Render(AvatarGlyph(s.AvatarAnimalIdx))
	badge := AvatarMnemonicBadge(s.AvatarAnimalIdx, s.AvatarColorIdx)
	sessionTitle := avatar + " " + badge
	if name := s.DisplayName(); name != "" {
		sessionTitle += " " + name
	}

	header := line1 + "\n" + sessionTitle + "\n"

	// Content viewport, optionally with transcript side panel
	contentWidth := m.width - 4
	vpRaw := m.viewport.View()
	if m.relayView != "" {
		vpRaw = injectAfterPrompt(vpRaw, m.relayView)
	}
	// Use the session's avatar color for the preview border
	contentStyle := DetailContentStyle.BorderForeground(avatarColor)

	var contentBox string
	showTranscript := m.transcriptMode != transcriptHidden && (len(m.userMessages) > 0 || m.summary != nil)
	showMemo := (m.memo != "" || m.memoEditing) && m.transcriptMode != transcriptHidden
	panelWidth := calcPanelWidth(contentWidth)
	if (showTranscript || showMemo) && m.transcriptMode == transcriptDocked {
		transcriptWidth := panelWidth
		vpWidth := contentWidth - transcriptWidth - 3 // 1 gap + 2 for content border
		vpView := truncateLines(vpRaw, vpWidth)
		vpPanel := lipgloss.NewStyle().Width(vpWidth).MaxWidth(vpWidth).Render(vpView)
		var rightCol string
		switch {
		case showTranscript && showMemo:
			rightCol = lipgloss.JoinVertical(lipgloss.Left, m.renderTranscript(transcriptWidth), m.renderMemoPanel(transcriptWidth))
		case showTranscript:
			rightCol = m.renderTranscript(transcriptWidth)
		default:
			rightCol = m.renderMemoPanel(transcriptWidth)
		}
		joined := lipgloss.JoinHorizontal(lipgloss.Top, vpPanel, " ", rightCol)
		joinedClip := lipgloss.NewStyle().MaxWidth(contentWidth).Render(joined)
		contentBox = contentStyle.Width(contentWidth).Render(joinedClip)
	} else {
		contentBox = contentStyle.Width(contentWidth).Render(vpRaw)
		if showTranscript { // overlay mode
			transcriptPanel := m.renderTranscript(panelWidth)
			col := lipgloss.Width(contentBox) - lipgloss.Width(transcriptPanel) - 1
			contentBox = overlayAt(contentBox, transcriptPanel, col, 1)
			if showMemo {
				memoPanel := m.renderMemoPanel(panelWidth)
				row := 1 + lipgloss.Height(transcriptPanel)
				contentBox = overlayAt(contentBox, memoPanel, col, row)
			}
		} else if showMemo {
			memoPanel := m.renderMemoPanel(panelWidth)
			col := lipgloss.Width(contentBox) - lipgloss.Width(memoPanel) - 1
			contentBox = overlayAt(contentBox, memoPanel, col, 1)
		}
	}

	// Hook events overlay on top of content
	if m.showHooks {
		// Use same dimensions as contentBox — border takes 2 lines
		contentBox = m.renderHookOverlay(contentWidth, m.viewport.Height)
	}

	// Raw transcript JSON overlay on top of content
	if m.showRawTranscript {
		contentBox = m.renderRawTranscriptOverlay(contentWidth, m.viewport.Height)
	}

	// Diff hunks overlay on top of content
	if m.showDiffs {
		contentBox = m.renderDiffOverlay(contentWidth, m.viewport.Height)
	}

	// Footer metadata
	var metaParts []string
	if s.SessionID != "" {
		short := s.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		metaParts = append(metaParts, IconID+" "+short)
	}
	if !s.LastChanged.IsZero() {
		age := FormatAge(s.LastChanged)
		metaParts = append(metaParts, IconClock+" "+age+" ago")
	}
	meta := DetailMetaStyle.Render(strings.Join(metaParts, "  "))

	return lipgloss.JoinVertical(lipgloss.Left, header, contentBox, "", meta)
}

// renderTranscript renders the user messages panel with a border.
func (m DetailModel) renderTranscript(width int) string {
	// Inner width for text (subtract border 2 + padding 2)
	innerWidth := width - 4
	if innerWidth < 5 {
		innerWidth = 5
	}

	var lines []string

	titleLine := TranscriptTitleStyle.Foreground(ColorBorder).Render(" " + IconInput + "  Your Messages")
	lines = append(lines, titleLine)
	lines = append(lines, "") // blank line after title
	for i, msg := range m.userMessages {
		var styledIndicator string
		if i == m.msgCursor {
			styledIndicator = TranscriptCursorStyle.Render("▶ ")
		} else {
			styledIndicator = TranscriptBulletStyle.Render(IconBullet + " ")
		}
		// Allow up to 2 lines per message: innerWidth minus the 2-char indicator column
		msgWidth := innerWidth - 2
		if msgWidth < 1 {
			msgWidth = 1
		}
		flat := strings.ReplaceAll(msg, "\n", " ")
		if ansi.StringWidth(flat) <= msgWidth {
			lines = append(lines, styledIndicator+TranscriptMsgStyle.Render(flat))
		} else {
			// Two-line display: word-wrap at msgWidth, truncate second line
			line1, rest := wordWrapFirst(flat, msgWidth)
			line2 := ansi.Truncate(rest, msgWidth, "…")
			indent := TranscriptBulletStyle.Render("  ")
			lines = append(lines,
				styledIndicator+TranscriptMsgStyle.Render(line1),
				indent+TranscriptMsgStyle.Render(line2),
			)
		}
	}

	content := strings.Join(lines, "\n")
	return TranscriptOverlayStyle.
		Width(width).
		Render(content)
}

// renderMemoPanel renders the session note panel with a border.
// When memoEditing is true, it shows the textarea for inline editing.
func (m *DetailModel) renderMemoPanel(width int) string {
	innerWidth := width - 4
	if innerWidth < 5 {
		innerWidth = 5
	}

	titleLine := TranscriptTitleStyle.Foreground(ColorNote).Render(" " + IconNote + "  Note")

	var body string
	if m.memoEditing {
		m.memoEditor.SetWidth(innerWidth)
		body = m.memoEditor.ViewTextarea()
	} else {
		wrapped := wordWrapContent(m.memo, innerWidth)
		body = TranscriptMsgStyle.Render(wrapped)
	}

	borderColor := ColorBorder
	if m.memoEditing {
		borderColor = ColorNote
	}
	content := titleLine + "\n\n" + body
	return TranscriptOverlayStyle.
		BorderForeground(borderColor).
		Width(width).
		Render(content)
}

// wordWrapContent wraps plain text to fit within maxWidth columns.
func wordWrapContent(s string, maxWidth int) string {
	if maxWidth <= 0 || s == "" {
		return s
	}
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if ansi.StringWidth(line) <= maxWidth {
			result = append(result, line)
			continue
		}
		for len(line) > 0 {
			first, rest := wordWrapFirst(line, maxWidth)
			result = append(result, first)
			if rest == line {
				break // wordWrapFirst made no progress (char wider than maxWidth)
			}
			line = rest
		}
	}
	return strings.Join(result, "\n")
}

func (m DetailModel) renderHookOverlay(width, height int) string {
	// Title with filter indicator
	filterLabel := ""
	switch m.hookFilter {
	case 1:
		filterLabel = "  " + DiffAddedStyle.Render("[handled]")
	case 2:
		filterLabel = "  " + DetailMetaStyle.Render("[unhandled]")
	}
	titleLine := DebugTitleStyle.Render(" Hook Events") + filterLabel

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	total := len(m.hookFiltered)
	if total == 0 {
		lines = append(lines, DetailMetaStyle.Render("No hook events recorded"))
	} else {
		visLines := m.hookVisLines()
		innerWidth := width - 6 // border(2) + padding(2) + cursor(2)
		clipStyle := lipgloss.NewStyle().MaxWidth(innerWidth)

		rendered := 0
		for i := m.hookScroll; i < total && rendered < visLines; i++ {
			ev := m.hookFiltered[i]

			cursorMark := "  "
			if i == m.hookCursor {
				cursorMark = "> "
			}
			timestamp := DetailMetaStyle.Render(ev.Time)
			hookType := hookTypeStyled(ev.HookType)

			// Effect annotation
			var effectStr string
			switch {
			case hookIsHandled(ev):
				effectText := ev.Effect
				effectSuffix := ""
				if strings.HasSuffix(effectText, claude.HookEffectDedupSuffix) {
					effectText = strings.TrimSuffix(effectText, claude.HookEffectDedupSuffix)
					effectSuffix = ItemDetailStyle.Render(claude.HookEffectDedupSuffix)
				}
				effectStr = "  " + ItemDetailStyle.Render(" → ") + DiffAddedStyle.Render(effectText) + effectSuffix
			case ev.Effect == "-":
				effectStr = "  " + ItemDetailStyle.Render("(passthrough)")
			default:
				effectStr = "  " + ItemDetailStyle.Render("(no data)")
			}

			line := fmt.Sprintf("%s%s  %s%s", cursorMark, timestamp, hookType, effectStr)
			lines = append(lines, clipStyle.Render(line))
			rendered++

			// Expanded JSON below summary (inline, scrolls with the list)
			if m.hookExpanded[i] {
				expanded := m.getHookExpandedJSON(i)
				for _, jsonLine := range strings.Split(expanded, "\n") {
					if rendered >= visLines {
						break
					}
					highlighted := HighlightJSON(jsonLine)
					lines = append(lines, clipStyle.Render("  │ "+highlighted))
					rendered++
				}
			}
		}

		// Scroll indicator
		if total > 1 {
			indicator := DetailMetaStyle.Render(fmt.Sprintf("── %d/%d events ──", min(m.hookCursor+1, total), total))
			lines = append(lines, indicator)
		}
	}

	content := strings.Join(lines, "\n")
	return DebugOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
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

func (m DetailModel) renderRawTranscriptOverlay(width, height int) string {
	total := len(m.transcriptEntries)
	titleLine := TranscriptTitleStyle.Render(fmt.Sprintf(" Transcript (%d entries)", total))

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	if total == 0 {
		lines = append(lines, DetailMetaStyle.Render("No transcript data"))
	} else {
		visLines := m.transcriptVisLines() - 1 // -1 for sticky header
		if visLines < 1 {
			visLines = 1
		}
		innerWidth := width - 6 // border(2) + padding(2) + cursor(2)
		headerStyle := lipgloss.NewStyle().Foreground(ColorMuted).Bold(true)
		clipStyle := lipgloss.NewStyle().MaxWidth(innerWidth)

		// Use cached column widths (computed in SetTranscriptEntries)
		maxTypeW := m.transcriptMaxTypeW
		maxContentTypeW := m.transcriptMaxCTypeW
		tsW := 8 // HH:MM:SS

		// Sticky header
		header := "  " +
			headerStyle.Render(fmt.Sprintf("%-*s", tsW, "TIME")) + "  " +
			headerStyle.Render(fmt.Sprintf("%-*s", maxTypeW, "TYPE")) + "  " +
			headerStyle.Render(fmt.Sprintf("%-*s", maxContentTypeW, "CONTENT")) + "  " +
			headerStyle.Render("SUMMARY")
		lines = append(lines, clipStyle.Render(header))

		rendered := 0
		for i := m.transcriptScroll; i < total && rendered < visLines; i++ {
			entry := m.transcriptEntries[i]

			// Cursor mark
			cursorMark := "  "
			if i == m.transcriptCursor {
				cursorMark = "> "
			}

			// Col 1: Timestamp (fixed 8 chars)
			ts := entry.Timestamp
			if ts == "" {
				ts = "        "
			}

			// Col 2: ContentType (padded to maxContentTypeW)
			ct := entry.ContentType
			ctPadded := ct + strings.Repeat(" ", maxContentTypeW-len(ct))

			// Col 3: Summary
			var summaryStr string
			if entry.Summary != "" {
				summaryStr = "  " + styleEntrySummary(entry)
			}

			line := cursorMark +
				ItemDetailStyle.Render(ts) + "  " +
				styleEntryType(entry.Type, maxTypeW) + "  " +
				ItemDetailStyle.Render(ctPadded) +
				summaryStr
			lines = append(lines, clipStyle.Render(line))
			rendered++

			// Expanded JSON below summary
			if m.transcriptExpanded[i] {
				expanded := m.getExpandedJSON(i)
				for _, jsonLine := range strings.Split(expanded, "\n") {
					if rendered >= visLines {
						break
					}
					highlighted := HighlightJSON(jsonLine)
					lines = append(lines, clipStyle.Render("  │ "+highlighted))
					rendered++
				}
			}
		}

		// Scroll indicator
		if total > 1 {
			indicator := DetailMetaStyle.Render(fmt.Sprintf("── %d/%d entries ──", min(m.transcriptCursor+1, total), total))
			lines = append(lines, indicator)
		}
	}

	content := strings.Join(lines, "\n")
	return TranscriptOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
}

// styleEntryType renders the type label with type-appropriate coloring, padded to minWidth.
func styleEntryType(typ string, minWidth int) string {
	padded := typ + strings.Repeat(" ", max(0, minWidth-len(typ)))
	switch typ {
	case "user":
		return DiffAddedStyle.Render(padded)
	case "assistant":
		return StatPostToolStyle.Render(padded)
	case "system":
		return StatWorkingStyle.Render(padded)
	default:
		return ItemDetailStyle.Render(padded)
	}
}

// styleEntrySummary renders the summary text with muted styling.
func styleEntrySummary(entry claude.TranscriptEntry) string {
	return ItemDetailStyle.Render(entry.Summary)
}

// HighlightJSON applies simple syntax highlighting to a JSON line.
func HighlightJSON(line string) string {
	var result strings.Builder
	i := 0
	runes := []rune(line)
	n := len(runes)

	for i < n {
		ch := runes[i]
		switch {
		case ch == '"':
			// Find end of string
			end := i + 1
			for end < n && runes[end] != '"' {
				if runes[end] == '\\' {
					end++ // skip escaped char
				}
				end++
			}
			if end < n {
				end++ // include closing quote
			}
			str := string(runes[i:end])
			// Check if this is a key (followed by ':')
			afterStr := end
			for afterStr < n && runes[afterStr] == ' ' {
				afterStr++
			}
			if afterStr < n && runes[afterStr] == ':' {
				result.WriteString(TitleStyle.Render(str))
			} else {
				result.WriteString(DiffAddedStyle.Render(str))
			}
			i = end
		case ch >= '0' && ch <= '9', ch == '-':
			// Number
			end := i + 1
			for end < n && (runes[end] >= '0' && runes[end] <= '9' || runes[end] == '.' || runes[end] == 'e' || runes[end] == 'E' || runes[end] == '+' || runes[end] == '-') {
				end++
			}
			result.WriteString(StatWorkingStyle.Render(string(runes[i:end])))
			i = end
		case ch == 't' || ch == 'f' || ch == 'n':
			// true, false, null
			word := ""
			if i+4 <= n && string(runes[i:i+4]) == "true" {
				word = "true"
			} else if i+5 <= n && string(runes[i:i+5]) == "false" {
				word = "false"
			} else if i+4 <= n && string(runes[i:i+4]) == "null" {
				word = "null"
			}
			if word != "" {
				result.WriteString(StatWorkingStyle.Render(word))
				i += len(word)
			} else {
				result.WriteRune(ch)
				i++
			}
		case ch == '{' || ch == '}' || ch == '[' || ch == ']' || ch == ':' || ch == ',':
			result.WriteString(DetailMetaStyle.Render(string(ch)))
			i++
		default:
			result.WriteRune(ch)
			i++
		}
	}
	return result.String()
}

// injectAfterPrompt finds the last line containing ❯ in the viewport output
// and replaces the line immediately after it with the relay input view.
func injectAfterPrompt(vpView, relayView string) string {
	lines := strings.Split(vpView, "\n")
	promptIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(ansi.Strip(lines[i]), "❯") {
			promptIdx = i
			break
		}
	}
	if promptIdx < 0 {
		return vpView
	}
	lines[promptIdx] = relayView
	return strings.Join(lines, "\n")
}

func hookTypeStyled(hookType string) string {
	switch hookType {
	case "PreToolUse":
		return StatWorkingStyle.Render(hookType)
	case "PostToolUse":
		return StatPostToolStyle.Render(hookType)
	case "UserPromptSubmit":
		return DiffAddedStyle.Render(hookType)
	case "Stop":
		return StatDoneStyle.Render(hookType)
	case "Notification":
		return StatWaitingStyle.Render(hookType)
	case "SessionStart":
		return DiffAddedStyle.Render(hookType)
	case "SessionEnd":
		return StatDoneStyle.Render(hookType)
	case "PreCompact":
		return StatLaterStyle.Render(hookType)
	default:
		return DetailMetaStyle.Render(hookType)
	}
}

// runeWidth returns the display width of a rune without allocating a string.
// CJK wide characters are 2 cells; everything else is 1.
func runeWidth(r rune) int {
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hiragana, r) {
		return 2
	}
	return 1
}

// wordWrapFirst splits s into a first line that fits within width (breaking at
// word boundaries) and the remaining text. If no word boundary is found within
// width, it falls back to a hard truncation.
func wordWrapFirst(s string, width int) (string, string) {
	if ansi.StringWidth(s) <= width {
		return s, ""
	}
	// Walk runes, track last space position
	w := 0
	lastSpace := -1
	lastSpaceByte := 0
	byteOff := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > width {
			break
		}
		if r == ' ' {
			lastSpace = w
			lastSpaceByte = byteOff
		}
		w += rw
		byteOff += utf8.RuneLen(r)
	}
	if lastSpace > 0 {
		return s[:lastSpaceByte], strings.TrimSpace(s[lastSpaceByte:])
	}
	// No space found — hard break
	return ansi.Truncate(s, width, ""), strings.TrimSpace(s[byteOff:])
}
