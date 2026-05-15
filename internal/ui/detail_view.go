package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

// EmptyView renders the "no session selected" placeholder at the given size.
func (m *DetailModel) EmptyView(w, h int) string {
	return EmptyStyle.Width(w).Height(h).Render("Select a session to preview")
}

func (m *DetailModel) View() string {
	if m.session == nil {
		return m.EmptyView(m.width, m.height)
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
	hasRecap := m.chatOutlineMode != chatOutlineHidden && m.session != nil && strings.TrimSpace(m.session.LastRecap) != ""
	showChatOutline := m.chatOutlineMode != chatOutlineHidden && (len(m.userMessages) > 0 || m.summary != nil)
	showNote := (m.note != "" || m.noteEditing) && m.chatOutlineMode != chatOutlineHidden
	panelWidth := m.effectivePanelWidth(contentWidth)
	if (showChatOutline || showNote || hasRecap) && m.isChatOutlineDocked() {
		chatOutlineWidth := panelWidth
		vpWidth := contentWidth - chatOutlineWidth - 3 // 1 gap + 2 for content border
		vpView := truncateLines(vpRaw, vpWidth)
		vpPanel := lipgloss.NewStyle().Width(vpWidth).MaxWidth(vpWidth).Render(vpView)
		var outlinePart, recapPart, notePart string
		if showChatOutline {
			outlinePart = m.renderChatOutline(chatOutlineWidth)
		}
		if hasRecap {
			recapPart = m.renderRecapPanel(chatOutlineWidth)
		}
		if showNote {
			notePart = m.renderNotePanel(chatOutlineWidth)
		}
		aside, noteVertStart := m.assembleAside(chatOutlineWidth, outlinePart, recapPart, notePart, m.viewport.Height)
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
		if showChatOutline || hasRecap || showNote { // overlay mode
			// Top-down stack at the right edge: outline → recap → note.
			row := 1
			col := 0
			width := panelWidth
			placePanel := func(panel string) {
				if panel == "" {
					return
				}
				if col == 0 {
					col = lipgloss.Width(contentBox) - lipgloss.Width(panel) - 1
				}
				contentBox = overlayAt(contentBox, panel, col, row)
				row += lipgloss.Height(panel)
			}
			if showChatOutline {
				placePanel(m.renderChatOutline(width))
			}
			if hasRecap {
				placePanel(m.renderRecapPanel(width))
			}
			if showNote {
				placePanel(m.renderNotePanel(width))
			}
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

	footer := m.renderFooter(s, meta)

	return lipgloss.JoinVertical(lipgloss.Left, header, contentBox, footer)
}

// renderFooter builds the bottom bar: insight (if present) on the left, metadata right-aligned.
// Insight footer: "★ Insight │ <glamour-rendered text>".
// Otherwise: just right-aligned metadata.
func (m *DetailModel) renderFooter(s *claude.ClaudeSession, meta string) string {
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

	gap := m.width - metaWidth - 2
	if gap < 1 {
		gap = 1
	}
	return strings.Repeat(" ", gap) + meta
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

// maxOutlineMessages is the maximum number of past user messages visible in the
// chat outline. The current-turn block is always pinned at the bottom, so this
// cap applies only to scroll-back.
const maxOutlineMessages = 15

// outlineGap is the number of space columns between the styled bullet glyph and message text.
const outlineGap = 1

// Per-section line caps inside the "current turn" block.
const (
	outlineLastUserMaxLines   = 5
	outlineFirstReplyMaxLines = 2
)

// recapPanelMaxLines caps the body of the bottom-docked recap panel.
const recapPanelMaxLines = 8

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

// renderChatOutline renders the chat outline panel: a compact scroll-back of
// past user messages (one line each) followed by a "current turn" block that
// surfaces the latest user message, the agent's first reply (while in-flight),
// the files touched this turn, and the recap / last reply (once the turn ends).
func (m *DetailModel) renderChatOutline(width int) string {
	panelStyle := TranscriptOverlayStyle
	borderCols := 4 // border(2) + padding(2)
	if m.chatOutlineMode == chatOutlineDockedLeft {
		panelStyle = AsideDockLeftStyle
		borderCols = 2
	}
	innerWidth := max(5, width-borderCols)
	indicatorWidth := outlineIndicatorWidth()
	msgWidth := max(1, innerWidth-indicatorWidth)

	bashBulletStyle := TranscriptBulletStyle.Foreground(ColorBashCmd)
	planBulletStyle := TranscriptBulletStyle.Foreground(ColorPlan)
	slashBulletStyle := TranscriptBulletStyle.Foreground(ColorSlashCmd)

	isWorking := m.session != nil &&
		m.session.Status == claude.StatusAgentTurn &&
		len(m.userMessages) > 0

	var lines []string
	lines = append(lines,
		TranscriptTitleStyle.Foreground(ColorBorder).Render(" "+IconInput+"  Your Messages"),
		"",
	)

	if len(m.userMessages) == 0 {
		return panelStyle.Width(width).Render(strings.Join(lines, "\n"))
	}
	lastIdx := len(m.userMessages) - 1

	// Past messages: one line each. Dim by default; cursor brightens.
	visStart, visEnd := m.outlinePastWindow()
	if visStart > 0 {
		lines = append(lines, ItemDetailStyle.Render(fmt.Sprintf("  ↑ %d more", visStart)))
	}
	for i := visStart; i < visEnd; i++ {
		raw := m.userMessages[i]
		flat := stripOutlinePrefix(strings.ReplaceAll(raw, "\n", " "))
		glyph, glyphStyle := pickOutlineBullet(raw, bashBulletStyle, planBulletStyle, slashBulletStyle)
		msgStyle := ItemDetailStyle
		if i == m.msgCursor {
			if glyph == IconQuote {
				glyphStyle = TranscriptCursorStyle
			}
			msgStyle = TranscriptMsgStyle
		}
		lines = append(lines,
			glyphStyle.Render(glyph)+strings.Repeat(" ", outlineGap)+
				msgStyle.Render(ansi.Truncate(flat, msgWidth, "…")),
		)
	}
	if visEnd < lastIdx {
		lines = append(lines, ItemDetailStyle.Render(fmt.Sprintf("  ↓ %d more", lastIdx-visEnd)))
	}

	// Current-turn separator rule — only when there's scroll-back to separate from.
	if lastIdx > 0 {
		lines = append(lines, renderTurnSeparator(innerWidth))
	}

	// Current user message (multi-line capped).
	rawCur := m.userMessages[lastIdx]
	flatCur := stripOutlinePrefix(strings.ReplaceAll(rawCur, "\n", " "))
	glyphCur, glyphStyleCur := pickOutlineBullet(rawCur, bashBulletStyle, planBulletStyle, slashBulletStyle)
	contColor := TranscriptBulletStyle.GetForeground()
	msgStyleCur := TranscriptMsgStyle
	if isWorking {
		phase := m.pulsePhase % 10
		pi := phase
		if pi > 5 {
			pi = 10 - pi
		}
		glyphStyleCur = TranscriptBulletStyle.Foreground(PulseGradient[pi])
		contColor = PulseGradient[pi]
	} else if lastIdx == m.msgCursor && glyphCur == IconQuote {
		glyphStyleCur = TranscriptCursorStyle
		contColor = TranscriptCursorStyle.GetForeground()
	}
	// Continuation glyph mirrors the bullet's Padding(0,1) so `│` lands in the
	// same column as the bullet and the wrapped text aligns with line 1.
	contGlyphStyle := lipgloss.NewStyle().Foreground(contColor).Padding(0, 1)
	for k, ln := range wrapToCappedLines(flatCur, msgWidth, outlineLastUserMaxLines) {
		if k == 0 {
			lines = append(lines, glyphStyleCur.Render(glyphCur)+strings.Repeat(" ", outlineGap)+msgStyleCur.Render(ln))
		} else {
			lines = append(lines, contGlyphStyle.Render("│")+strings.Repeat(" ", outlineGap)+msgStyleCur.Render(ln))
		}
	}

	// Agent reply in the current turn. While the agent is working we show the
	// first reply ("I'll start by…") as the in-flight signal; after the turn
	// ends we show the last assistant text as the wrap-up. The Recap panel
	// lives separately at the bottom — independent of this slot.
	var agentReply string
	if isWorking {
		agentReply = strings.TrimSpace(m.currentTurn.FirstAssistantText)
	} else if m.session != nil {
		agentReply = strings.TrimSpace(m.session.LastAssistantMessage)
	}
	if agentReply != "" {
		lines = append(lines, renderTurnAccessory(agentReply, innerWidth, "↪ ", outlineFirstReplyMaxLines, ItemDetailStyle)...)
	}

	// Files touched this turn. Stays live as tool_use entries land. The row
	// gets a one-line break above it and renders on a subtle bg band so it
	// reads as turn metadata rather than another line of the user's message.
	// Bg is composed into every leaf style inside renderTurnFilesRow so the
	// band is continuous across child segments; the outer Width pad picks up
	// the same bg for the trailing whitespace.
	if len(m.currentTurn.Files) > 0 {
		if row := renderTurnFilesRow(m.currentTurn.Files, innerWidth, TurnFilesBg); row != "" {
			lines = append(lines, "")
			lines = append(lines, lipgloss.NewStyle().Background(TurnFilesBg).Width(innerWidth).Render(row))
		}
	}

	return panelStyle.Width(width).Render(strings.Join(lines, "\n"))
}

// pickOutlineBullet picks the bullet glyph + style for a user message based on
// the type-prefix (bash, plan, slash) embedded by the transcript reader.
func pickOutlineBullet(raw string, bashStyle, planStyle, slashStyle lipgloss.Style) (string, lipgloss.Style) {
	switch {
	case strings.HasPrefix(raw, claude.BashCmdGlyph):
		return "!", bashStyle
	case strings.HasPrefix(raw, claude.PlanGlyph):
		return IconPlan, planStyle
	case strings.HasPrefix(raw, claude.SlashCmdGlyph):
		return "/", slashStyle
	default:
		return IconQuote, TranscriptBulletStyle
	}
}

// wrapToCappedLines word-wraps text to width and truncates to maxLines, suffixing
// the last line with " ↩" when content was cut.
func wrapToCappedLines(text string, width, maxLines int) []string {
	wrapped := strings.Split(WordWrapContent(text, width), "\n")
	if len(wrapped) <= maxLines {
		return wrapped
	}
	out := wrapped[:maxLines]
	last := out[maxLines-1]
	if ansi.StringWidth(last)+2 > width {
		last = ansi.Truncate(last, max(1, width-2), "")
	}
	out[maxLines-1] = last + " ↩"
	return out
}

// renderTurnAccessory wraps text with a one-line prefix on line 1 and continuation
// indent on subsequent lines, capped to maxLines and styled with one style.
func renderTurnAccessory(text string, innerWidth int, prefix string, maxLines int, style lipgloss.Style) []string {
	pw := ansi.StringWidth(prefix)
	contentWidth := max(1, innerWidth-pw)
	indent := strings.Repeat(" ", pw)
	wrapped := wrapToCappedLines(text, contentWidth, maxLines)
	out := make([]string, len(wrapped))
	for i, ln := range wrapped {
		if i == 0 {
			out[i] = style.Render(prefix + ln)
		} else {
			out[i] = style.Render(indent + ln)
		}
	}
	return out
}

// renderTurnFilesRow renders one line of `file +A -R · file +A -R · …+N`.
// Every leaf style composes the given bg so the row reads as one continuous
// band — applying bg only at the parent leaves gaps where each child's reset
// (`\x1b[0m`) drops the background. Returns "" when nothing fits.
func renderTurnFilesRow(files []claude.TurnFileStat, innerWidth int, bg lipgloss.TerminalColor) string {
	iconStyle := ItemDetailStyle.Background(bg)
	dimStyle := ItemDetailStyle.Background(bg)
	nameStyle := TranscriptMsgStyle.Background(bg)
	addedStyle := DiffAddedStyle.Background(bg)
	removedStyle := StatWorkingStyle.Background(bg)

	iconPrefix := iconStyle.Render(IconFile + " ")
	overheadW := lipgloss.Width(iconPrefix)
	sep := dimStyle.Render(" · ")
	sepW := lipgloss.Width(sep)
	budget := max(1, innerWidth-overheadW)

	var entries []string
	used := 0
	for i, f := range files {
		base := filepath.Base(f.Path)
		nameRendered := nameStyle.Render(base)
		var statRendered string
		statW := 0
		if f.Added > 0 {
			rendered := addedStyle.Render(fmt.Sprintf(" +%d", f.Added))
			statRendered += rendered
			statW += lipgloss.Width(rendered)
		}
		if f.Removed > 0 {
			rendered := removedStyle.Render(fmt.Sprintf(" -%d", f.Removed))
			statRendered += rendered
			statW += lipgloss.Width(rendered)
		}
		entry := nameRendered + statRendered
		eW := lipgloss.Width(nameRendered) + statW
		needed := eW
		if len(entries) > 0 {
			needed += sepW
		}
		if used+needed > budget && len(entries) > 0 {
			remaining := len(files) - i
			entries = append(entries, dimStyle.Render(fmt.Sprintf("…+%d", remaining)))
			break
		}
		entries = append(entries, entry)
		used += needed
	}
	if len(entries) == 0 {
		return ""
	}
	return iconPrefix + strings.Join(entries, sep)
}

// assembleAside stacks the aside sections vertically with recap + note bottom-
// docked: outline (top, padded), then recap, then note. In docked-left mode an
// h-separator is drawn between adjacent sections. Returns the aside string and
// the row at which the note panel begins (-1 when no note is shown), used for
// the v-separator color transition in docked-left.
func (m *DetailModel) assembleAside(width int, outline, recap, note string, targetHeight int) (string, int) {
	isDockedLeft := m.chatOutlineMode == chatOutlineDockedLeft

	// Heights of each section. Recap and note panels include their own top
	// separator now (pulse-style), so no inter-panel separator slot is needed.
	outlineH := 0
	if outline != "" {
		outlineH = lipgloss.Height(outline)
	}
	recapH := 0
	if recap != "" {
		recapH = lipgloss.Height(recap)
	}
	noteH := 0
	if note != "" {
		noteH = lipgloss.Height(note)
	}

	// Only pad when something is being bottom-docked — otherwise let the outline
	// stay compact (the contentBox's own border will close at its natural height).
	hasBottom := recap != "" || note != ""
	filler := 0
	if hasBottom {
		filler = max(0, targetHeight-outlineH-recapH-noteH)
	}

	var sections []string
	if outline != "" {
		sections = append(sections, outline)
	}
	if filler > 0 {
		sections = append(sections, lipgloss.NewStyle().Height(filler).Render(""))
	}
	if recap != "" {
		sections = append(sections, recap)
	}

	noteVertStart := -1
	if note != "" {
		if isDockedLeft && m.noteEditing {
			noteVertStart = 0
			for _, s := range sections {
				noteVertStart += lipgloss.Height(s)
			}
		}
		sections = append(sections, note)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...), noteVertStart
}

// renderRecapPanel renders the bottom-docked session recap panel. Returns ""
// when the session has no away_summary recap. Uses the same compact layout as
// the sidebar pulse: top separator, single-line title, body indented by 1 col.
func (m *DetailModel) renderRecapPanel(width int) string {
	if m.session == nil {
		return ""
	}
	raw := strings.TrimSpace(m.session.LastRecap)
	if raw == "" {
		return ""
	}
	if raw == m.cachedRecapSrc && width == m.cachedRecapWidth && m.cachedRecapBlock != "" {
		return m.cachedRecapBlock
	}
	innerWidth := max(5, width-1) // PaddingLeft(1)

	sep := BorderCharStyle.Render(strings.Repeat("─", width))
	titleLine := lipgloss.NewStyle().Width(width).Render(
		" " + TranscriptTitleStyle.Foreground(ColorLater).Render("★ Recap"),
	)

	var rendered []string
	if r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(innerWidth),
	); err == nil {
		if out, gerr := r.Render(raw); gerr == nil {
			for _, line := range strings.Split(out, "\n") {
				if len(rendered) == 0 && strings.TrimSpace(ansi.Strip(line)) == "" {
					continue
				}
				rendered = append(rendered, dedentANSILine(line))
			}
			for len(rendered) > 0 && strings.TrimSpace(ansi.Strip(rendered[len(rendered)-1])) == "" {
				rendered = rendered[:len(rendered)-1]
			}
		}
	}
	if len(rendered) == 0 {
		rendered = strings.Split(WordWrapContent(raw, innerWidth), "\n")
	}
	if len(rendered) > recapPanelMaxLines {
		rendered = rendered[:recapPanelMaxLines]
		last := rendered[recapPanelMaxLines-1]
		if ansi.StringWidth(ansi.Strip(last))+2 > innerWidth {
			last = ansi.Truncate(last, max(1, innerWidth-2), "")
		}
		rendered[recapPanelMaxLines-1] = last + " ↩"
	}
	body := lipgloss.NewStyle().Width(width).PaddingLeft(1).Render(strings.Join(rendered, "\n"))
	out := lipgloss.JoinVertical(lipgloss.Left, sep, titleLine, body)
	m.cachedRecapSrc = raw
	m.cachedRecapWidth = width
	m.cachedRecapBlock = out
	return out
}

// renderTurnSeparator draws the `─── current turn ───` rule.
func renderTurnSeparator(innerWidth int) string {
	label := " current turn "
	lw := lipgloss.Width(label)
	if innerWidth < lw+4 {
		return BorderCharStyle.Render(strings.Repeat("─", innerWidth))
	}
	left := (innerWidth - lw) / 2
	right := innerWidth - lw - left
	return BorderCharStyle.Render(strings.Repeat("─", left)) +
		ItemDetailStyle.Render(label) +
		BorderCharStyle.Render(strings.Repeat("─", right))
}

// renderNotePanel renders the session note panel. Uses the same compact layout
// as the sidebar pulse: top separator, single-line title, body indented by 1.
// When noteEditing is true, it shows the textarea for inline editing and tints
// the top separator with the note color.
func (m *DetailModel) renderNotePanel(width int) string {
	innerWidth := max(5, width-1) // PaddingLeft(1)

	sepStyle := BorderCharStyle
	if m.noteEditing {
		sepStyle = NoteCharStyle
	}
	sep := sepStyle.Render(strings.Repeat("─", width))
	titleLine := lipgloss.NewStyle().Width(width).Render(
		" " + TranscriptTitleStyle.Foreground(ColorNote).Render(IconNote+" Notes"),
	)

	var body string
	if m.noteEditing {
		m.noteEditor.SetWidth(innerWidth)
		body = m.noteEditor.ViewTextarea()
	} else {
		wrapped := WordWrapContent(m.note, innerWidth)
		body = TranscriptMsgStyle.Render(wrapped)
	}
	body = lipgloss.NewStyle().Width(width).PaddingLeft(1).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, sep, titleLine, body)
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
