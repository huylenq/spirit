package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// diffFileStat is a pre-sorted, per-file diff entry cached on SetDiffStats.
type diffFileStat struct {
	name      string
	added     int
	removed   int
	footprint int
}

type PreviewModel struct {
	viewport        viewport.Model
	session         *claude.ClaudeSession
	content         string
	userMessages    []string
	msgOffsets      []int // line index in content for each userMessage; -1 if not found
	msgCursor       int   // which user message we last navigated to
	pendingMsgReset bool  // set on session switch; reset msgCursor when messages arrive
	diffStats       map[string]claude.FileDiffStat
	diffFiles       []diffFileStat // cached sorted file entries
	summary         *claude.SessionSummary
	relayView       string // when set, rendered inline after the ❯ prompt line
	hookEvents      []claude.HookEvent
	showHooks       bool
	hideTranscript  bool
	hookCursor       int
	hookExpanded     map[int]bool   // per-entry expansion (keyed by filtered index)
	hookExpandedJSON map[int]string // lazy pretty-print cache
	hookScroll       int
	hookFilter       int // 0=all, 1=handled only, 2=unhandled only
	hookFiltered     []claude.HookEvent // cached filtered+reversed slice
	transcriptEntries      []claude.TranscriptEntry
	transcriptCursor       int            // selected entry index
	transcriptScroll       int            // first visible entry index
	transcriptExpanded     map[int]bool   // which entries are expanded
	transcriptExpandedJSON map[int]string // lazy pretty-print cache
	transcriptMaxTypeW     int            // cached max width of Type column
	transcriptMaxCTypeW    int            // cached max width of ContentType column
	showRawTranscript      bool
	showDiffs       bool
	diffHunks       []claude.FileDiffHunk
	diffHunkFiles   []diffHunkFile
	diffFileCursor  int
	diffExpanded    map[int]bool
	diffScroll      int
	width           int
	height          int
	ready           bool
}

func NewPreviewModel() PreviewModel {
	return PreviewModel{}
}

func (m *PreviewModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	headerHeight := 3 // title + branch/diff + blank line
	footerHeight := 2 // metadata line + blank
	borderHeight := 2 // top + bottom border of content box
	contentHeight := h - headerHeight - footerHeight - borderHeight
	if contentHeight < 1 {
		contentHeight = 1
	}
	vpWidth := w - 6 // account for content box border (2) + padding from outer layout (4 total)
	if vpWidth < 1 {
		vpWidth = 1
	}
	if !m.ready {
		m.viewport = viewport.New(vpWidth, contentHeight)
		m.ready = true
		if m.content != "" {
			m.viewport.SetContent(truncateLines(trimTrailingBlanks(m.content), m.viewport.Width))
			m.viewport.GotoBottom() // content arrived before size was known
		}
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = contentHeight
		if m.content != "" {
			m.viewport.SetContent(truncateLines(trimTrailingBlanks(m.content), m.viewport.Width))
		}
	}
}

// ClearSession resets the preview to the empty "Select a session" state.
func (m *PreviewModel) ClearSession() {
	m.session = nil
	m.content = ""
	m.userMessages = nil
	m.diffFiles = nil
	m.summary = nil
}

// SetNonClaudePane shows a raw terminal capture for a non-Claude pane.
func (m *PreviewModel) SetNonClaudePane(paneID string, paneTitle string, content string) {
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
		m.viewport.SetContent(truncateLines(trimTrailingBlanks(content), m.viewport.Width))
		if isNew {
			m.viewport.GotoBottom()
		}
	}
}

func (m *PreviewModel) SetSession(s *claude.ClaudeSession, content string) {
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
		m.viewport.SetContent(truncateLines(trimTrailingBlanks(content), m.viewport.Width))
		if isNewSession {
			m.viewport.GotoBottom()
		}
	}
}

func (m *PreviewModel) SetUserMessages(msgs []string) {
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

func (m *PreviewModel) SetDiffStats(stats map[string]claude.FileDiffStat) {
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

func (m *PreviewModel) SetSummary(s *claude.SessionSummary) {
	m.summary = s
}

func (m *PreviewModel) SetHideTranscript(hide bool) {
	m.hideTranscript = hide
}

func (m *PreviewModel) SetShowHooks(show bool) {
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
func (m *PreviewModel) CycleHookFilter() {
	m.hookFilter = (m.hookFilter + 1) % 3
	m.hookCursor = 0
	m.hookScroll = 0
	m.hookExpanded = make(map[int]bool)
	m.hookExpandedJSON = make(map[int]string)
	m.rebuildHookFiltered()
}

// rebuildHookFiltered rebuilds the cached filtered+reversed hook event list.
func (m *PreviewModel) rebuildHookFiltered() {
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

func (m *PreviewModel) ToggleExpand() {
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

func (m *PreviewModel) SetRelayView(v string) {
	m.relayView = v
}

func (m *PreviewModel) SetHookEvents(events []claude.HookEvent) {
	m.hookEvents = events
	m.hookExpanded = make(map[int]bool)       // reset — filtered indices shift
	m.hookExpandedJSON = make(map[int]string) // invalidate cache
	m.rebuildHookFiltered()
}

func (m *PreviewModel) SetTranscriptEntries(entries []claude.TranscriptEntry) {
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

func (m *PreviewModel) SetShowRawTranscript(show bool) {
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
func (m *PreviewModel) recomputeOffsets() {
	m.msgOffsets = findMsgLineOffsets(m.content, m.userMessages)
}

// NavigateMsg moves the message cursor by delta (+1 = next, -1 = prev) and scrolls
// the viewport to that message's line in the pane capture.
func (m *PreviewModel) NavigateMsg(delta int) {
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
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func (m *PreviewModel) scrollDown(n int) {
	if m.showDiffs {
		total := len(m.diffHunkFiles)
		for range n {
			if m.diffFileCursor < total-1 {
				m.diffFileCursor++
			}
		}
		visLines := m.diffVisLines()
		if m.diffFileCursor >= m.diffScroll+visLines {
			m.diffScroll = m.diffFileCursor - visLines + 1
		}
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

func (m *PreviewModel) scrollUp(n int) {
	if m.showDiffs {
		for range n {
			if m.diffFileCursor > 0 {
				m.diffFileCursor--
			}
		}
		if m.diffFileCursor < m.diffScroll {
			m.diffScroll = m.diffFileCursor
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
func (m *PreviewModel) ensureTranscriptCursorVisible() {
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

func (m *PreviewModel) halfPage() int {
	h := m.viewport.Height / 2
	if h < 1 {
		h = 1
	}
	return h
}

func (m *PreviewModel) fullPage() int {
	h := m.viewport.Height - 3
	if h < 1 {
		h = 1
	}
	return h
}

// ScrollDown scrolls half a page down (ctrl+d).
func (m *PreviewModel) ScrollDown() { m.scrollDown(m.halfPage()) }

// ScrollUp scrolls half a page up (ctrl+u).
func (m *PreviewModel) ScrollUp() { m.scrollUp(m.halfPage()) }

// ScrollPageDown scrolls a full page down (ctrl+f).
func (m *PreviewModel) ScrollPageDown() { m.scrollDown(m.fullPage()) }

// ScrollPageUp scrolls a full page up (ctrl+b).
func (m *PreviewModel) ScrollPageUp() { m.scrollUp(m.fullPage()) }

// ScrollLines scrolls the preview by n lines (positive = down, negative = up).
func (m *PreviewModel) ScrollLines(n int) {
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
func (m *PreviewModel) hookVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// ensureHookCursorVisible adjusts hookScroll so the cursor is in view,
// accounting for expanded entries consuming extra lines.
func (m *PreviewModel) ensureHookCursorVisible() {
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
func (m *PreviewModel) hookExpandedLineCount(idx int) int {
	s := m.getHookExpandedJSON(idx)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// getHookExpandedJSON returns the pretty-printed payload JSON for a hook entry, caching lazily.
func (m *PreviewModel) getHookExpandedJSON(idx int) string {
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

func (m PreviewModel) View() string {
	if m.session == nil {
		return EmptyStyle.Width(m.width).Height(m.height).Render("Select a session to preview")
	}

	s := m.session

	// Header: project name + branch + diff stats
	title := PreviewTitleStyle.Render(s.Project + "/")
	branch := ""
	if s.GitBranch != "" {
		branch = PreviewMetaStyle.Render(s.GitBranch + " " + IconGitBranch + " " +
			s.TmuxSession + ":" + fmt.Sprintf("%d.%s", s.TmuxWindow, s.PaneID))
	}

	// Per-file diff stats across two rows (title line + branch line), sorted by footprint
	titleSuffix := ""
	branchSuffix := ""
	if len(m.diffFiles) > 0 {
		// Stats column aligned to the right of whichever label (title/branch) is wider
		titleWidth := lipgloss.Width(title)
		branchWidth := lipgloss.Width(branch)
		statsCol := max(titleWidth, branchWidth) + 2
		rowWidth := m.width - statsCol - 2

		if rowWidth < 10 {
			rowWidth = 10
		}

		// Render file entries into up to 2 rows
		var rows [2][]string
		var rowUsed [2]int
		row := 0
		for i, fs := range m.diffFiles {
			entry := fs.name + " "
			addStr := fmt.Sprintf("+%d", fs.added)
			rmStr := fmt.Sprintf("-%d", fs.removed)
			plainWidth := lipgloss.Width(entry) + lipgloss.Width(addStr) + 1 + lipgloss.Width(rmStr)
			if rowUsed[row] > 0 {
				plainWidth += 3 // separator " │ "
			}
			if rowUsed[row]+plainWidth > rowWidth && len(rows[row]) > 0 {
				if row == 0 {
					row = 1
					// Recalc width for fresh row
					plainWidth = lipgloss.Width(entry) + lipgloss.Width(addStr) + 1 + lipgloss.Width(rmStr)
				} else {
					remaining := len(m.diffFiles) - i
					if remaining > 0 {
						rows[row] = append(rows[row], ItemDetailStyle.Render(fmt.Sprintf("…+%d", remaining)))
					}
					break
				}
			}
			rendered := ItemDetailStyle.Render(entry) + DiffAddedStyle.Render(addStr) + " " + StatWorkingStyle.Render(rmStr)
			rows[row] = append(rows[row], rendered)
			rowUsed[row] += plainWidth
		}

		sep := ItemDetailStyle.Render(" │ ")
		if len(rows[1]) > 0 {
			// Two rows: first on title line, second on branch line
			pad := statsCol - titleWidth
			if pad < 2 {
				pad = 2
			}
			titleSuffix = strings.Repeat(" ", pad) + strings.Join(rows[0], sep)
			pad = statsCol - branchWidth
			if pad < 2 {
				pad = 2
			}
			branchSuffix = strings.Repeat(" ", pad) + strings.Join(rows[1], sep)
		} else if len(rows[0]) > 0 {
			// Single row: only on branch line
			pad := statsCol - branchWidth
			if pad < 2 {
				pad = 2
			}
			branchSuffix = strings.Repeat(" ", pad) + strings.Join(rows[0], sep)
		}
	}

	header := title + titleSuffix + "\n" + branch + branchSuffix + "\n"

	// Content viewport, optionally with transcript side panel
	contentWidth := m.width - 4
	vpRaw := m.viewport.View()
	if m.relayView != "" {
		vpRaw = injectAfterPrompt(vpRaw, m.relayView)
	}
	var contentBox string
	if !m.hideTranscript && (len(m.userMessages) > 0 || m.summary != nil) {
		transcriptWidth := contentWidth * 40 / 100
		if transcriptWidth < 20 {
			transcriptWidth = 20
		}
		if transcriptWidth > 50 {
			transcriptWidth = 50
		}
		vpWidth := contentWidth - transcriptWidth - 3 // 1 gap + 2 for content border
		vpView := truncateLines(vpRaw, vpWidth)
		vpPanel := lipgloss.NewStyle().Width(vpWidth).MaxWidth(vpWidth).Render(vpView)
		transcriptPanel := m.renderTranscript(transcriptWidth, m.viewport.Height-2)
		joined := lipgloss.JoinHorizontal(lipgloss.Top, vpPanel, " ", transcriptPanel)
		// Hard clip the entire joined output so nothing overflows the content border
		joinedClip := lipgloss.NewStyle().MaxWidth(contentWidth).Render(joined)
		contentBox = PreviewContentStyle.Width(contentWidth).Render(joinedClip)
	} else {
		contentBox = PreviewContentStyle.Width(contentWidth).Render(vpRaw)
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
		age := formatAge(s.LastChanged)
		metaParts = append(metaParts, IconClock+" "+age+" ago")
	}
	meta := PreviewMetaStyle.Render(strings.Join(metaParts, "  "))

	return lipgloss.JoinVertical(lipgloss.Left, header, contentBox, "", meta)
}

// renderTranscript renders the user messages panel with a border.
func (m PreviewModel) renderTranscript(width, height int) string {
	// Inner width for text (subtract border 2 + padding 2)
	innerWidth := width - 4
	if innerWidth < 5 {
		innerWidth = 5
	}

	var lines []string

	titleLine := TranscriptTitleStyle.Render(" " + IconInput + "  Your Messages")
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
		Height(height).
		Render(content)
}

func (m PreviewModel) renderHookOverlay(width, height int) string {
	// Title with filter indicator
	filterLabel := ""
	switch m.hookFilter {
	case 1:
		filterLabel = "  " + DiffAddedStyle.Render("[handled]")
	case 2:
		filterLabel = "  " + PreviewMetaStyle.Render("[unhandled]")
	}
	titleLine := DebugTitleStyle.Render(" Hook Events") + filterLabel

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	total := len(m.hookFiltered)
	if total == 0 {
		lines = append(lines, PreviewMetaStyle.Render("No hook events recorded"))
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
			timestamp := PreviewMetaStyle.Render(ev.Time)
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
					highlighted := highlightJSON(jsonLine)
					lines = append(lines, clipStyle.Render("  │ "+highlighted))
					rendered++
				}
			}
		}

		// Scroll indicator
		if total > 1 {
			indicator := PreviewMetaStyle.Render(fmt.Sprintf("── %d/%d events ──", min(m.hookCursor+1, total), total))
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
func (m *PreviewModel) transcriptVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

// expandedLineCount returns the number of extra lines an expanded entry consumes.
func (m *PreviewModel) expandedLineCount(idx int) int {
	json := m.getExpandedJSON(idx)
	if json == "" {
		return 0
	}
	return strings.Count(json, "\n") + 1
}

// getExpandedJSON returns the pretty-printed JSON for an entry, caching lazily.
func (m *PreviewModel) getExpandedJSON(idx int) string {
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

func (m PreviewModel) renderRawTranscriptOverlay(width, height int) string {
	total := len(m.transcriptEntries)
	titleLine := TranscriptTitleStyle.Render(fmt.Sprintf(" Transcript (%d entries)", total))

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	if total == 0 {
		lines = append(lines, PreviewMetaStyle.Render("No transcript data"))
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
					highlighted := highlightJSON(jsonLine)
					lines = append(lines, clipStyle.Render("  │ "+highlighted))
					rendered++
				}
			}
		}

		// Scroll indicator
		if total > 1 {
			indicator := PreviewMetaStyle.Render(fmt.Sprintf("── %d/%d entries ──", min(m.transcriptCursor+1, total), total))
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

// highlightJSON applies simple syntax highlighting to a JSON line.
func highlightJSON(line string) string {
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
			result.WriteString(PreviewMetaStyle.Render(string(ch)))
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
		return PreviewMetaStyle.Render(hookType)
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
