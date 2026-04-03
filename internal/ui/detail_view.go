package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

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
		// Strip newlines — FirstMessage can be multiline
		name = strings.ReplaceAll(name, "\n", " ")
		prefixWidth := lipgloss.Width(sessionTitle) + 1 // +1 for the separating space we'll prepend
		maxNameWidth := m.width - prefixWidth - 2       // -2 matches line-1 right-margin convention
		if maxNameWidth > 0 {
			name = ansi.Truncate(name, maxNameWidth, "…")
		}
		sessionTitle += " " + name
	}

	header := line1 + "\n" + sessionTitle

	// Content viewport, optionally with aside panel (chat outline + notes)
	contentWidth := m.width - 4
	vpRaw := m.viewport.View()
	if m.relayView != "" {
		vpRaw = injectAfterPrompt(vpRaw, m.relayView)
	}
	// Use the session's avatar color for the preview border
	contentStyle := DetailContentStyle.BorderForeground(avatarColor)

	var contentBox string
	showChatOutline := m.chatOutlineMode != chatOutlineHidden && (len(m.userMessages) > 0 || m.summary != nil)
	showNote := (m.note != "" || m.noteEditing) && m.chatOutlineMode != chatOutlineHidden
	panelWidth := m.effectivePanelWidth(contentWidth)
	if (showChatOutline || showNote) && m.isChatOutlineDocked() {
		chatOutlineWidth := panelWidth
		vpWidth := contentWidth - chatOutlineWidth - 3 // 1 gap + 2 for content border
		vpView := truncateLines(vpRaw, vpWidth)
		vpPanel := lipgloss.NewStyle().Width(vpWidth).MaxWidth(vpWidth).Render(vpView)
		var aside string
		noteVertStart := -1 // row in aside where note begins (-1 = no highlight)
		switch {
		case showChatOutline && showNote:
			outline := m.renderChatOutline(chatOutlineWidth)
			note := m.renderNotePanel(chatOutlineWidth)
			if m.chatOutlineMode == chatOutlineDockedLeft {
				sepStyle := BorderCharStyle
				if m.noteEditing {
					sepStyle = NoteCharStyle
					noteVertStart = lipgloss.Height(outline) + 1 // after outline + h-separator
				}
				sep := sepStyle.Render(strings.Repeat("─", chatOutlineWidth))
				aside = lipgloss.JoinVertical(lipgloss.Left, outline, sep, note)
			} else {
				aside = lipgloss.JoinVertical(lipgloss.Left, outline, note)
			}
		case showChatOutline:
			aside = m.renderChatOutline(chatOutlineWidth)
		default:
			aside = m.renderNotePanel(chatOutlineWidth)
			if m.noteEditing && m.chatOutlineMode == chatOutlineDockedLeft {
				noteVertStart = 0
			}
		}
		var joined string
		if m.chatOutlineMode == chatOutlineDockedLeft {
			// Full-height separator: standalone │ column replaces the gap.
			sepHeight := max(lipgloss.Height(aside), lipgloss.Height(vpPanel))
			normalSep := BorderCharStyle.Render("│")
			sepLines := make([]string, sepHeight)
			for i := range sepLines {
				sepLines[i] = normalSep
			}
			if noteVertStart >= 0 {
				noteSep := NoteCharStyle.Render("│")
				for i := noteVertStart; i < sepHeight; i++ {
					sepLines[i] = noteSep
				}
				// Round the corner where h-separator meets v-separator.
				if noteVertStart > 0 {
					sepLines[noteVertStart-1] = NoteCharStyle.Render("╮")
				}
			}
			joined = lipgloss.JoinHorizontal(lipgloss.Top, aside, strings.Join(sepLines, "\n"), vpPanel)
		} else {
			joined = lipgloss.JoinHorizontal(lipgloss.Top, vpPanel, " ", aside)
		}
		joinedClip := lipgloss.NewStyle().MaxWidth(contentWidth).Render(joined)
		contentBox = contentStyle.Width(contentWidth).Render(joinedClip)
	} else {
		contentBox = contentStyle.Width(contentWidth).Render(vpRaw)
		if showChatOutline { // overlay mode
			outlinePanel := m.renderChatOutline(panelWidth)
			col := lipgloss.Width(contentBox) - lipgloss.Width(outlinePanel) - 1
			contentBox = overlayAt(contentBox, outlinePanel, col, 1)
			if showNote {
				notePanel := m.renderNotePanel(panelWidth)
				row := 1 + lipgloss.Height(outlinePanel)
				contentBox = overlayAt(contentBox, notePanel, col, row)
			}
		} else if showNote {
			notePanel := m.renderNotePanel(panelWidth)
			col := lipgloss.Width(contentBox) - lipgloss.Width(notePanel) - 1
			contentBox = overlayAt(contentBox, notePanel, col, 1)
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

	// Bottom bar: session title (left) + footer metadata (right-aligned)
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

	footer := m.renderFooter(s, avatar, badge, meta)

	return lipgloss.JoinVertical(lipgloss.Left, header, contentBox, footer)
}

// renderFooter builds the bottom bar: label + content (left) + metadata (right-aligned).
// Insight footer: "★ Insight │ <glamour-rendered text>". No bubble — glamour provides styling.
// Non-insight footer: avatar+badge bubble with last assistant message.
func (m *DetailModel) renderFooter(s *claude.ClaudeSession, avatar, badge, meta string) string {
	metaWidth := lipgloss.Width(meta)

	if m.renderedInsight != "" {
		label := InsightLabelStyle.Render("★ Insight")
		sep := InsightSepStyle.Render(" │ ")
		prefix := label + sep
		prefixWidth := lipgloss.Width(prefix)
		// overhead: prefix + gap(2) + margin(2)
		maxW := m.width - prefixWidth - metaWidth - 4
		if len(s.Insights) > 1 {
			maxW -= 2
		}
		content := prefix
		if maxW > 0 {
			text := ansi.Truncate(m.renderedInsight, maxW, "…")
			if len(s.Insights) > 1 {
				text += " ↩"
			}
			content = prefix + text
		}
		contentWidth := lipgloss.Width(content)
		gap := m.width - contentWidth - metaWidth - 2
		if gap < 1 {
			gap = 1
		}
		return content + strings.Repeat(" ", gap) + meta
	}

	// Non-insight: avatar+badge bubble with last assistant message
	bubblePrefix := avatar + " " + badge + " "
	prefixWidth := lipgloss.Width(bubblePrefix)
	overheadFixed := prefixWidth + 8 // leftCap(1) + " "(1) + " "(1) + rightCap(1) + gap(2) + margin(2)

	lastResp := bubblePrefix
	bubbleMsg := s.LastAssistantMessage
	if bubbleMsg != "" {
		firstLine, _, multiline := strings.Cut(bubbleMsg, "\n")
		maxW := m.width - metaWidth - overheadFixed
		if multiline {
			maxW -= 2
		}
		if maxW > 0 {
			text := ansi.Truncate(firstLine, maxW, "…")
			if multiline {
				text += " ↩"
			}
			lastResp = bubblePrefix + BubbleLeftCap + BubbleTextStyle.Render(" "+text+" ") + BubbleRightCap
		}
	}

	lastRespWidth := lipgloss.Width(lastResp)
	gap := m.width - lastRespWidth - metaWidth - 2
	if gap < 1 {
		gap = 1
	}
	return lastResp + strings.Repeat(" ", gap) + meta
}

// AllQuietCounts holds per-section counts for the all-quiet dashboard.
type AllQuietCounts struct {
	Clauding int
	Later    int
	Backlog  int
}

// ViewAllQuiet renders the animated mobile scene with a contextual dashboard.
func (m *DetailModel) ViewAllQuiet(counts AllQuietCounts) string {
	if m.allQuiet.Active() {
		return m.allQuiet.Render(m.width, m.height, counts)
	}
	return renderStaticDashboard(m.width, m.height, counts)
}

// maxOutlineMessages is the maximum number of user messages visible in the chat outline.
// When there are more messages, the outline becomes scrollable.
const maxOutlineMessages = 15

// outlineGap is the number of space columns between the styled bullet glyph and message text.
const outlineGap = 1

// outlineIndicatorWidth returns the visual column width of a styled bullet indicator + gap.
// All bullet styles share the same Padding(0,1), so this is constant across glyph types.
func outlineIndicatorWidth() int {
	return lipgloss.Width(TranscriptBulletStyle.Render("x")) + outlineGap
}

// stripOutlinePrefix removes the type-prefix glyph (bash/plan/slash) from a
// flattened outline message, returning the stripped text.
func stripOutlinePrefix(flat string) string {
	switch {
	case strings.HasPrefix(flat, claude.BashCmdGlyph):
		return flat[len(claude.BashCmdGlyph):]
	case strings.HasPrefix(flat, claude.PlanGlyph):
		return flat[len(claude.PlanGlyph):]
	case strings.HasPrefix(flat, claude.SlashCmdGlyph):
		return flat[len(claude.SlashCmdGlyph):]
	default:
		return flat
	}
}

// renderChatOutline renders the user messages outline panel with a border.
func (m *DetailModel) renderChatOutline(width int) string {
	// Pick panel style: docked-left uses borderless style (separator drawn in View);
	// others use full rounded border.
	panelStyle := TranscriptOverlayStyle
	borderCols := 4 // border(2) + padding(2)
	if m.chatOutlineMode == chatOutlineDockedLeft {
		panelStyle = AsideDockLeftStyle
		borderCols = 2 // padding(2) only, no border
	}
	innerWidth := width - borderCols
	if innerWidth < 5 {
		innerWidth = 5
	}

	// Pre-compute per-type bullet styles to avoid per-iteration allocations.
	bulletContGlyph := lipgloss.NewStyle().Foreground(TranscriptBulletStyle.GetForeground()).Render("╰")
	cursorContGlyph := lipgloss.NewStyle().Foreground(TranscriptCursorStyle.GetForeground()).Render("╰")
	bashBulletStyle := TranscriptBulletStyle.Foreground(ColorBashCmd)
	planBulletStyle := TranscriptBulletStyle.Foreground(ColorPlan)
	slashBulletStyle := TranscriptBulletStyle.Foreground(ColorSlashCmd)

	var lines []string

	// Hoist constant layout values before the loop.
	indicatorWidth := outlineIndicatorWidth()
	msgWidth := max(1, innerWidth-indicatorWidth)
	indentPad := strings.Repeat(" ", indicatorWidth)

	titleLine := TranscriptTitleStyle.Foreground(ColorBorder).Render(" " + IconInput + "  Your Messages")
	lines = append(lines, titleLine)
	lines = append(lines, "") // blank line after title
	// Pulse the last bullet when the agent is working — regardless of whether
	// LastAssistantMessage is set, because that field holds the previous exchange's
	// response until the new one arrives.
	isLastPulsing := m.session != nil &&
		m.session.Status == claude.StatusAgentTurn &&
		len(m.userMessages) > 0

	// Compute visible window for scrollable outline.
	totalMsgs := len(m.userMessages)
	visStart, visEnd := m.outlineWindow()

	// Show scroll-up indicator.
	if visStart > 0 {
		arrow := ItemDetailStyle.Render(fmt.Sprintf("  ↑ %d more", visStart))
		lines = append(lines, arrow)
	}

	for i := visStart; i < visEnd; i++ {
		msg := m.userMessages[i]
		focused := i == m.msgCursor
		isLast := i == len(m.userMessages)-1

		// Flatten + detect/strip type prefix → promote prefix to bullet glyph.
		raw := strings.ReplaceAll(msg, "\n", " ")
		flat := stripOutlinePrefix(raw)
		bulletGlyph := IconQuote
		typedStyle := TranscriptBulletStyle
		switch {
		case strings.HasPrefix(raw, claude.BashCmdGlyph):
			bulletGlyph = "!"
			typedStyle = bashBulletStyle
		case strings.HasPrefix(raw, claude.PlanGlyph):
			bulletGlyph = IconPlan
			typedStyle = planBulletStyle
		case strings.HasPrefix(raw, claude.SlashCmdGlyph):
			bulletGlyph = "/"
			typedStyle = slashBulletStyle
		}

		msgStyle := TranscriptMsgStyle
		contGlyph := bulletContGlyph
		var styledGlyph string
		if isLast && isLastPulsing {
			// Breathing gradient: ping-pong through PulseGradient (6 colors, 10 frames/cycle ≈ 800ms).
			phase := m.pulsePhase % 10
			idx := phase
			if idx > 5 {
				idx = 10 - idx // bounce back: 4,3,2,1
			}
			styledGlyph = TranscriptBulletStyle.Foreground(PulseGradient[idx]).Render(bulletGlyph)
		} else if focused {
			// Only the default quote glyph adopts cursor color; typed glyphs keep their own color.
			if bulletGlyph == IconQuote {
				styledGlyph = TranscriptCursorStyle.Render(bulletGlyph)
			} else {
				styledGlyph = typedStyle.Render(bulletGlyph)
			}
			contGlyph = cursorContGlyph
		} else {
			styledGlyph = typedStyle.Render(bulletGlyph)
			msgStyle = ItemDetailStyle
		}
		// Indicator = styled glyph (includes style padding) + uniform gap.
		styledIndicator := styledGlyph + strings.Repeat(" ", outlineGap)

		if ansi.StringWidth(flat) <= msgWidth {
			lines = append(lines, styledIndicator+msgStyle.Render(flat))
		} else {
			// Two-line display: word-wrap at msgWidth, truncate second line.
			// ╰ aligns with the first character of line 1 text.
			line1, rest := wordWrapFirst(flat, msgWidth)
			indent := indentPad + contGlyph + " "
			line2 := ansi.Truncate(rest, max(1, msgWidth-2), "…")
			lines = append(lines,
				styledIndicator+msgStyle.Render(line1),
				indent+msgStyle.Render(line2),
			)
		}

		// After the last user message, render the assistant's response — but only
		// when the agent is not actively working (otherwise the response is stale).
		if isLast && !isLastPulsing && m.session != nil && m.session.LastAssistantMessage != "" {
			if reply := m.renderOutlineReply(innerWidth); reply != "" {
				lines = append(lines, reply)
			}
		}
	}

	// Show scroll-down indicator.
	if visEnd < totalMsgs {
		arrow := ItemDetailStyle.Render(fmt.Sprintf("  ↓ %d more", totalMsgs-visEnd))
		lines = append(lines, arrow)
	}

	content := strings.Join(lines, "\n")
	return panelStyle.
		Width(width).
		Render(content)
}

// outlineReplyMaxLines is the maximum number of word-wrapped lines shown for the
// assistant response in the chat outline.
const outlineReplyMaxLines = 5

// renderOutlineReply renders the last assistant response as a bordered block.
// Results are cached and invalidated when the message or width changes.
func (m *DetailModel) renderOutlineReply(innerWidth int) string {
	raw := strings.TrimSpace(m.session.LastAssistantMessage)
	if raw == "" {
		return ""
	}

	// Return cached block if inputs haven't changed.
	if raw == m.cachedReplyMsg && innerWidth == m.cachedReplyWidth {
		return m.cachedReplyBlock
	}

	// border(2) + padding(1 each side = 2) = 4 overhead columns
	contentWidth := max(5, innerWidth-4)

	// Render markdown via glamour; glamour adds 2 spaces of left indent which
	// dedentANSILine strips. Fall back to plain word-wrap if glamour fails.
	var lines []string
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(max(20, contentWidth-2)),
	)
	if err == nil {
		if rendered, rerr := r.Render(raw); rerr == nil {
			for _, line := range strings.Split(rendered, "\n") {
				// Skip leading blank lines before any content.
				if len(lines) == 0 && strings.TrimSpace(ansi.Strip(line)) == "" {
					continue
				}
				lines = append(lines, dedentANSILine(line))
			}
			// Trim trailing blank lines.
			for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
				lines = lines[:len(lines)-1]
			}
		}
	}
	if len(lines) == 0 {
		// Fallback: plain word wrap.
		wrapped := WordWrapContent(raw, contentWidth)
		lines = strings.Split(wrapped, "\n")
	}
	if len(lines) > outlineReplyMaxLines {
		lines = lines[:outlineReplyMaxLines]
		last := lines[outlineReplyMaxLines-1]
		lines[outlineReplyMaxLines-1] = ansi.Truncate(last, max(1, contentWidth-2), "…")
	}

	result := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(0, 1).
		Width(contentWidth).
		Render(strings.Join(lines, "\n"))

	m.cachedReplyMsg = raw
	m.cachedReplyWidth = innerWidth
	m.cachedReplyBlock = result
	return result
}

// renderNotePanel renders the session note panel with a border.
// When noteEditing is true, it shows the textarea for inline editing.
func (m *DetailModel) renderNotePanel(width int) string {
	panelStyle := NoteOverlayStyle
	borderCols := 6 // border(2) + padding(4)
	if m.chatOutlineMode == chatOutlineDockedLeft {
		panelStyle = NoteDockedStyle
		borderCols = 4 // padding(4) only, no border
	}
	innerWidth := width - borderCols
	if innerWidth < 5 {
		innerWidth = 5
	}

	titleLine := TranscriptTitleStyle.Foreground(ColorNote).Render(" " + IconNote + "  Notes")

	var body string
	if m.noteEditing {
		m.noteEditor.SetWidth(innerWidth)
		body = m.noteEditor.ViewTextarea()
	} else {
		wrapped := WordWrapContent(m.note, innerWidth)
		body = TranscriptMsgStyle.Render(wrapped)
	}

	borderColor := ColorBorder
	if m.noteEditing {
		borderColor = ColorNote
	}
	content := titleLine + "\n\n" + body
	return panelStyle.
		BorderForeground(borderColor).
		Width(width).
		Render(content)
}

// WordWrapContent wraps plain text to fit within maxWidth columns.
func WordWrapContent(s string, maxWidth int) string {
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
