package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// View and rendering methods for SidebarModel.

func (m *SidebarModel) View() string {
	if len(m.items) == 0 {
		return EmptyStyle.Width(m.width).Render("No Claude sessions found\n\nStart Claude in a tmux pane to see it here.")
	}

	dw := m.computeDiffColWidths()
	query := strings.ToLower(m.narrow)

	// Determine selected PaneID for cursor tracking across the full list
	var selectedPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		selectedPaneID = m.filtered[m.cursor].PaneID
	}

	// Pre-calculate auto-jump target
	autoJumpTargetID := m.AutoJumpTargetFromCursor()

	// Project-level selection
	selectedProject, atProjectLevel := m.SelectedProject()

	var lines []string
	m.selectedProjectRow = -1
	m.selectedItemRow = -1

	if m.narrow != "" {
		// Search mode: render from m.filtered directly (score-sorted, flat)
		for _, s := range m.filtered {
			isSelected := s.PaneID == selectedPaneID && !m.deselected
			isAutoJump := !isSelected && s.PaneID == autoJumpTargetID
			lines = append(lines, m.renderItem(isSelected, isAutoJump, s, dw, query))
		}
	} else {
		currentProject := ""
		currentOrder := -1

		for _, s := range m.allSorted {
			// When Clauding is collapsed, skip Clauding items entirely (header rendered at bottom)
			if !m.claudingExpanded && sessionOrder(s) == OrderAgentTurn {
				continue
			}
			// When Later is collapsed, skip Later items entirely (header rendered at bottom)
			if !m.laterExpanded && sessionOrder(s) == OrderLater {
				continue
			}
			// Group headers — always rendered for spatial stability during narrowing
			if m.groupByProject {
				order := sessionOrder(s)
				// Emit LATER header when entering the Later zone
				if order == OrderLater && currentOrder != OrderLater {
					currentOrder = OrderLater
					currentProject = "" // reset to force project sub-header
					if len(lines) > 0 {
						lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
					}
					lines = append(lines, renderStatusGroupHeader(OrderLater))
				}
				if s.Project != currentProject {
					currentProject = s.Project
					if currentOrder == OrderLater {
						// Project sub-header within Later section
						if atProjectLevel && currentProject == selectedProject.Name && selectedProject.StatusOrder == OrderLater {
							m.selectedProjectRow = len(lines)
							lines = append(lines, renderSelectedProjectHeader(s.Project, m.width))
						} else {
							lines = append(lines, renderProjectSubHeader(s.Project))
						}
					} else {
						if len(lines) > 0 {
							lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
						}
						if atProjectLevel && currentProject == selectedProject.Name && selectedProject.StatusOrder == -1 {
							m.selectedProjectRow = len(lines)
							lines = append(lines, renderSelectedProjectHeader(s.Project, m.width))
						} else {
							lines = append(lines, renderGroupHeader(s.Project))
						}
					}
				}
			} else {
				order := sessionOrder(s)
				if order != currentOrder {
					currentOrder = order
					currentProject = "" // reset project tracking for new status group
					if len(lines) > 0 {
						lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
					}
					lines = append(lines, renderStatusGroupHeader(order))
				}
				if s.Project != currentProject {
					currentProject = s.Project
					if atProjectLevel && currentProject == selectedProject.Name && currentOrder == selectedProject.StatusOrder {
						m.selectedProjectRow = len(lines)
						lines = append(lines, renderSelectedProjectHeader(s.Project, m.width))
					} else {
						lines = append(lines, renderProjectSubHeader(s.Project))
					}
				}
			}

			// Only render items that match the narrow (matchSet nil = all match)
			if m.matchSet != nil && !m.matchSet[s.PaneID] {
				continue
			}

			isSelected := s.PaneID == selectedPaneID && !m.deselected && !atProjectLevel
			isAutoJump := !isSelected && s.PaneID == autoJumpTargetID
			if isSelected {
				m.selectedItemRow = len(lines)
			}
			lines = append(lines, m.renderItem(isSelected, isAutoJump, s, dw, query))
		}
	}

	// Render backlog section (after sessions)
	if len(m.filteredBacklog) > 0 {
		if len(lines) > 0 {
			lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
		}
		lines = append(lines, renderStatusGroupHeader(OrderBacklog))

		currentBacklogProject := ""
		for i, backlog := range m.filteredBacklog {
			if backlog.Project != currentBacklogProject {
				currentBacklogProject = backlog.Project
				backlogPE := projectEntry{Name: backlog.Project, StatusOrder: OrderBacklog}
				if atProjectLevel && selectedProject == backlogPE {
					m.selectedProjectRow = len(lines)
					lines = append(lines, renderSelectedProjectHeader(backlog.Project, m.width))
				} else {
					lines = append(lines, renderProjectSubHeader(backlog.Project))
				}
			}
			backlogCursor := len(m.filtered) + i
			isSelected := backlogCursor == m.cursor && !m.deselected && !atProjectLevel
			lines = append(lines, m.renderBacklogItem(isSelected, backlog))
		}
	}

	// Pin collapsed section badges at the bottom (clauding, later, and/or backlog when hidden)
	claudingCount := m.claudingCount // cached by applyNarrow; non-zero only when !claudingExpanded
	laterCount := m.laterCount       // cached by applyNarrow; non-zero only when !laterExpanded
	backlogCount := 0
	if !m.backlogExpanded {
		backlogCount = len(m.backlogs)
	}
	if (claudingCount > 0 || laterCount > 0 || backlogCount > 0) && m.height > 0 {
		var parts []string
		if claudingCount > 0 {
			parts = append(parts, GroupHeaderWorkingStyle.Render(fmt.Sprintf("%s CLAUDING (%d)", IconBolt, claudingCount)))
		}
		if laterCount > 0 {
			parts = append(parts, GroupHeaderLaterStyle.Render(fmt.Sprintf("%s LATER (%d)", IconBookmark, laterCount)))
		}
		if backlogCount > 0 {
			parts = append(parts, GroupHeaderBacklogStyle.Render(fmt.Sprintf("%s BACKLOG (%d)", IconBacklog, backlogCount)))
		}
		separator := SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width))
		collapsedHeader := strings.Join(parts, " ")
		needed := 2 // separator + header line
		visualLines := 0
		for _, line := range lines {
			visualLines += lipgloss.Height(line)
		}
		for visualLines < m.height-needed {
			lines = append(lines, "")
			visualLines++
		}
		lines = append(lines, separator, collapsedHeader)
	}

	// Truncate to fit available height
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}

	return strings.Join(lines, "\n")
}

func renderGroupHeader(project string) string {
	return GroupHeaderProjectStyle.Render(IconFolder + " " + project)
}

// selectedProjectHeaderStyle is the highlight style for project headers at project-level nav.
var selectedProjectHeaderStyle = GroupHeaderStyle.
	Foreground(ColorAccent).
	Background(ColorSelectionBg)

func renderSelectedProjectHeader(project string, width int) string {
	return selectedProjectHeaderStyle.Width(width).Render(IconFolder + " " + project)
}

func renderProjectSubHeader(project string) string {
	return ProjectSubHeaderStyle.Render(IconFolder + " " + project)
}

func renderStatusGroupHeader(order int) string {
	switch order {
	case OrderUserTurn:
		return GroupHeaderDoneStyle.Render(IconFlag + " YOUR TURN")
	case OrderAgentTurn:
		return GroupHeaderWorkingStyle.Render(IconBolt + " CLAUDING")
	case OrderLater:
		return GroupHeaderLaterStyle.Render(IconBookmark + " LATER")
	case OrderBacklog:
		return GroupHeaderBacklogStyle.Render(IconBacklog + " BACKLOG")
	default:
		return ""
	}
}

func (m SidebarModel) renderItem(isSelected, isAutoJump bool, s claude.ClaudeSession, dw diffColWidths, query string) string {

	// Display name priority: custom title → headline → first message → (new session)
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
	isLanding := isSelected && s.PaneID == m.landPaneID && m.landFrame < jumpAnimFrames
	isTrail := !isSelected && !isAutoJump && s.PaneID == m.trailPaneID && m.trailFrame < jumpAnimFrames
	var selBgSt, barSt, autoJumpBarSt, trailBarSt lipgloss.Style
	if isSelected {
		selBgSt = lipgloss.NewStyle().Background(avatarBg)
		var barColor lipgloss.TerminalColor = avatarColor
		if isLanding {
			t := float64(m.landFrame) / float64(jumpAnimFrames-1)
			barColor = lipgloss.Color(blendHex("#ffffff", avatarHex, t))
		}
		barSt = lipgloss.NewStyle().Foreground(barColor).Background(avatarBg)
	} else if isAutoJump {
		autoJumpBarSt = lipgloss.NewStyle().Foreground(avatarColor)
	} else if isTrail {
		t := float64(m.trailFrame) / float64(jumpAnimFrames-1)
		trailBarSt = lipgloss.NewStyle().Foreground(lipgloss.Color(blendHex(avatarHex, "#333333", t)))
	}

	withBg := func(st lipgloss.Style) lipgloss.Style { return selBg(st, isSelected, s.AvatarColorIdx) }
	sp := func(s string) string {
		if isSelected {
			return selBgSt.Render(s)
		}
		return s
	}

	// Build right-side (detail + diff stats) — styles include bg when selected
	detail := m.renderDetail(s, isSelected)
	right := detail
	if s.SessionID != "" {
		if stats, ok := m.diffStats[s.SessionID]; ok && len(stats) > 0 {
			totalAdded, totalRemoved := 0, 0
			for _, ds := range stats {
				totalAdded += ds.Added
				totalRemoved += ds.Removed
			}
			diffPart := sp("  ") +
				withBg(ItemDetailStyle).Render(fmt.Sprintf("%s %*d", IconFile, dw.files, len(stats))) +
				sp(" ") + withBg(DiffAddedStyle).Render(fmt.Sprintf("+%-*d", dw.added, totalAdded)) +
				sp(" ") + withBg(StatWorkingStyle).Render(fmt.Sprintf("-%-*d", dw.removed, totalRemoved))
			right = detail + diffPart
		} else if dw.files > 0 {
			// Pad so the spinner stays in the same column as items that have diff stats
			diffPartWidth := 2 + lipgloss.Width(fmt.Sprintf("%s %*d", IconFile, dw.files, 0)) +
				1 + len(fmt.Sprintf("+%-*d", dw.added, 0)) +
				1 + len(fmt.Sprintf("-%-*d", dw.removed, 0))
			right = detail + sp(strings.Repeat(" ", diffPartWidth))
		}
	}
	rightWidth := lipgloss.Width(right)

	// prefix is always 4 cells: "  ▌ " (selected) or "    " (unselected)
	const prefixWidth = 4
	var worktreeIcon string
	if s.IsWorktree {
		worktreeIcon = worktreeIconRendered
	}
	iconStr := AvatarStyle(s.AvatarColorIdx).Render(glyph+"  ") + worktreeIcon
	iconWidth := lipgloss.Width(iconStr)

	// 2 for outer padding, 2 for minimum gap
	maxNameWidth := m.width - prefixWidth - iconWidth - rightWidth - 4
	if maxNameWidth < 4 {
		maxNameWidth = 4
	}
	if lipgloss.Width(displayName) > maxNameWidth {
		displayName = ansi.Truncate(displayName, maxNameWidth, "…")
	}

	// Geometric gap — computed once before styling branches to prevent ANSI width drift
	displayNameWidth := lipgloss.Width(displayName)
	gap := m.width - prefixWidth - iconWidth - displayNameWidth - rightWidth - 2
	if gap < 1 {
		gap = 1
	}

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
		namePart = bg.Render("  ") +
			barSt.Render("▌") +
			bg.Render(" ") +
			AvatarStyle(s.AvatarColorIdx).Background(avatarBg).Render(glyph+"  ") +
			selWorktreeIcon +
			styledName
		gapStr = bg.Render(strings.Repeat(" ", gap))
	} else {
		var styledName string
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, lipgloss.NewStyle())
		} else {
			styledName = displayName
		}
		if isAutoJump {
			namePart = "  " + autoJumpBarSt.Render("▯") + " " + iconStr + styledName
		} else if isTrail {
			namePart = "  " + trailBarSt.Render("▯") + " " + iconStr + styledName
		} else {
			namePart = "    " + iconStr + styledName
		}
		gapStr = strings.Repeat(" ", gap)
	}

	line := namePart + gapStr + right

	// selSubtitle wraps a subtitle content string with the selection bar at col 2.
	// bar(1) + sp("  ")(2) + content(m.width-5) = m.width-2 total, matching the main line.
	selSubtitle := func(style lipgloss.Style, content string) string {
		return sp("  ") + barSt.Render("▌") +
			withBg(style).Width(m.width-5).Render(content)
	}

	// autoJumpSubtitle wraps an unselected subtitle with the auto-jump bar at col 2.
	// Visually: "  │" + content — same width as "      " (6 spaces).
	autoJumpSubtitle := func(style lipgloss.Style, content string) string {
		return "  " + autoJumpBarSt.Render("▯") + style.Render("   "+content)
	}

	if m.summaryLoadingPanes[s.PaneID] {
		if isSelected {
			line += "\n" + selSubtitle(selBgSt.Foreground(ColorMuted).Italic(true), "   "+m.spinnerView+" synthesizing…")
		} else if isAutoJump {
			line += "\n" + autoJumpSubtitle(SummaryStyle, m.spinnerView+" synthesizing…")
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
		// Headline: shown when it's not the display name (i.e. customTitle is set) and matches
		if s.Headline != "" && s.CustomTitle != "" && matchesNarrow(s.Headline, query) {
			line += "\n" + m.renderSubtitleLine(s.Headline, query, IconHeadline, isSelected, isAutoJump, true, s.AvatarColorIdx, barSt)
		}
		// FirstMessage: shown when it's not the display name (customTitle or headline is set) and matches
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.Headline != "") && matchesNarrow(s.FirstMessage, query) {
			rawFirst := strings.ReplaceAll(s.FirstMessage, "\n", " ")
			line += "\n" + m.renderSubtitleLine(rawFirst, query, IconQuote, isSelected, isAutoJump, true, s.AvatarColorIdx, barSt)
		}
	}

	// Badges line — outcome indicators (git commit, etc.) + optional inline tag input.
	// Pass withBg so each badge's style gets the row background, avoiding transparent holes.
	badges := renderBadges(s, withBg, query)
	showTagInput := isSelected && m.inlineTagSessionID == s.SessionID && m.inlineTagInputView != ""
	if badges != "" || showTagInput {
		if isSelected {
			if showTagInput {
				// Tag input is active: render badges with background + append raw input view.
				// Don't use fixed width so the cursor isn't clipped by lipgloss padding.
				sep := ""
				if badges != "" {
					sep = "  "
				}
				line += "\n" + sp("  ") + barSt.Render("▌") +
					withBg(ItemDetailStyle).Render("   "+badges+sep) + m.inlineTagInputView
			} else {
				line += "\n" + selSubtitle(ItemDetailStyle, "   "+badges)
			}
		} else if isAutoJump {
			line += "\n" + autoJumpSubtitle(ItemDetailStyle, badges)
		} else {
			line += "\n" + ItemDetailStyle.Render("      "+badges)
		}
	}

	return line
}

// renderBacklogItem renders a single backlog entry in the list.
func (m SidebarModel) renderBacklogItem(isSelected bool, backlog claude.Backlog) string {
	title := backlog.DisplayTitle()
	title = strings.ReplaceAll(title, "\n", " ")

	age := FormatAge(backlog.UpdatedAt)

	const prefixWidth = 4
	iconStr := ItemDetailStyle.Render(IconBacklog + "  ")
	iconWidth := lipgloss.Width(iconStr)
	ageStr := ItemDetailStyle.Render(age)
	ageWidth := lipgloss.Width(ageStr)

	maxNameWidth := m.width - prefixWidth - iconWidth - ageWidth - 4
	if maxNameWidth < 4 {
		maxNameWidth = 4
	}
	if lipgloss.Width(title) > maxNameWidth {
		title = ansi.Truncate(title, maxNameWidth, "…")
	}

	titleWidth := lipgloss.Width(title)
	gap := m.width - prefixWidth - iconWidth - titleWidth - ageWidth - 2
	if gap < 1 {
		gap = 1
	}

	if isSelected {
		bg := lipgloss.NewStyle().Background(ColorSelectionBg)
		return bg.Render("  ▌ ") +
			bg.Render(IconBacklog+"  ") +
			bg.Render(title) +
			bg.Render(strings.Repeat(" ", gap)) +
			bg.Render(age)
	}

	return "    " + iconStr + title + strings.Repeat(" ", gap) + ageStr
}

// subtitleMsgWidth returns the available text width for a subtitle line with the given icon.
func (m SidebarModel) subtitleMsgWidth(icon string, isSelected bool) int {
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
		return bgStyle.Render("  ") + barSt.Render("▌") + content + bgStyle.Render(strings.Repeat(" ", padWidth))
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

// hasBadges returns true if the session has any outcome badges to display.
// Delegates to renderBadges to avoid condition drift between the two.
func hasBadges(s claude.ClaudeSession) bool {
	return renderBadges(s, nil, "") != ""
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
	if s.HasOverlap {
		badges = append(badges, applyTransform(OverlapStyle).Render(IconOverlap))
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
		if s.LaterBookmarkID != "" && s.IsPhantom {
			return bg(StatLaterStyle).Render(IconBookmark + " " + age)
		}
		return bg(ItemDetailStyle).Render(age)
	case claude.StatusAgentTurn:
		if s.LaterBookmarkID != "" {
			return bg(StatLaterStyle).Render(IconBookmark + " " + m.spinnerView)
		}
		if s.PermissionMode == "plan" {
			return bg(StatPlanStyle).Render(m.spinnerView)
		}
		return bg(StatWorkingStyle).Render(m.spinnerView)
	default:
		return ""
	}
}

// FormatAge returns a human-friendly age string like "<1m", "5m", "2h", "3d".
func FormatAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// PaneIDAtLine maps a terminal line index (relative to list content start) to
// the PaneID of the session rendered at that line. Returns "" for headers,
// separators, or out-of-bounds lines. Mirrors View()'s group/filter/item logic.
func (m SidebarModel) PaneIDAtLine(line int) string {
	if line < 0 || len(m.allSorted) == 0 {
		return ""
	}

	query := strings.ToLower(m.narrow)
	currentLine := 0

	if m.narrow != "" {
		// Search mode: flat list from m.filtered (score-sorted)
		for _, s := range m.filtered {
			lineCount := m.itemLineCount(s, query)
			if line >= currentLine && line < currentLine+lineCount {
				return s.PaneID
			}
			currentLine += lineCount
		}
		return ""
	}

	currentProject := ""
	currentOrder := -1
	anyLinesEmitted := false

	for _, s := range m.allSorted {
		// Group headers — must mirror View()'s logic exactly
		if m.groupByProject {
			if s.Project != currentProject {
				currentProject = s.Project
				if anyLinesEmitted {
					currentLine++ // separator
				}
				anyLinesEmitted = true
				currentLine++ // group header
			}
		} else {
			order := sessionOrder(s)
			if order != currentOrder {
				currentOrder = order
				currentProject = ""
				if anyLinesEmitted {
					currentLine++ // separator
				}
				anyLinesEmitted = true
				currentLine++ // status group header
			}
			if s.Project != currentProject {
				currentProject = s.Project
				currentLine++ // project sub-header
			}
		}

		// Skip non-matching items (same as View)
		if m.matchSet != nil && !m.matchSet[s.PaneID] {
			continue
		}

		lineCount := m.itemLineCount(s, query)
		if line >= currentLine && line < currentLine+lineCount {
			return s.PaneID
		}
		currentLine += lineCount
	}

	return ""
}

// BacklogIDAtLine returns the ID of the backlog item at the given display line,
// or "" if the line is a header, separator, or session line.
// Mirrors the rendering logic of View() for the backlog section.
func (m SidebarModel) BacklogIDAtLine(line int) string {
	if line < 0 || len(m.filteredBacklog) == 0 {
		return ""
	}

	query := strings.ToLower(m.narrow)
	currentLine := 0

	// Count all session lines (mirrors PaneIDAtLine's counting, runs the full loop).
	if m.narrow != "" {
		for _, s := range m.filtered {
			currentLine += m.itemLineCount(s, query)
		}
	} else {
		currentProject := ""
		currentOrder := -1
		anyLinesEmitted := false

		for _, s := range m.allSorted {
			if m.groupByProject {
				if s.Project != currentProject {
					currentProject = s.Project
					if anyLinesEmitted {
						currentLine++ // separator
					}
					anyLinesEmitted = true
					currentLine++ // group header
				}
			} else {
				order := sessionOrder(s)
				if order != currentOrder {
					currentOrder = order
					currentProject = ""
					if anyLinesEmitted {
						currentLine++ // separator
					}
					anyLinesEmitted = true
					currentLine++ // status group header
				}
				if s.Project != currentProject {
					currentProject = s.Project
					currentLine++ // project sub-header
				}
			}
			if m.matchSet != nil && !m.matchSet[s.PaneID] {
				continue
			}
			currentLine += m.itemLineCount(s, query)
		}
	}

	// Backlog section: separator (if sessions exist) + group header.
	if currentLine > 0 {
		currentLine++ // "─────" separator
	}
	currentLine++ // "BACKLOG" group header

	// Walk backlog items (each project gets a 1-line sub-header, each item is 1 line).
	currentBacklogProject := ""
	for _, backlog := range m.filteredBacklog {
		if backlog.Project != currentBacklogProject {
			currentBacklogProject = backlog.Project
			currentLine++ // project sub-header (or selected project header — same 1 line)
		}
		if line == currentLine {
			return backlog.ID
		}
		currentLine++
	}
	return ""
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
		if s.Headline != "" && s.CustomTitle != "" && matchesNarrow(s.Headline, query) {
			count++
		}
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.Headline != "") && matchesNarrow(s.FirstMessage, query) {
			count++
		}
	}

	if hasBadges(s) {
		count++
	}

	return count
}
