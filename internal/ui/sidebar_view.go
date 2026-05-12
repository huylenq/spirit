package ui

// Sidebar list view — groups, headers, backlog, and click hit-testing.
// Session item rendering (renderItem, badges, subtitles, etc.) lives in session_item.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

// renderItemWithStats renders a session item and pins diff stats on the bottom-right.
func (m *SidebarModel) renderItemWithStats(isSelected, isAutoJump bool, s claude.ClaudeSession, dw DiffColWidths, query string) string {
	content := m.renderItem(isSelected, isAutoJump, s, query)
	_, padSp := selectionFuncs(isSelected, s.AvatarColorIdx)
	if isSelected {
		// Tint renderItem's trailing breathing-room buffer so the selection
		// highlight reaches the right edge on every row of the item.
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			lineW := lipgloss.Width(line)
			if lineW < m.width {
				lines[i] = line + padSp(strings.Repeat(" ", m.width-lineW))
			}
		}
		content = strings.Join(lines, "\n")
	}
	statsRight := m.BuildStatsRight(s, dw, isSelected, s.AvatarColorIdx)
	return PinStatsRight(content, statsRight, m.width, padSp)
}

func (m *SidebarModel) View() string {
	if len(m.items) == 0 {
		return EmptyStyle.Width(m.width).Render("No Claude sessions found\n\nStart Claude in a tmux pane to see it here.")
	}

	dw := m.computeDiffColWidths()
	query := m.searchTextQuery()

	// Determine selected PaneID for cursor tracking across the full list
	var selectedPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		selectedPaneID = m.filtered[m.cursor].PaneID
	}

	// Pre-calculate auto-jump target
	autoJumpTargetID := ""
	if m.ShowAutoJump {
		autoJumpTargetID = m.AutoJumpTargetFromCursor()
	}

	// Project-level selection
	selectedProject, atProjectLevel := m.SelectedProject()

	var lines []string
	m.selectedProjectRow = -1
	m.selectedItemRow = -1

	if query != "" {
		// Search mode: render from m.filtered directly (score-sorted, flat)
		for _, s := range m.filtered {
			isSelected := s.PaneID == selectedPaneID && !m.deselected
			isAutoJump := !isSelected && s.PaneID == autoJumpTargetID
			lines = append(lines, m.renderItemWithStats(isSelected, isAutoJump, s, dw, query))
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
			// Focus mode: skip unflagged sessions
			if m.focusMode && !m.IsEffectivelyFlagged(s) {
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
							lines = append(lines, renderSelectedProjectHeader(s.Project, m.width, m.flaggedProjects[s.Project]))
						} else {
							lines = append(lines, renderProjectSubHeader(s.Project, m.flaggedProjects[s.Project]))
						}
					} else {
						if len(lines) > 0 {
							lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
						}
						if atProjectLevel && currentProject == selectedProject.Name && selectedProject.StatusOrder == -1 {
							m.selectedProjectRow = len(lines)
							lines = append(lines, renderSelectedProjectHeader(s.Project, m.width, m.flaggedProjects[s.Project]))
						} else {
							lines = append(lines, renderGroupHeader(s.Project, m.flaggedProjects[s.Project]))
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
						lines = append(lines, renderSelectedProjectHeader(s.Project, m.width, m.flaggedProjects[s.Project]))
					} else {
						lines = append(lines, renderProjectSubHeader(s.Project, m.flaggedProjects[s.Project]))
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
			lines = append(lines, m.renderItemWithStats(isSelected, isAutoJump, s, dw, query))
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
					lines = append(lines, renderSelectedProjectHeader(backlog.Project, m.width, m.flaggedProjects[backlog.Project]))
				} else {
					lines = append(lines, renderProjectSubHeader(backlog.Project, m.flaggedProjects[backlog.Project]))
				}
			}
			backlogCursor := len(m.filtered) + i
			isSelected := backlogCursor == m.cursor && !m.deselected && !atProjectLevel
			lines = append(lines, m.renderBacklogItem(isSelected, backlog))
		}
	}

	// Skeleton placeholder when all sections are collapsed
	if len(lines) == 0 && m.IsAllQuiet() {
		lines = append(lines, "")
		lines = append(lines, ItemDetailStyle.Render("    All clear"))
	}

	// Compose the bottom-pinned region: pulse (if cached) above the collapsed
	// section badges (if any). Badges have higher priority than the pulse body
	// — if the sidebar is too short to fit both, the pulse tail is sacrificed
	// first so the badges stay visible at the floor. All accounting is in
	// visual rows (lipgloss.Height) because a single session entry can render
	// as multiple terminal lines via wrapped subtitles.
	pulseRows := m.pulseBlock()
	badgesRows := m.collapsedBadgesBlock()

	if m.height > 0 && (len(pulseRows) > 0 || len(badgesRows) > 0) {
		badgesH := visualHeight(badgesRows)
		pulseRoom := m.height - badgesH
		if pulseRoom < 0 {
			pulseRoom = 0
		}
		// Truncate pulse rows by visual height, not entry count.
		pulseRows = truncateToVisualBudget(pulseRows, pulseRoom)

		pinned := append([]string{}, pulseRows...)
		pinned = append(pinned, badgesRows...)

		pinnedH := visualHeight(pinned)
		upperBudget := m.height - pinnedH
		if upperBudget < 0 {
			upperBudget = 0
		}

		// Walk session lines in entry order, keeping them while visual budget
		// holds; drop the rest from the bottom so pinned content stays on screen.
		visual := 0
		cutAt := len(lines)
		for i, l := range lines {
			h := lipgloss.Height(l)
			if visual+h > upperBudget {
				cutAt = i
				break
			}
			visual += h
		}
		lines = lines[:cutAt]

		// Pad blank lines until the pinned region floats to the floor.
		for visual < upperBudget {
			lines = append(lines, "")
			visual++
		}

		lines = append(lines, pinned...)
	}

	// Truncate to fit available height
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}

	return strings.Join(lines, "\n")
}

// visualHeight sums lipgloss.Height across a slice of rendered lines. A single
// entry may span multiple terminal rows (e.g. a session row with a wrapped
// subtitle), so entry count is not the same as visual height.
func visualHeight(rows []string) int {
	total := 0
	for _, r := range rows {
		total += lipgloss.Height(r)
	}
	return total
}

// truncateToVisualBudget drops trailing entries until the cumulative visual
// height fits within budget. An entry that doesn't fit at all is dropped
// whole — partial-entry truncation would corrupt styling.
func truncateToVisualBudget(rows []string, budget int) []string {
	if budget <= 0 {
		return nil
	}
	visual := 0
	for i, r := range rows {
		h := lipgloss.Height(r)
		if visual+h > budget {
			return rows[:i]
		}
		visual += h
	}
	return rows
}

// pulseBlock returns the pulse rows to pin near the bottom of the sidebar: a
// separator, a dim "pulse · <age>" header, and the prose summary wrapped to
// sidebar width. Returns nil when no pulse is cached. The summary text is
// italicized once the pulse is older than an hour so stale state is visually
// demoted without disappearing.
func (m *SidebarModel) pulseBlock() []string {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	pulse := claude.ReadCachedPulse()
	if pulse == nil {
		return nil
	}
	summary := strings.TrimSpace(pulse.Summary)
	if summary == "" {
		return nil
	}
	age := time.Since(pulse.GeneratedAt)
	label := PulseHeaderStyle.Render(IconPulse + " pulse")
	timestamp := ItemDetailStyle.Render(" · " + FormatAge(pulse.GeneratedAt) + " ago")
	header := " " + label + timestamp

	bodyStyle := ItemDetailStyle.Width(m.width).PaddingLeft(1)
	if age > time.Hour {
		bodyStyle = bodyStyle.Italic(true)
	}
	body := bodyStyle.Render(summary)
	separator := SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width))
	out := []string{separator, header}
	out = append(out, strings.Split(body, "\n")...)
	return out
}

// collapsedBadgesBlock returns the inline "CLAUDING / LATER / BACKLOG (n)"
// badges to pin to the floor of the sidebar. Returns nil when no sections
// are collapsed.
func (m *SidebarModel) collapsedBadgesBlock() []string {
	if m.height <= 0 {
		return nil
	}
	claudingCount := m.claudingCount
	laterCount := m.laterCount
	backlogCount := 0
	if !m.backlogExpanded {
		backlogCount = len(m.backlogs)
	}
	if claudingCount == 0 && laterCount == 0 && backlogCount == 0 {
		return nil
	}
	var parts []string
	if claudingCount > 0 {
		parts = append(parts, GroupHeaderWorkingStyle.Render(fmt.Sprintf("%s CLAUDING (%d)", IconWand, claudingCount)))
	}
	if laterCount > 0 {
		parts = append(parts, GroupHeaderLaterStyle.Render(fmt.Sprintf("%s LATER (%d)", IconLater, laterCount)))
	}
	if backlogCount > 0 {
		parts = append(parts, GroupHeaderBacklogStyle.Render(fmt.Sprintf("%s BACKLOG (%d)", IconBacklog, backlogCount)))
	}
	separator := SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width))
	return []string{separator, strings.Join(parts, " ")}
}

// projectLabel builds the "🐾 name [🚩]" string shared by all project headers.
// The leading glyph is the project's spirit animal (deterministic from the
// project name) — it inherits the caller's foreground color.
func projectLabel(project string, flagged bool) string {
	s := IconForProject(project) + " " + project
	if flagged {
		s += " " + flagItemStyle.Render(IconFlag)
	}
	return s
}

func renderGroupHeader(project string, flagged bool) string {
	return GroupHeaderProjectStyle.Render(projectLabel(project, flagged))
}

// selectedProjectHeaderStyle is the highlight style for project headers at project-level nav.
var selectedProjectHeaderStyle = GroupHeaderStyle.
	Foreground(ColorAccent).
	Background(ColorSelectionBg)

func renderSelectedProjectHeader(project string, width int, flagged bool) string {
	return selectedProjectHeaderStyle.Width(width).Render(projectLabel(project, flagged))
}

func renderProjectSubHeader(project string, flagged bool) string {
	return ProjectSubHeaderStyle.Render(projectLabel(project, flagged))
}

func renderStatusGroupHeader(order int) string {
	switch order {
	case OrderUserTurn:
		return GroupHeaderDoneStyle.Render(IconHandRaise + " YOUR TURN")
	case OrderAgentTurn:
		return GroupHeaderWorkingStyle.Render(IconWand + " CLAUDING")
	case OrderLater:
		return GroupHeaderLaterStyle.Render(IconLater + " LATER")
	case OrderBacklog:
		return GroupHeaderBacklogStyle.Render(IconBacklog + " BACKLOG")
	default:
		return ""
	}
}

// backlogItemLineCount returns the number of terminal lines a rendered backlog item occupies.
// Must stay in sync with renderBacklogItem — accounts for tags line AND active tag input.
func (m SidebarModel) backlogItemLineCount(b claude.Backlog) int {
	if len(b.Tags) > 0 {
		return 2
	}
	if m.inlineTagBacklogID == b.ID && m.inlineTagInputView != "" {
		return 2
	}
	return 1
}

// renderBacklogItem renders a single backlog entry in the list.
// Tagged items get a second line showing "#tag1 #tag2 …".
func (m SidebarModel) renderBacklogItem(isSelected bool, backlog claude.Backlog) string {
	title := backlog.DisplayTitle()
	title = strings.ReplaceAll(title, "\n", " ")

	age := FormatAge(backlog.UpdatedAt)

	const prefixWidth = 4
	iconStr := ItemDetailStyle.Render(IconBacklog + " ")
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

	isLanding := isSelected && backlog.ID == m.landBacklogID && m.landFrame < m.landMaxFrames

	// Compute shared selected-state styles once (reused by main line and tags subtitle).
	var bg, barSt lipgloss.Style
	if isSelected {
		bg = lipgloss.NewStyle().Background(ColorSelectionBg)
		if isLanding {
			barColor := lipgloss.Color(blendHex("#60a5fa", "#ffffff", m.landT()))
			barSt = lipgloss.NewStyle().Foreground(barColor).Background(ColorSelectionBg)
		}
	}

	flagGlyph := " "
	if m.flaggedBacklogs[backlog.ID] {
		flagGlyph = flagItemStyle.Render(IconFlag)
	}

	var line string
	if isSelected {
		if isLanding {
			line = flagGlyph + " " + barSt.Render("▌") + bg.Render(" ") +
				bg.Render(IconBacklog+" ") +
				bg.Render(title) +
				bg.Render(strings.Repeat(" ", gap)) +
				bg.Render(age)
		} else {
			line = flagGlyph + " " + bg.Render("▌ ") +
				bg.Render(IconBacklog+" ") +
				bg.Render(title) +
				bg.Render(strings.Repeat(" ", gap)) +
				bg.Render(age)
		}
	} else {
		line = flagGlyph + "   " + iconStr + title + strings.Repeat(" ", gap) + ageStr
	}

	showTagInput := isSelected && m.inlineTagBacklogID == backlog.ID && m.inlineTagInputView != ""
	if len(backlog.Tags) > 0 || showTagInput {
		indent := strings.Repeat(" ", iconWidth)
		tagsStr := ""
		if len(backlog.Tags) > 0 {
			tagsStr = "#" + strings.Join(backlog.Tags, " #")
			maxTagsWidth := m.width - prefixWidth - iconWidth - 2
			if maxTagsWidth < 1 {
				maxTagsWidth = 1
			}
			if lipgloss.Width(tagsStr) > maxTagsWidth {
				tagsStr = ansi.Truncate(tagsStr, maxTagsWidth, "…")
			}
		}
		if isSelected {
			if showTagInput {
				sep := ""
				if tagsStr != "" {
					sep = "  "
				}
				line += "\n" + "  " + bg.Render("▌ ") +
					TagBadgeStyle.Background(ColorSelectionBg).Render(indent+tagsStr+sep) +
					m.inlineTagInputView
			} else {
				tagsContent := indent + tagsStr
				padWidth := m.width - prefixWidth - lipgloss.Width(tagsContent)
				if padWidth < 0 {
					padWidth = 0
				}
				if isLanding {
					line += "\n" + "  " + barSt.Render("▌") + bg.Render(" ") +
						TagBadgeStyle.Background(ColorSelectionBg).Render(tagsContent) +
						bg.Render(strings.Repeat(" ", padWidth))
				} else {
					line += "\n" + "  " + bg.Render("▌ ") +
						TagBadgeStyle.Background(ColorSelectionBg).Render(tagsContent) +
						bg.Render(strings.Repeat(" ", padWidth))
				}
			}
		} else {
			line += "\n" + "    " + indent + TagBadgeStyle.Render(tagsStr)
		}
	}

	return line
}

// PaneIDAtLine maps a terminal line index (relative to list content start) to
// the PaneID of the session rendered at that line. Returns "" for headers,
// separators, or out-of-bounds lines. Mirrors View()'s group/filter/item logic.
func (m SidebarModel) PaneIDAtLine(line int) string {
	if line < 0 || len(m.allSorted) == 0 {
		return ""
	}

	query := m.searchTextQuery()
	currentLine := 0

	if query != "" {
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
		order := sessionOrder(s)

		// Skip collapsed sections — mirrors View()'s continue checks exactly
		if !m.claudingExpanded && order == OrderAgentTurn {
			continue
		}
		if !m.laterExpanded && order == OrderLater {
			continue
		}
		if m.focusMode && !m.IsEffectivelyFlagged(s) {
			continue
		}

		// Group headers — must mirror View()'s logic exactly
		if m.groupByProject {
			// Emit LATER status header when entering the Later zone
			if order == OrderLater && currentOrder != OrderLater {
				currentOrder = OrderLater
				currentProject = "" // reset to force project sub-header
				if anyLinesEmitted {
					currentLine++ // separator
				}
				anyLinesEmitted = true
				currentLine++ // LATER status group header
			}
			if s.Project != currentProject {
				currentProject = s.Project
				if currentOrder == OrderLater {
					currentLine++ // project sub-header (no separator within Later)
				} else {
					if anyLinesEmitted {
						currentLine++ // separator
					}
					anyLinesEmitted = true
					currentLine++ // group header
				}
			}
		} else {
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

	query := m.searchTextQuery()
	currentLine := 0

	// Count all session lines (mirrors PaneIDAtLine's counting, runs the full loop).
	if query != "" {
		for _, s := range m.filtered {
			currentLine += m.itemLineCount(s, query)
		}
	} else {
		currentProject := ""
		currentOrder := -1
		anyLinesEmitted := false

		for _, s := range m.allSorted {
			order := sessionOrder(s)

			// Skip collapsed sections — mirrors View()'s continue checks exactly
			if !m.claudingExpanded && order == OrderAgentTurn {
				continue
			}
			if !m.laterExpanded && order == OrderLater {
				continue
			}
			if m.focusMode && !m.IsEffectivelyFlagged(s) {
				continue
			}

			if m.groupByProject {
				// Emit LATER status header when entering the Later zone
				if order == OrderLater && currentOrder != OrderLater {
					currentOrder = OrderLater
					currentProject = "" // reset to force project sub-header
					if anyLinesEmitted {
						currentLine++ // separator
					}
					anyLinesEmitted = true
					currentLine++ // LATER status group header
				}
				if s.Project != currentProject {
					currentProject = s.Project
					if currentOrder == OrderLater {
						currentLine++ // project sub-header (no separator within Later)
					} else {
						if anyLinesEmitted {
							currentLine++ // separator
						}
						anyLinesEmitted = true
						currentLine++ // group header
					}
				}
			} else {
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

	// Walk backlog items: project header → items.
	currentBacklogProject := ""
	for _, backlog := range m.filteredBacklog {
		if backlog.Project != currentBacklogProject {
			currentBacklogProject = backlog.Project
			currentLine++ // project sub-header
		}
		if line == currentLine {
			return backlog.ID
		}
		currentLine += m.backlogItemLineCount(backlog)
	}
	return ""
}
