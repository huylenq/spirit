package ui

// Session item rendering — shared between sidebar list and work queue cards.
// All methods remain on SidebarModel to access its state (diffStats, spinnerView,
// flaggedSessions, animation frames, cardMode, etc.).

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

const (
	providerClaudeGlyph = "󰛄" // asterisk, matching Claude's mark
	providerCodexGlyph  = "󰝨" // atom/knot, matching OpenAI's mark
	providerPrefixWidth = 2   // one glyph + one trailing space
)

var (
	providerClaudeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#D97757"))
	providerCodexStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3B82F6"))
)

func providerPrefix(s claude.ClaudeSession) string {
	if s.Provider == claude.ProviderCodex {
		return providerCodexStyle.Render(providerCodexGlyph) + " "
	}
	return providerClaudeStyle.Render(providerClaudeGlyph) + " "
}

func providerPrefixBg(s claude.ClaudeSession, bg lipgloss.TerminalColor) string {
	if s.Provider == claude.ProviderCodex {
		return providerCodexStyle.Background(bg).Render(providerCodexGlyph) + lipgloss.NewStyle().Background(bg).Render(" ")
	}
	return providerClaudeStyle.Background(bg).Render(providerClaudeGlyph) + lipgloss.NewStyle().Background(bg).Render(" ")
}

// titleLayout decides how a session's display name is laid out on its row.
// It returns the (possibly truncated) first line, the second-line remainder
// ("" when the title fits on one line), and whether the session is the
// "(New session)" placeholder. The title only wraps to a second line when the
// last-message subtitle is hidden; otherwise it is truncated to a single line.
// During search (query != "") the last-message subtitle is always shown, so the
// title truncates rather than wraps regardless of the hideLastMessage pref.
// Width math here MUST mirror renderItem's prefix/icon/detail accounting.
func (m SidebarModel) titleLayout(s claude.ClaudeSession, query string) (line1, rest string, isNew bool) {
	name := s.DisplayName()
	if name == "" {
		return "", "", true
	}
	name = strings.ReplaceAll(name, "\n", " ")

	detailWidth := lipgloss.Width(m.renderDetail(s, false))
	prefixWidth := 4
	if m.cardMode {
		prefixWidth = 1
	}
	var worktreeIcon string
	if s.IsWorktree {
		worktreeIcon = worktreeIconRendered
	}
	iconWidth := lipgloss.Width(AvatarStyle(s.AvatarColorIdx).Render(AvatarGlyph(s.AvatarAnimalIdx)+"  ") + providerPrefix(s) + worktreeIcon)

	// 2 for outer padding, 2 for minimum gap; reserve the "[CODE] " prefix width.
	maxNameWidth := m.width - prefixWidth - iconWidth - detailWidth - 4 - projectCodeWidth(s)
	if maxNameWidth < 4 {
		maxNameWidth = 4
	}
	if lipgloss.Width(name) <= maxNameWidth {
		return name, "", false
	}
	if m.hideLastMessage && query == "" {
		l1, r := wordWrapFirst(name, maxNameWidth)
		if r != "" {
			// Second line spans the full row (no detail column on its right).
			restWidth := m.width - prefixWidth - iconWidth - 2
			if restWidth < 1 {
				restWidth = 1
			}
			return l1, ansi.Truncate(r, restWidth, "…"), false
		}
	}
	return ansi.Truncate(name, maxNameWidth, "…"), "", false
}

func (m SidebarModel) renderItem(isSelected, isAutoJump bool, s claude.ClaudeSession, query string) string {

	// Display name priority: custom title → synthesized title → first message → (new session).
	// When the last-message subtitle is hidden, a long title wraps onto a second
	// line (titleRest) instead of being truncated to one.
	displayName, titleRest, isNewSession := m.titleLayout(s, query)
	if isNewSession {
		displayName = lipgloss.NewStyle().Italic(true).Render("(New session)")
	}

	glyph := AvatarGlyph(s.AvatarAnimalIdx)
	hasQuery := query != ""

	// Avatar-colored selection styles (only allocated for the active state)
	avatarColor := AvatarColor(s.AvatarColorIdx)
	avatarHex := avatarColor.Dark
	avatarBg := AvatarFillBg(s.AvatarColorIdx)
	isLanding := isSelected && s.PaneID == m.landPaneID && m.landFrame < m.landMaxFrames
	isTrail := !isSelected && !isAutoJump && s.PaneID == m.trailPaneID && m.trailFrame < JumpAnimFrames
	var selBgSt, barSt, autoJumpBarSt, trailBarSt lipgloss.Style
	if isSelected {
		selBgSt = lipgloss.NewStyle().Background(avatarBg)
		var barColor lipgloss.TerminalColor = avatarColor
		if isLanding {
			t := m.landT()
			barColor = lipgloss.Color(blendHex("#ffffff", avatarHex, t))
		}
		barSt = lipgloss.NewStyle().Foreground(barColor).Background(avatarBg)
	} else if isAutoJump {
		autoJumpBarSt = lipgloss.NewStyle().Foreground(avatarColor)
	} else if isTrail {
		t := float64(m.trailFrame) / float64(JumpAnimFrames-1)
		trailBarSt = lipgloss.NewStyle().Foreground(lipgloss.Color(blendHex(avatarHex, "#333333", t)))
	}

	withBg := func(st lipgloss.Style) lipgloss.Style { return selBg(st, isSelected, s.AvatarColorIdx) }
	// Detail (spinner/age) stays on line 1; drift, overlap, diff stats move to stats line
	detail := m.renderDetail(s, isSelected)
	detailWidth := lipgloss.Width(detail)

	// prefix: 4 cells in sidebar (slot+flag+bar+space), 1 cell in card mode (space only)
	prefixWidth := 4
	if m.cardMode {
		prefixWidth = 1
	}
	var worktreeIcon string
	if s.IsWorktree {
		worktreeIcon = worktreeIconRendered
	}
	iconStr := AvatarStyle(s.AvatarColorIdx).Render(glyph+"  ") + providerPrefix(s) + worktreeIcon
	iconWidth := lipgloss.Width(iconStr)

	// Geometric gap — computed once before styling branches to prevent ANSI width drift
	displayNameWidth := lipgloss.Width(displayName)
	gap := m.width - prefixWidth - iconWidth - projectCodeWidth(s) - displayNameWidth - detailWidth - 2
	if gap < 1 {
		gap = 1
	}

	// Pack active indicators left: slot and flag float to col 0/1 based on presence.
	var indicators [2]string
	indicators[0] = " "
	indicators[1] = " "
	idx := 0
	if slot := m.SlotForSession(s.PaneID); slot != 0 {
		indicators[idx] = slotItemStyle.Render(fmt.Sprintf("%d", slot))
		idx++
	}
	if m.flaggedSessions[s.PaneID] {
		indicators[idx] = flagItemStyle.Render(IconFlag)
	}
	slotGlyph := indicators[0]
	flagGlyph := indicators[1]

	var namePart, gapStr string
	if isSelected {
		bg := selBgSt
		var styledName string
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, bg)
		} else {
			styledName = bg.Render(displayName)
		}
		styledName = renderProjectCodeBg(s, avatarBg) + styledName
		var selWorktreeIcon string
		if s.IsWorktree {
			selWorktreeIcon = worktreeIconStyle.Background(avatarBg).Render(IconWorktree) + bg.Render(" ")
		}
		if m.cardMode {
			namePart = bg.Render(" ") +
				AvatarStyle(s.AvatarColorIdx).Background(avatarBg).Render(glyph+"  ") +
				providerPrefixBg(s, avatarBg) +
				selWorktreeIcon +
				styledName
		} else {
			namePart = slotGlyph + flagGlyph +
				barSt.Render("▌") +
				bg.Render(" ") +
				AvatarStyle(s.AvatarColorIdx).Background(avatarBg).Render(glyph+"  ") +
				providerPrefixBg(s, avatarBg) +
				selWorktreeIcon +
				styledName
		}
		gapStr = bg.Render(strings.Repeat(" ", gap))
	} else {
		var styledName string
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, lipgloss.NewStyle())
		} else {
			styledName = displayName
		}
		styledName = renderProjectCode(s) + styledName
		if m.cardMode {
			namePart = " " + iconStr + styledName
		} else if isAutoJump {
			namePart = slotGlyph + flagGlyph + autoJumpBarSt.Render("▯") + " " + iconStr + styledName
		} else if isTrail {
			namePart = slotGlyph + flagGlyph + trailBarSt.Render("▯") + " " + iconStr + styledName
		} else {
			namePart = slotGlyph + flagGlyph + "  " + iconStr + styledName
		}
		gapStr = strings.Repeat(" ", gap)
	}

	line := namePart + gapStr + detail

	// Wrapped title second line (only present when the last-message subtitle is hidden).
	if titleRest != "" {
		line += "\n" + m.renderTitleContinuation(titleRest, query, isSelected, isAutoJump, isTrail, hasQuery && !isNewSession, prefixWidth+iconWidth, s.AvatarColorIdx, barSt, autoJumpBarSt, trailBarSt)
	}

	// selSubtitle wraps a subtitle content string with the selection bar at col 2.
	// In card mode: no bar prefix, just indent.
	selSubPrefixW := 5 // "  ▌" + padding = 5 cells consumed before content
	selSubtitle := func(style lipgloss.Style, content string) string {
		if m.cardMode {
			return withBg(style).Width(m.width - 1).Render(" " + content)
		}
		return "  " + barSt.Render("▌") +
			withBg(style).Width(m.width-selSubPrefixW).Render(content)
	}

	// autoJumpSubtitle wraps an unselected subtitle with the auto-jump bar at col 2.
	autoJumpSubtitle := func(style lipgloss.Style, content string) string {
		if m.cardMode {
			return style.Render(" " + content)
		}
		return "  " + autoJumpBarSt.Render("▯") + style.Render("   "+content)
	}

	if m.summaryLoadingPanes[s.PaneID] {
		if isSelected {
			line += "\n" + selSubtitle(selBgSt.Foreground(ColorMuted).Italic(true), "   "+m.spinnerView+" synthesizing…")
		} else if isAutoJump {
			line += "\n" + autoJumpSubtitle(SummaryStyle, m.spinnerView+" synthesizing…")
		} else if m.cardMode {
			line += "\n" + SummaryStyle.Render(" "+m.spinnerView+" synthesizing…")
		} else {
			line += "\n" + SummaryStyle.Render("      "+m.spinnerView+" synthesizing…")
		}
	}

	// Show queue badge with count
	if len(s.QueuePending) > 0 {
		queueBadge := fmt.Sprintf("%s %d", IconQueue, len(s.QueuePending))
		line += "\n" + m.renderSubtitleLine(queueBadge, query, "", isSelected, isAutoJump, false, s.AvatarColorIdx, barSt)
	}

	// Show last user message as subtitle (up to two lines, word-wrapped)
	if s.LastUserMessage != "" && (!m.hideLastMessage || hasQuery) {
		rawMsg := strings.ReplaceAll(s.LastUserMessage, "\n", " ")
		doHL := hasQuery && matchesNarrow(s.LastUserMessage, query)
		line += "\n" + m.renderSubtitleTwoLines(rawMsg, query, IconQuote, isSelected, isAutoJump, doHL, s.AvatarColorIdx, barSt)
	}

	// Match-context subtitles: show non-visible fields that matched the search
	if hasQuery {
		// SynthesizedTitle: shown when it's not the display name (i.e. customTitle is set) and matches
		if s.SynthesizedTitle != "" && s.CustomTitle != "" && matchesNarrow(s.SynthesizedTitle, query) {
			line += "\n" + m.renderSubtitleLine(s.SynthesizedTitle, query, IconSynthTitle, isSelected, isAutoJump, true, s.AvatarColorIdx, barSt)
		}
		// FirstMessage: shown when it's not the display name (customTitle or synthesized title is set) and matches
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.SynthesizedTitle != "") && matchesNarrow(s.FirstMessage, query) {
			rawFirst := strings.ReplaceAll(s.FirstMessage, "\n", " ")
			line += "\n" + m.renderSubtitleLine(rawFirst, query, IconQuote, isSelected, isAutoJump, true, s.AvatarColorIdx, barSt)
		}
	}

	// Badges line (no stats — stats are pinned by the caller after height is finalized).
	badges := renderBadges(s, withBg, query)
	showTagInput := !m.cardMode && isSelected && m.inlineTagSessionID == s.SessionID && m.inlineTagInputView != ""

	if showTagInput {
		sep := ""
		if badges != "" {
			sep = "  "
		}
		line += "\n" + "  " + barSt.Render("▌") +
			withBg(ItemDetailStyle).Render("   "+badges+sep) + m.inlineTagInputView
	} else if badges != "" {
		m.renderBadgesLine(&line, badges, isSelected, isAutoJump, barSt, autoJumpBarSt, withBg)
	}

	return line
}

// renderBadgesLine appends a new line with badge content, respecting sidebar selection and card mode.
func (m SidebarModel) renderBadgesLine(line *string, badges string, isSelected, isAutoJump bool, barSt, autoJumpBarSt lipgloss.Style, withBg func(lipgloss.Style) lipgloss.Style) {
	if m.cardMode {
		if isSelected {
			*line += "\n" + withBg(ItemDetailStyle).Render(" "+badges)
		} else {
			*line += "\n" + ItemDetailStyle.Render(" "+badges)
		}
		return
	}
	if isSelected {
		*line += "\n" + "  " + barSt.Render("▌") +
			withBg(ItemDetailStyle).Render("   "+badges)
	} else if isAutoJump {
		*line += "\n" + "  " + autoJumpBarSt.Render("▯") +
			ItemDetailStyle.Render("   "+badges)
	} else {
		*line += "\n" + ItemDetailStyle.Render("      "+badges)
	}
}

func (m SidebarModel) subtitleMsgWidth(icon string, isSelected bool) int {
	if m.cardMode {
		prefix := " " + icon + " "
		w := m.width - 1 - lipgloss.Width(prefix)
		if w < 1 {
			return 1
		}
		return w
	}
	if isSelected {
		prefix := "   " + icon + " "
		w := m.width - 5 - lipgloss.Width(prefix)
		if w < 1 {
			return 1
		}
		return w
	}
	prefix := "      " + icon + " "
	w := m.width - 2 - lipgloss.Width(prefix)
	if w < 1 {
		return 1
	}
	return w
}

// renderSubtitleLine renders a subtitle with optional search highlighting.
// Each segment gets its own Render call — no nesting of lipgloss Render.
func (m SidebarModel) renderSubtitleLine(text, query, icon string, isSelected, isAutoJump, doHighlight bool, avatarColorIdx int, barSt lipgloss.Style) string {
	msgWidth := m.subtitleMsgWidth(icon, isSelected)
	truncated := ansi.Truncate(text, msgWidth, "…")

	if m.cardMode {
		prefix := " " + icon + " "
		if isSelected {
			fillBg := AvatarFillBg(avatarColorIdx)
			baseStyle := ItemDetailStyle.Background(fillBg)
			bgStyle := lipgloss.NewStyle().Background(fillBg)
			prefixWidth := lipgloss.Width(prefix)
			var content string
			if doHighlight && query != "" {
				content = baseStyle.Render(prefix) + highlightMatch(truncated, query, baseStyle)
			} else {
				content = baseStyle.Render(prefix + truncated)
			}
			padWidth := m.width - 1 - prefixWidth - lipgloss.Width(truncated)
			if padWidth < 0 {
				padWidth = 0
			}
			return content + bgStyle.Render(strings.Repeat(" ", padWidth))
		}
		if doHighlight && query != "" {
			return ItemDetailStyle.Render(prefix) + highlightMatch(truncated, query, ItemDetailStyle)
		}
		return ItemDetailStyle.Render(prefix + truncated)
	}

	if isSelected {
		prefix := "   " + icon + " "
		prefixWidth := lipgloss.Width(prefix)
		fillBg := AvatarFillBg(avatarColorIdx)
		baseStyle := ItemDetailStyle.Background(fillBg)
		bgStyle := lipgloss.NewStyle().Background(fillBg)

		var content string
		if doHighlight && query != "" {
			content = baseStyle.Render(prefix) + highlightMatch(truncated, query, baseStyle)
		} else {
			content = baseStyle.Render(prefix + truncated)
		}
		// Manual padding to fill width (can't use .Width().Render() on pre-highlighted content)
		contentPlainWidth := prefixWidth + lipgloss.Width(truncated)
		padWidth := m.width - 5 - contentPlainWidth
		if padWidth < 0 {
			padWidth = 0
		}
		return "  " + barSt.Render("▌") + content + bgStyle.Render(strings.Repeat(" ", padWidth))
	}

	// Unselected — with optional auto-jump bar at col 2
	if isAutoJump {
		localAutoJumpSt := lipgloss.NewStyle().Foreground(AvatarColor(avatarColorIdx))
		prefix := "   " + icon + " "
		if doHighlight && query != "" {
			return "  " + localAutoJumpSt.Render("▯") + ItemDetailStyle.Render(prefix) + highlightMatch(truncated, query, ItemDetailStyle)
		}
		return "  " + localAutoJumpSt.Render("▯") + ItemDetailStyle.Render(prefix+truncated)
	}
	prefix := "      " + icon + " "
	if doHighlight && query != "" {
		return ItemDetailStyle.Render(prefix) + highlightMatch(truncated, query, ItemDetailStyle)
	}
	return ItemDetailStyle.Render(prefix + truncated)
}

// renderSubtitleTwoLines renders up to two lines for a subtitle, word-wrapping
// at word boundaries. The first line gets the icon; the second is indented with
// spaces matching the icon's width.
func (m SidebarModel) renderSubtitleTwoLines(text, query, icon string, isSelected, isAutoJump, doHighlight bool, avatarColorIdx int, barSt lipgloss.Style) string {
	msgWidth := m.subtitleMsgWidth(icon, isSelected)
	if msgWidth < 1 {
		return m.renderSubtitleLine(text, query, icon, isSelected, isAutoJump, doHighlight, avatarColorIdx, barSt)
	}

	// Word-wrap at word boundary to split into two lines
	line1, rest := wordWrapFirst(text, msgWidth)
	if rest == "" {
		return m.renderSubtitleLine(text, query, icon, isSelected, isAutoJump, doHighlight, avatarColorIdx, barSt)
	}

	// Render first line, second line with blank icon of same width
	first := m.renderSubtitleLine(line1, query, icon, isSelected, isAutoJump, doHighlight, avatarColorIdx, barSt)
	blankIcon := strings.Repeat(" ", lipgloss.Width(icon))
	second := m.renderSubtitleLine(rest, query, blankIcon, isSelected, isAutoJump, doHighlight, avatarColorIdx, barSt)
	return first + "\n" + second
}

// renderTitleContinuation renders the wrapped-over tail of a long session title
// on a second line, aligned under where the title text begins on line 1 (indent
// = prefix + icon width). Styled like the title — selection-tinted, not dimmed
// like a subtitle. Only invoked when the last-message subtitle is hidden.
func (m SidebarModel) renderTitleContinuation(rest, query string, isSelected, isAutoJump, isTrail, doHighlight bool, indent, avatarColorIdx int, barSt, autoJumpBarSt, trailBarSt lipgloss.Style) string {
	highlighted := func(base lipgloss.Style) string {
		if doHighlight && query != "" {
			return highlightMatch(rest, query, base)
		}
		return base.Render(rest)
	}

	if m.cardMode {
		if isSelected {
			bg := lipgloss.NewStyle().Background(AvatarFillBg(avatarColorIdx))
			padWidth := m.width - 1 - indent - lipgloss.Width(rest)
			if padWidth < 0 {
				padWidth = 0
			}
			return bg.Render(strings.Repeat(" ", indent)) + highlighted(bg) + bg.Render(strings.Repeat(" ", padWidth))
		}
		return strings.Repeat(" ", indent) + highlighted(lipgloss.NewStyle())
	}

	if isSelected {
		bg := lipgloss.NewStyle().Background(AvatarFillBg(avatarColorIdx))
		// "  " + bar = 3 cells; fill bg up to the title column, then pad to full width.
		fill := indent - 3
		if fill < 0 {
			fill = 0
		}
		padWidth := m.width - indent - lipgloss.Width(rest)
		if padWidth < 0 {
			padWidth = 0
		}
		return "  " + barSt.Render("▌") + bg.Render(strings.Repeat(" ", fill)) + highlighted(bg) + bg.Render(strings.Repeat(" ", padWidth))
	}

	// Unselected — preserve the auto-jump / trail bar at col 2 when present.
	fill := indent - 3
	if fill < 0 {
		fill = 0
	}
	if isAutoJump {
		return "  " + autoJumpBarSt.Render("▯") + strings.Repeat(" ", fill) + highlighted(lipgloss.NewStyle())
	}
	if isTrail {
		return "  " + trailBarSt.Render("▯") + strings.Repeat(" ", fill) + highlighted(lipgloss.NewStyle())
	}
	return strings.Repeat(" ", indent) + highlighted(lipgloss.NewStyle())
}

// renderBadges returns inline outcome indicators for a session entry.
// Returns empty string if no badges apply.
// transform, if non-nil, is applied to each badge's base style so callers can inject
// a row background (e.g. selection tint) without leaving transparent holes.
// query, if non-empty, highlights matched characters in the ProblemType badge.
func renderBadges(s claude.ClaudeSession, transform func(lipgloss.Style) lipgloss.Style, query string) string {
	applyTransform := func(st lipgloss.Style) lipgloss.Style {
		if transform != nil {
			return transform(st)
		}
		return st
	}
	var badges []string
	if s.LastActionCommit && s.Status == claude.StatusUserTurn {
		badges = append(badges, applyTransform(DiffAddedStyle).Render(IconGitCommit+" committed"))
	}
	// Skill badge: outcome indicator, shown after skill completes (user-turn).
	// Cleared on next non-skill prompt.
	if s.SkillName != "" && s.Status == claude.StatusUserTurn {
		badges = append(badges, applyTransform(DiffAddedStyle).Render(IconSkill+" "+skillBadgeLabel(s.SkillName)))
	}
	if s.StopReason != "" && s.Status == claude.StatusUserTurn {
		badges = append(badges, applyTransform(StatDoneStyle).Render(s.StopReason))
	}
	if s.ProblemType != "" {
		badges = append(badges, problemTypeBadge(s.ProblemType, query))
	}
	if s.CompactCount > 0 {
		badges = append(badges, applyTransform(ItemDetailStyle).Render(fmt.Sprintf("%s %d", IconCompact, s.CompactCount)))
	}
	for _, tag := range s.Tags {
		badges = append(badges, applyTransform(TagBadgeStyle).Render("#"+tag))
	}
	if len(badges) == 0 {
		return ""
	}
	// Style the separator so it inherits the row background between pre-rendered badge spans.
	// Without this, each badge's trailing reset (\x1b[0m) leaves the separating spaces transparent.
	sep := "  "
	if transform != nil {
		sep = transform(lipgloss.NewStyle()).Render("  ")
	}
	return strings.Join(badges, sep)
}

// skillBadgeLabel maps a raw skill command name to a human-friendly past-tense label.
var skillBadgeLabels = map[string]string{
	"simplify": "simplified",
	"review":   "reviewed",
}

func skillBadgeLabel(name string) string {
	if label, ok := skillBadgeLabels[name]; ok {
		return label
	}
	return name
}

// problemTypeBadge renders a color-coded pill for the synthesized problem type.
// query, if non-empty, highlights matched characters within the badge text.
func problemTypeBadge(pt, query string) string {
	var fg, bg lipgloss.AdaptiveColor
	switch pt {
	case "bug":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#fca5a5"}
		bg = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#450a0a"}
	case "debug":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#fdba74"}
		bg = lipgloss.AdaptiveColor{Light: "#ea580c", Dark: "#431407"}
	case "feature":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#6ee7b7"}
		bg = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#022c22"}
	case "refactoring":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#93c5fd"}
		bg = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#172554"}
	case "test":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#67e8f9"}
		bg = lipgloss.AdaptiveColor{Light: "#0891b2", Dark: "#083344"}
	case "docs":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#c4b5fd"}
		bg = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#2e1065"}
	case "exploration":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#d8b4fe"}
		bg = lipgloss.AdaptiveColor{Light: "#a855f7", Dark: "#3b0764"}
	case "performance":
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#fcd34d"}
		bg = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#422006"}
	default: // chore and unknown
		fg = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#d1d5db"}
		bg = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#1f2937"}
	}
	base := lipgloss.NewStyle().Foreground(fg).Background(bg)
	return base.Render(" ") + highlightMatch(pt, query, base) + base.Render(" ")
}

func (m SidebarModel) renderDetail(s claude.ClaudeSession, selected bool) string {
	bg := func(st lipgloss.Style) lipgloss.Style { return selBg(st, selected, s.AvatarColorIdx) }
	if s.CommitDonePending {
		return bg(CommitDoneStyle).Render(commitDoneFrames[m.commitDoneFrame])
	}
	// Waiting state: static icon (no spinner) — ball is in YOUR court
	if s.IsWaiting {
		return bg(StatWaitingStyle).Render(IconWaiting)
	}
	switch s.Status {
	case claude.StatusUserTurn:
		age := FormatAge(s.LastChanged)
		if s.LaterID != "" {
			if s.LaterWakeAt != nil {
				remaining := FormatCountdown(*s.LaterWakeAt)
				return bg(StatLaterStyle).Render(IconClock + " " + remaining)
			}
			return bg(StatLaterStyle).Render(IconLater + " " + age)
		}
		return bg(ItemDetailStyle).Render(age)
	case claude.StatusAgentTurn:
		if s.LaterID != "" {
			if s.LaterWakeAt != nil {
				remaining := FormatCountdown(*s.LaterWakeAt)
				return bg(StatLaterStyle).Render(IconClock + " " + remaining)
			}
			return bg(StatLaterStyle).Render(IconLater + " " + m.spinnerView)
		}
		if s.PermissionMode == "plan" {
			return bg(StatPlanStyle).Render(m.spinnerView)
		}
		return bg(StatWorkingStyle).Render(m.spinnerView)
	default:
		return ""
	}
}

// formatCompactDuration formats a positive duration compactly: "<1m", "5m", "2h", "3d".
// showSubHourMins includes the minute component for sub-day durations (e.g. "1h30m").
func formatCompactDuration(d time.Duration, showSubHourMins bool) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		if showSubHourMins {
			if m := int(d.Minutes()) % 60; m > 0 {
				return fmt.Sprintf("%dh%dm", h, m)
			}
		}
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// FormatAge returns a human-friendly age string like "<1m", "5m", "2h", "3d".
func FormatAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatCompactDuration(time.Since(t), false)
}

// FormatCountdown returns a compact countdown string like "4m", "1h30m", "2h".
// Returns "<1m" if the target is within a minute, "expired" if past.
func FormatCountdown(target time.Time) string {
	d := time.Until(target)
	if d <= 0 {
		return "expired"
	}
	return formatCompactDuration(d, true)
}

// itemLineCount returns the number of terminal lines a rendered item occupies.
// Must stay in sync with renderItem's subtitle appendages.
func (m SidebarModel) itemLineCount(s claude.ClaudeSession, query string) int {
	count := 1 // main line

	// Wrapped title second line (only when the last-message subtitle is hidden).
	if _, rest, _ := m.titleLayout(s, query); rest != "" {
		count++
	}

	if m.summaryLoadingPanes[s.PaneID] {
		count++
	}

	if len(s.QueuePending) > 0 {
		count++
	}

	if s.LastUserMessage != "" && (!m.hideLastMessage || query != "") {
		rawMsg := strings.ReplaceAll(s.LastUserMessage, "\n", " ")
		// subtitleMsgWidth returns identical width for selected/unselected
		msgWidth := m.subtitleMsgWidth(IconQuote, false)
		if msgWidth > 0 {
			_, rest := wordWrapFirst(rawMsg, msgWidth)
			if rest != "" {
				count += 2
			} else {
				count++
			}
		} else {
			count++
		}
	}

	hasQuery := query != ""
	if hasQuery {
		if s.SynthesizedTitle != "" && s.CustomTitle != "" && matchesNarrow(s.SynthesizedTitle, query) {
			count++
		}
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.SynthesizedTitle != "") && matchesNarrow(s.FirstMessage, query) {
			count++
		}
	}

	// Badges line: only adds a line when badges exist.
	// (Stats are pinned onto the last line by PinStatsRight, not emitted as a separate line.)
	if renderBadges(s, nil, "") != "" {
		count++
	}

	return count
}

// ComputeDiffColWidths exposes diff column width calculation for callers
// that render multiple cards (e.g. work queue) and want to compute once.
func (m SidebarModel) ComputeDiffColWidths() DiffColWidths {
	return m.computeDiffColWidths()
}

// BuildStatsRight computes the diff stats string for a session (file count, +/- lines,
// drift/overlap indicators). isSelected and colorIdx control selection background styling.
func (m *SidebarModel) BuildStatsRight(s claude.ClaudeSession, dw DiffColWidths, isSelected bool, colorIdx int) string {
	transform, sp := selectionFuncs(isSelected, colorIdx)
	if sp == nil {
		sp = func(s string) string { return s }
	}
	withBg := func(st lipgloss.Style) lipgloss.Style {
		if transform != nil {
			return transform(st)
		}
		return st
	}
	var statsRight string
	if s.TitleDrift {
		statsRight += sp(" ") + withBg(DriftStyle).Render(IconSynthTitle)
	}
	if s.HasOverlap {
		statsRight += sp(" ") + withBg(OverlapStyle).Render(IconOverlap)
	}
	if s.SessionID != "" {
		if stats, ok := m.diffStats[s.SessionID]; ok && len(stats) > 0 {
			totalAdded, totalRemoved := 0, 0
			for _, ds := range stats {
				totalAdded += ds.Added
				totalRemoved += ds.Removed
			}
			statsRight += sp(" ") +
				withBg(ItemDetailStyle).Render(fmt.Sprintf("%s %*d", IconDiff, dw.files, len(stats))) +
				sp(" ") + withBg(DiffAddedStyle).Render(fmt.Sprintf("+%-*d", dw.added, totalAdded)) +
				sp(" ") + withBg(StatWorkingStyle).Render(fmt.Sprintf("-%-*d", dw.removed, totalRemoved))
		}
	}
	return statsRight
}

// selectionFuncs returns a style transform and padding-space renderer for the
// selected item's avatar background. Both are nil when not selected.
func selectionFuncs(isSelected bool, colorIdx int) (func(lipgloss.Style) lipgloss.Style, func(string) string) {
	if !isSelected {
		return nil, nil
	}
	avatarBg := AvatarFillBg(colorIdx)
	bgSt := lipgloss.NewStyle().Background(avatarBg)
	transform := func(st lipgloss.Style) lipgloss.Style { return st.Background(avatarBg) }
	padSp := func(s string) string { return bgSt.Render(s) }
	return transform, padSp
}

// PinStatsRight overlays statsRight on the bottom-right of the last line of content.
// padSp renders padding spaces (with optional background styling).
func PinStatsRight(content, statsRight string, width int, padSp func(string) string) string {
	if statsRight == "" {
		return content
	}
	if padSp == nil {
		padSp = func(s string) string { return s }
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return content
	}
	last := lines[len(lines)-1]
	statsW := lipgloss.Width(statsRight)
	lastW := lipgloss.Width(last)
	targetW := width
	if lastW <= targetW-statsW {
		gap := targetW - lastW - statsW
		lines[len(lines)-1] = last + padSp(strings.Repeat(" ", gap)) + statsRight
	} else {
		trimmed := ansi.Truncate(last, max(targetW-statsW-1, 0), "")
		trimmedW := lipgloss.Width(trimmed)
		gap := targetW - trimmedW - statsW
		if gap < 0 {
			gap = 0
		}
		lines[len(lines)-1] = trimmed + padSp(strings.Repeat(" ", gap)) + statsRight
	}
	return strings.Join(lines, "\n")
}

// RenderCard renders a single session item at the given width, padded or truncated
// to exactly maxLines lines. Reuses renderItem internally. Used by the work queue
// to render cards at a different width than the sidebar.
func (m *SidebarModel) RenderCard(cardWidth, maxLines int, isSelected, isAutoJump bool, s claude.ClaudeSession, dw DiffColWidths) string {
	origWidth := m.width
	m.width = cardWidth
	m.cardMode = true
	result := m.renderItem(isSelected, isAutoJump, s, "")
	m.cardMode = false
	m.width = origWidth

	// Pad or truncate to exactly maxLines
	lines := strings.Split(result, "\n")
	for len(lines) < maxLines {
		lines = append(lines, strings.Repeat(" ", cardWidth))
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	result = strings.Join(lines, "\n")

	// Pin stats on the bottom-right — selection-aware
	_, padSp := selectionFuncs(isSelected, s.AvatarColorIdx)
	statsRight := m.BuildStatsRight(s, dw, isSelected, s.AvatarColorIdx)
	out := PinStatsRight(result, statsRight, cardWidth, padSp)
	// Card mode has no slot/flag prefix — selection background starts at col 0.
	return m.applyRevealWave(out, s.PaneID, s.AvatarColorIdx, 0, cardWidth)
}
