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
	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (m SidebarModel) renderItem(isSelected, isAutoJump bool, s claude.ClaudeSession, dw DiffColWidths, query string) string {

	// Display name priority: custom title → synthesized title → first message → (new session)
	displayName := s.DisplayName()
	isNewSession := displayName == ""
	if isNewSession {
		displayName = lipgloss.NewStyle().Italic(true).Render("(New session)")
	} else {
		displayName = strings.ReplaceAll(displayName, "\n", " ")
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
	sp := func(s string) string {
		if isSelected {
			return selBgSt.Render(s)
		}
		return s
	}

	// Detail (spinner/age) stays on line 1; drift, overlap, diff stats move to stats line
	detail := m.renderDetail(s, isSelected)
	detailWidth := lipgloss.Width(detail)

	// Build stats-right content (drift + overlap + diff stats) for the badges line
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
				withBg(ItemDetailStyle).Render(fmt.Sprintf("%s %*d", IconFile, dw.files, len(stats))) +
				sp(" ") + withBg(DiffAddedStyle).Render(fmt.Sprintf("+%-*d", dw.added, totalAdded)) +
				sp(" ") + withBg(StatWorkingStyle).Render(fmt.Sprintf("-%-*d", dw.removed, totalRemoved))
		}
	}
	statsRightWidth := lipgloss.Width(statsRight)

	// prefix: 4 cells in sidebar (slot+flag+bar+space), 1 cell in card mode (space only)
	prefixWidth := 4
	if m.cardMode {
		prefixWidth = 1
	}
	var worktreeIcon string
	if s.IsWorktree {
		worktreeIcon = worktreeIconRendered
	}
	iconStr := AvatarStyle(s.AvatarColorIdx).Render(glyph+"  ") + worktreeIcon
	iconWidth := lipgloss.Width(iconStr)

	// 2 for outer padding, 2 for minimum gap
	maxNameWidth := m.width - prefixWidth - iconWidth - detailWidth - 4
	if maxNameWidth < 4 {
		maxNameWidth = 4
	}
	if lipgloss.Width(displayName) > maxNameWidth {
		displayName = ansi.Truncate(displayName, maxNameWidth, "…")
	}

	// Geometric gap — computed once before styling branches to prevent ANSI width drift
	displayNameWidth := lipgloss.Width(displayName)
	gap := m.width - prefixWidth - iconWidth - displayNameWidth - detailWidth - 2
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
		var selWorktreeIcon string
		if s.IsWorktree {
			selWorktreeIcon = worktreeIconStyle.Background(avatarBg).Render(IconWorktree) + bg.Render(" ")
		}
		if m.cardMode {
			namePart = bg.Render(" ") +
				AvatarStyle(s.AvatarColorIdx).Background(avatarBg).Render(glyph+"  ") +
				selWorktreeIcon +
				styledName
		} else {
			namePart = slotGlyph + flagGlyph +
				barSt.Render("▌") +
				bg.Render(" ") +
				AvatarStyle(s.AvatarColorIdx).Background(avatarBg).Render(glyph+"  ") +
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
	if s.LastUserMessage != "" {
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

	// Badges + stats placement.
	// Strategy: place statsRight on the bottom-right of the item, sharing a line
	// with existing content (badges or last subtitle) when possible.
	badges := renderBadges(s, withBg, query)
	showTagInput := isSelected && m.inlineTagSessionID == s.SessionID && m.inlineTagInputView != ""
	hasBadgesLine := badges != "" || showTagInput

	if showTagInput {
		sep := ""
		if badges != "" {
			sep = "  "
		}
		if m.cardMode {
			line += "\n" + withBg(ItemDetailStyle).Render(" "+badges+sep) + m.inlineTagInputView
		} else {
			line += "\n" + "  " + barSt.Render("▌") +
				withBg(ItemDetailStyle).Render("   "+badges+sep) + m.inlineTagInputView
		}
	}

	if hasBadgesLine {
		// Dedicated badges+stats line (badges left, statsRight right-aligned)
		leftContent := badges
		if showTagInput {
			leftContent = ""
		}
		m.renderStatsLine(&line, leftContent, statsRight, statsRightWidth, isSelected, isAutoJump, sp, barSt, autoJumpBarSt, withBg)
	} else if statsRightWidth > 0 {
		// No badges — attach statsRight to the bottom-right of the last content line
		if idx := strings.LastIndex(line, "\n"); idx >= 0 {
			// Subtitle lines exist — piggyback on the last one
			lastLine := line[idx+1:]
			rest := line[:idx+1]
			targetW := m.width - 2
			trimmed := ansi.Truncate(lastLine, targetW-statsRightWidth, "")
			trimmedW := lipgloss.Width(trimmed)
			statsGap := targetW - trimmedW - statsRightWidth
			if statsGap < 0 {
				statsGap = 0
			}
			line = rest + trimmed + sp(strings.Repeat(" ", statsGap)) + statsRight
		} else {
			// No subtitle lines — render stats-only line
			m.renderStatsLine(&line, "", statsRight, statsRightWidth, isSelected, isAutoJump, sp, barSt, autoJumpBarSt, withBg)
		}
	}

	return line
}

// renderStatsLine appends a new line with leftContent left-aligned and statsRight right-aligned.
func (m SidebarModel) renderStatsLine(line *string, leftContent, statsRight string, statsRightWidth int, isSelected, isAutoJump bool, sp func(string) string, barSt, autoJumpBarSt lipgloss.Style, withBg func(lipgloss.Style) lipgloss.Style) {
	if m.cardMode {
		leftStr := " " + leftContent
		leftWidth := lipgloss.Width(leftStr)
		statsGap := m.width - 1 - leftWidth - statsRightWidth
		if statsGap < 0 {
			statsGap = 0
		}
		if isSelected {
			*line += "\n" + withBg(ItemDetailStyle).Render(leftStr) +
				sp(strings.Repeat(" ", statsGap)) +
				statsRight
		} else {
			*line += "\n" + ItemDetailStyle.Render(leftStr) +
				strings.Repeat(" ", statsGap) +
				statsRight
		}
		return
	}
	if isSelected {
		leftStr := "   " + leftContent
		leftWidth := lipgloss.Width(leftStr)
		innerWidth := m.width - 5 // content area after "  ▌"
		statsGap := innerWidth - leftWidth - statsRightWidth
		if statsGap < 0 {
			statsGap = 0
		}
		*line += "\n" + "  " + barSt.Render("▌") +
			withBg(ItemDetailStyle).Render(leftStr) +
			sp(strings.Repeat(" ", statsGap)) +
			statsRight
	} else if isAutoJump {
		leftStr := "   " + leftContent
		leftWidth := lipgloss.Width(leftStr)
		statsGap := m.width - 5 - leftWidth - statsRightWidth
		if statsGap < 0 {
			statsGap = 0
		}
		*line += "\n" + "  " + autoJumpBarSt.Render("▯") +
			ItemDetailStyle.Render(leftStr) +
			strings.Repeat(" ", statsGap) +
			statsRight
	} else {
		leftStr := "      " + leftContent
		leftWidth := lipgloss.Width(leftStr)
		statsGap := m.width - 2 - leftWidth - statsRightWidth
		if statsGap < 0 {
			statsGap = 0
		}
		*line += "\n" + ItemDetailStyle.Render(leftStr) +
			strings.Repeat(" ", statsGap) +
			statsRight
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

	if m.summaryLoadingPanes[s.PaneID] {
		count++
	}

	if len(s.QueuePending) > 0 {
		count++
	}

	if s.LastUserMessage != "" {
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

	// Stats line: only adds a line when badges exist, or when there are stats
	// but no subtitle lines to piggyback on.
	hasBadges := renderBadges(s, nil, "") != ""
	hasStats := s.TitleDrift || s.HasOverlap ||
		(s.SessionID != "" && len(m.diffStats[s.SessionID]) > 0)
	hasSubtitles := count > 1

	if hasBadges {
		count++ // dedicated badges+stats line
	} else if hasStats && !hasSubtitles {
		count++ // stats-only line (no subtitle to attach to)
	}

	return count
}

// ComputeDiffColWidths exposes diff column width calculation for callers
// that render multiple cards (e.g. work queue) and want to compute once.
func (m SidebarModel) ComputeDiffColWidths() DiffColWidths {
	return m.computeDiffColWidths()
}

// RenderCard renders a single session item at the given width, padded or truncated
// to exactly maxLines lines. Reuses renderItem internally. Used by the work queue
// to render cards at a different width than the sidebar.
func (m *SidebarModel) RenderCard(cardWidth, maxLines int, isSelected, isAutoJump bool, s claude.ClaudeSession, dw DiffColWidths) string {
	origWidth := m.width
	m.width = cardWidth
	m.cardMode = true
	result := m.renderItem(isSelected, isAutoJump, s, dw, "")
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
	return strings.Join(lines, "\n")
}
