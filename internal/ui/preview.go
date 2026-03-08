package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	hookCursor     int
	hookExpanded   bool
	hookScroll     int
	rawTranscript       string // full pretty-printed JSON
	rawTranscriptLines  []string // pre-split lines for rendering
	showRawTranscript   bool
	rawTranscriptScroll int
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
			m.viewport.SetContent(truncateLines(m.content, m.viewport.Width))
			m.viewport.GotoBottom() // content arrived before size was known
		}
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = contentHeight
		if m.content != "" {
			m.viewport.SetContent(truncateLines(m.content, m.viewport.Width))
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
		m.viewport.SetContent(truncateLines(content, m.viewport.Width))
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
	m.hookExpanded = false
	if !show {
		m.hookEvents = nil
	}
}

func (m *PreviewModel) ToggleExpand() {
	if !m.showHooks {
		return
	}
	m.hookExpanded = !m.hookExpanded
}

func (m *PreviewModel) SetRelayView(v string) {
	m.relayView = v
}

func (m *PreviewModel) SetHookEvents(events []claude.HookEvent) {
	m.hookEvents = events
}

func (m *PreviewModel) SetRawTranscript(s string) {
	m.rawTranscript = s
	if s == "" {
		m.rawTranscriptLines = nil
	} else {
		m.rawTranscriptLines = strings.Split(s, "\n")
	}
}

func (m *PreviewModel) SetShowRawTranscript(show bool) {
	m.showRawTranscript = show
	m.rawTranscriptScroll = 0
	if !show {
		m.rawTranscript = ""
		m.rawTranscriptLines = nil
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

func (m *PreviewModel) ScrollDown() {
	if m.showRawTranscript {
		visLines := m.rawTranscriptVisLines()
		maxScroll := len(m.rawTranscriptLines) - visLines
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.rawTranscriptScroll = min(m.rawTranscriptScroll+3, maxScroll)
		return
	}
	if m.showHooks {
		total := len(m.hookEvents)
		if m.hookCursor < total-1 {
			m.hookCursor++
		}
		visLines := m.hookListLines()
		if m.hookCursor >= m.hookScroll+visLines {
			m.hookScroll = m.hookCursor - visLines + 1
		}
		return
	}
	m.viewport.LineDown(3)
}

func (m *PreviewModel) ScrollUp() {
	if m.showRawTranscript {
		m.rawTranscriptScroll = max(m.rawTranscriptScroll-3, 0)
		return
	}
	if m.showHooks {
		if m.hookCursor > 0 {
			m.hookCursor--
		}
		if m.hookCursor < m.hookScroll {
			m.hookScroll = m.hookCursor
		}
		return
	}
	m.viewport.LineUp(3)
}

// hookListLines returns the number of rows available for the event list inside the overlay.
func (m *PreviewModel) hookListLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	if m.hookExpanded {
		return avail / 2
	}
	return avail
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
		indicator := "  "
		if i == m.msgCursor {
			indicator = "▶ "
		}
		// Truncate to single line: innerWidth minus the 2-char indicator column
		msgWidth := innerWidth - 2
		if msgWidth < 1 {
			msgWidth = 1
		}
		truncated := ansi.Truncate(strings.ReplaceAll(msg, "\n", " "), msgWidth, "…")
		entry := PreviewMetaStyle.Render(indicator) + TranscriptMsgStyle.Render(truncated)
		lines = append(lines, entry)
	}

	content := strings.Join(lines, "\n")
	return TranscriptOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
}

func (m PreviewModel) renderHookOverlay(width, height int) string {
	titleLine := DebugTitleStyle.Render(" Hook Events")

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	if len(m.hookEvents) == 0 {
		lines = append(lines, PreviewMetaStyle.Render("No hook events recorded"))
	} else {
		// Reverse events so newest is on top
		total := len(m.hookEvents)
		reversed := make([]claude.HookEvent, total)
		for i, ev := range m.hookEvents {
			reversed[total-1-i] = ev
		}

		listLines := m.hookListLines()

		// Clamp cursor and scroll
		cursor := m.hookCursor
		if cursor >= total {
			cursor = total - 1
		}
		scroll := m.hookScroll
		if scroll > total {
			scroll = total
		}
		end := scroll + listLines
		if end > total {
			end = total
		}

		for i, ev := range reversed[scroll:end] {
			evIdx := scroll + i
			cursorMark := "  "
			if evIdx == cursor {
				cursorMark = "> "
			}
			timestamp := PreviewMetaStyle.Render(ev.Time)
			hookType := hookTypeStyled(ev.HookType)
			lines = append(lines, fmt.Sprintf("%s%s  %s", cursorMark, timestamp, hookType))
		}

		// Payload section when expanded
		if m.hookExpanded && cursor < total {
			payload := reversed[cursor].Payload
			avail := height - 4                   // border(2) + title(1) + blank(1)
			payloadLines := avail - listLines - 1 // 1 for separator
			if payloadLines < 1 {
				payloadLines = 1
			}
			innerWidth := width - 6 // border(2) + padding(2) + cursor(2)

			sep := PreviewMetaStyle.Render(strings.Repeat("─", innerWidth))
			lines = append(lines, sep)

			formatted := formatJSON(payload)
			shown := 0
			clipStyle := lipgloss.NewStyle().MaxWidth(innerWidth)
			for _, pl := range strings.Split(formatted, "\n") {
				if shown >= payloadLines {
					break
				}
				lines = append(lines, clipStyle.Render(pl))
				shown++
			}
		}
	}

	content := strings.Join(lines, "\n")
	return DebugOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
}

// rawTranscriptVisLines returns the number of visible lines for the raw transcript overlay.
func (m *PreviewModel) rawTranscriptVisLines() int {
	avail := m.viewport.Height - 4 // border(2) + title(1) + blank(1)
	if avail < 1 {
		avail = 1
	}
	return avail
}

func (m PreviewModel) renderRawTranscriptOverlay(width, height int) string {
	titleLine := TranscriptTitleStyle.Render(" Transcript JSON")

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, "")

	if len(m.rawTranscriptLines) == 0 {
		lines = append(lines, PreviewMetaStyle.Render("No transcript data"))
	} else {
		visLines := m.rawTranscriptVisLines()
		innerWidth := width - 6 // border(2) + padding(2) + gutter(2)
		lineNumWidth := len(fmt.Sprintf("%d", len(m.rawTranscriptLines)))
		gutterStyle := PreviewMetaStyle
		clipStyle := lipgloss.NewStyle().MaxWidth(innerWidth)

		scroll := m.rawTranscriptScroll
		end := scroll + visLines
		if end > len(m.rawTranscriptLines) {
			end = len(m.rawTranscriptLines)
		}

		for i := scroll; i < end; i++ {
			lineNum := gutterStyle.Render(fmt.Sprintf("%*d ", lineNumWidth, i+1))
			line := clipStyle.Render(m.rawTranscriptLines[i])
			lines = append(lines, lineNum+line)
		}

		// Scroll indicator
		total := len(m.rawTranscriptLines)
		if total > visLines {
			pct := (scroll * 100) / (total - visLines)
			indicator := PreviewMetaStyle.Render(fmt.Sprintf("── %d/%d lines (%d%%) ──", min(end, total), total, pct))
			lines = append(lines, indicator)
		}
	}

	content := strings.Join(lines, "\n")
	return TranscriptOverlayStyle.
		Width(width).
		Height(height).
		Render(content)
}

func formatJSON(raw string) string {
	if raw == "" {
		return "(no payload)"
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return raw
	}
	return "  " + string(b)
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
	case "Stop":
		return StatDoneStyle.Render(hookType)
	default:
		return PreviewMetaStyle.Render(hookType)
	}
}
