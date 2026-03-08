package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// selBg adds ColorSelectionBg background to st when selected, otherwise returns st unchanged.
func selBg(st lipgloss.Style, selected bool) lipgloss.Style {
	if selected {
		return st.Background(ColorSelectionBg)
	}
	return st
}

type ListModel struct {
	items                []claude.ClaudeSession
	filtered             []claude.ClaudeSession            // cursor-navigable matching items
	allSorted            []claude.ClaudeSession            // all items sorted (for stable group rendering)
	matchSet             map[string]bool                   // PaneIDs of filter-matching items; nil = all match
	cursor               int
	height               int
	width                int
	filter               string
	spinnerView          string
	commitDoneFrame      int
	diffStats            map[string]map[string]claude.FileDiffStat // sessionID -> file stats
	summaryLoadingPanes  map[string]bool                           // pane IDs with in-flight synthesization
	groupByProject       bool
}

func (m *ListModel) SetGroupByProject(v bool) {
	m.groupByProject = v
	m.applyFilter()
}

func (m ListModel) GroupByProject() bool {
	return m.groupByProject
}

func NewListModel() ListModel {
	return ListModel{
		diffStats:           make(map[string]map[string]claude.FileDiffStat),
		summaryLoadingPanes: make(map[string]bool),
	}
}

func (m ListModel) SummaryLoadingCount() int {
	return len(m.summaryLoadingPanes)
}

// SetSummaryLoading sets immediate client-side loading state for instant UI feedback.
func (m *ListModel) SetSummaryLoading(paneID string, loading bool) {
	if m.summaryLoadingPanes == nil {
		m.summaryLoadingPanes = make(map[string]bool)
	}
	if loading {
		m.summaryLoadingPanes[paneID] = true
	} else {
		delete(m.summaryLoadingPanes, paneID)
	}
}

func (m *ListModel) SetDiffStats(sessionID string, stats map[string]claude.FileDiffStat) {
	if m.diffStats == nil {
		m.diffStats = make(map[string]map[string]claude.FileDiffStat)
	}
	m.diffStats[sessionID] = stats
}

// commitDoneFrames is a distinct animation for commit-and-done pending sessions.
var commitDoneFrames = []string{"◐", "◓", "◑", "◒"}

func (m *ListModel) SetSpinnerView(s string) {
	m.spinnerView = s
	m.commitDoneFrame = (m.commitDoneFrame + 1) % len(commitDoneFrames)
}

func (m *ListModel) SetItems(items []claude.ClaudeSession) {
	m.items = items
	// Sync summary loading state from daemon-pushed SynthesizePending flags
	m.summaryLoadingPanes = make(map[string]bool)
	for _, s := range items {
		if s.SynthesizePending {
			m.summaryLoadingPanes[s.PaneID] = true
		}
	}
	m.applyFilter()
	// Clamp cursor
	if len(m.filtered) > 0 && m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *ListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *ListModel) SetFilter(f string) {
	m.filter = f
	m.applyFilter()
	m.cursor = 0
}

func (m *ListModel) ClearFilter() {
	m.filter = ""
	m.applyFilter()
	m.cursor = 0
}

func (m ListModel) SelectedItem() (claude.ClaudeSession, bool) {
	if len(m.filtered) == 0 {
		return claude.ClaudeSession{}, false
	}
	return m.filtered[m.cursor], true
}

func (m *ListModel) SelectByPaneID(paneID string) bool {
	for i, s := range m.filtered {
		if s.PaneID == paneID {
			m.cursor = i
			return true
		}
	}
	return false
}

func (m *ListModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *ListModel) MoveDown() {
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
	}
}

func (m ListModel) Items() []claude.ClaudeSession {
	return m.filtered
}

func (m *ListModel) applyFilter() {
	// Always maintain a sorted copy of all items for stable group rendering
	m.allSorted = make([]claude.ClaudeSession, len(m.items))
	copy(m.allSorted, m.items)

	if m.filter == "" {
		m.filtered = make([]claude.ClaudeSession, len(m.items))
		copy(m.filtered, m.items)
		m.matchSet = nil // nil = all match
	} else {
		f := strings.ToLower(m.filter)
		m.filtered = nil
		m.matchSet = make(map[string]bool)
		for _, s := range m.items {
			surface := strings.ToLower(s.CustomTitle + " " + s.Headline + " " + s.FirstMessage + " " + s.LastUserMessage)
			if strings.Contains(surface, f) {
				m.filtered = append(m.filtered, s)
				m.matchSet[s.PaneID] = true
			}
		}
	}
	if m.groupByProject {
		sortByProject(m.filtered)
		sortByProject(m.allSorted)
	} else {
		sortByStatus(m.filtered)
		sortByStatus(m.allSorted)
	}
}

func sortByProject(sessions []claude.ClaudeSession) {
	// Primary: project name alphabetically; secondary: status order; tertiary: oldest LastChanged first
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0; j-- {
			a, b := sessions[j-1], sessions[j]
			if a.Project > b.Project ||
				(a.Project == b.Project && statusOrder(a.Status) > statusOrder(b.Status)) ||
				(a.Project == b.Project && statusOrder(a.Status) == statusOrder(b.Status) && a.LastChanged.After(b.LastChanged)) {
				sessions[j], sessions[j-1] = sessions[j-1], sessions[j]
			} else {
				break
			}
		}
	}
}

func sortByStatus(sessions []claude.ClaudeSession) {
	// Primary: status order (Done, Working, Deferred); secondary: oldest LastChanged first
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0; j-- {
			a, b := sessions[j-1], sessions[j]
			if statusOrder(a.Status) > statusOrder(b.Status) ||
				(statusOrder(a.Status) == statusOrder(b.Status) && a.LastChanged.After(b.LastChanged)) {
				sessions[j], sessions[j-1] = sessions[j-1], sessions[j]
			} else {
				break
			}
		}
	}
}

func statusOrder(s claude.Status) int {
	switch s {
	case claude.StatusDone:
		return 0
	case claude.StatusWorking:
		return 1
	case claude.StatusDeferred:
		return 2
	default:
		return 3
	}
}

// diffColWidths holds the max digit widths for diff stat columns across all visible items.
type diffColWidths struct {
	files   int // digits in file count
	added   int // digits in added lines
	removed int // digits in removed lines
}

func (m ListModel) computeDiffColWidths() diffColWidths {
	var dw diffColWidths
	for _, s := range m.filtered {
		if s.SessionID == "" {
			continue
		}
		stats, ok := m.diffStats[s.SessionID]
		if !ok || len(stats) == 0 {
			continue
		}
		totalAdded, totalRemoved := 0, 0
		for _, ds := range stats {
			totalAdded += ds.Added
			totalRemoved += ds.Removed
		}
		if w := len(fmt.Sprintf("%d", len(stats))); w > dw.files {
			dw.files = w
		}
		if w := len(fmt.Sprintf("%d", totalAdded)); w > dw.added {
			dw.added = w
		}
		if w := len(fmt.Sprintf("%d", totalRemoved)); w > dw.removed {
			dw.removed = w
		}
	}
	return dw
}

func (m ListModel) View() string {
	if len(m.items) == 0 {
		return EmptyStyle.Width(m.width).Render("No Claude sessions found\n\nStart Claude in a tmux pane to see it here.")
	}

	dw := m.computeDiffColWidths()
	filterLower := strings.ToLower(m.filter)

	// Determine selected PaneID for cursor tracking across the full list
	var selectedPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		selectedPaneID = m.filtered[m.cursor].PaneID
	}

	var lines []string
	currentProject := ""
	currentStatus := claude.Status(-1)

	for _, s := range m.allSorted {
		// Group headers — always rendered for spatial stability during filtering
		if m.groupByProject {
			if s.Project != currentProject {
				currentProject = s.Project
				if len(lines) > 0 {
					lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
				}
				lines = append(lines, renderGroupHeader(s.Project))
			}
		} else {
			if s.Status != currentStatus {
				currentStatus = s.Status
				currentProject = "" // reset project tracking for new status group
				if len(lines) > 0 {
					lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
				}
				lines = append(lines, renderStatusGroupHeader(s.Status))
			}
			if s.Project != currentProject {
				currentProject = s.Project
				lines = append(lines, renderProjectSubHeader(s.Project))
			}
		}

		// Only render items that match the filter (matchSet nil = all match)
		if m.matchSet != nil && !m.matchSet[s.PaneID] {
			continue
		}

		isSelected := s.PaneID == selectedPaneID
		lines = append(lines, m.renderItem(isSelected, s, dw, filterLower))
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

func renderProjectSubHeader(project string) string {
	return ProjectSubHeaderStyle.Render(IconFolder + " " + project)
}

func renderStatusGroupHeader(status claude.Status) string {
	switch status {
	case claude.StatusDone:
		return GroupHeaderDoneStyle.Render(IconFlag + " YOUR TURN")
	case claude.StatusWorking:
		return GroupHeaderWorkingStyle.Render(IconBolt + " CLAUDING")
	case claude.StatusDeferred:
		return GroupHeaderDeferredStyle.Render(IconHourglass + " DEFERRED")
	default:
		return ""
	}
}

func (m ListModel) renderItem(isSelected bool, s claude.ClaudeSession, dw diffColWidths, filterLower string) string {

	// Display name priority: custom title → headline → first message → project (fallback)
	var displayName, sourceIcon string
	if s.CustomTitle != "" {
		displayName = s.CustomTitle
		sourceIcon = IconTag
	} else if s.Headline != "" {
		displayName = s.Headline
		sourceIcon = IconTag
	} else if s.FirstMessage != "" {
		displayName = strings.ReplaceAll(s.FirstMessage, "\n", " ")
		sourceIcon = IconQuote
	} else {
		displayName = "(New session)"
		sourceIcon = IconAsterisk
		displayName = lipgloss.NewStyle().Italic(true).Render(displayName)
	}

	isNewSession := s.CustomTitle == "" && s.Headline == "" && s.FirstMessage == ""
	filterActive := filterLower != ""

	withBg := func(st lipgloss.Style) lipgloss.Style { return selBg(st, isSelected) }
	sp := func(s string) string {
		if isSelected {
			return SelectedBgStyle.Render(s)
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
	iconStr := ItemDetailStyle.Render(sourceIcon + " ")
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
		bg := SelectedBgStyle
		var styledName string
		if filterActive && !isNewSession {
			styledName = highlightFilter(displayName, filterLower, bg)
		} else {
			styledName = bg.Render(displayName)
		}
		namePart = bg.Render("  ") +
			SelectedBarStyle.Render("▌") +
			bg.Render(" ") +
			bg.Foreground(ColorMuted).Render(sourceIcon+" ") +
			styledName
		gapStr = bg.Render(strings.Repeat(" ", gap))
	} else {
		var styledName string
		if filterActive && !isNewSession {
			styledName = highlightFilter(displayName, filterLower, lipgloss.NewStyle())
		} else {
			styledName = displayName
		}
		namePart = "    " + iconStr + styledName
		gapStr = strings.Repeat(" ", gap)
	}

	line := namePart + gapStr + right

	// selSubtitle wraps a subtitle content string with the selection bar at col 2.
	// bar(1) + sp("  ")(2) + content(m.width-5) = m.width-2 total, matching the main line.
	selSubtitle := func(style lipgloss.Style, content string) string {
		return sp("  ") + SelectedBarStyle.Render("▌") +
			withBg(style).Width(m.width - 5).Render(content)
	}

	if m.summaryLoadingPanes[s.PaneID] {
		if isSelected {
			line += "\n" + selSubtitle(SelectedBgStyle.Foreground(ColorMuted).Italic(true), "   "+m.spinnerView+" synthesizing…")
		} else {
			line += "\n" + SummaryStyle.Render("      "+m.spinnerView+" synthesizing…")
		}
	}

	// Show enqueued message as a subtitle line
	if s.EnqueuePending != "" {
		rawMsg := strings.ReplaceAll(s.EnqueuePending, "\n", " ")
		line += "\n" + m.renderSubtitleLine(rawMsg, filterLower, IconEnqueue, isSelected, false)
	}

	// Show last user message as a subtitle line (single line, truncated to list width)
	if s.LastUserMessage != "" {
		rawMsg := strings.ReplaceAll(s.LastUserMessage, "\n", " ")
		doHL := filterActive && containsFilter(s.LastUserMessage, filterLower)
		line += "\n" + m.renderSubtitleLine(rawMsg, filterLower, IconInput, isSelected, doHL)
	}

	// Match-context subtitles: show non-visible fields that matched the filter
	if filterActive {
		// Headline: shown when it's not the display name (i.e. customTitle is set) and matches
		if s.Headline != "" && s.CustomTitle != "" && containsFilter(s.Headline, filterLower) {
			line += "\n" + m.renderSubtitleLine(s.Headline, filterLower, IconHeadline, isSelected, true)
		}
		// FirstMessage: shown when it's not the display name (customTitle or headline is set) and matches
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.Headline != "") && containsFilter(s.FirstMessage, filterLower) {
			rawFirst := strings.ReplaceAll(s.FirstMessage, "\n", " ")
			line += "\n" + m.renderSubtitleLine(rawFirst, filterLower, IconQuote, isSelected, true)
		}
	}

	// Badges line — outcome indicators (git commit, etc.)
	if badges := renderBadges(s); badges != "" {
		if isSelected {
			line += "\n" + selSubtitle(ItemDetailStyle, "   "+badges)
		} else {
			line += "\n" + ItemDetailStyle.Render("      "+badges)
		}
	}

	return line
}

// renderSubtitleLine renders a subtitle with optional filter highlighting.
// Each segment gets its own Render call — no nesting of lipgloss Render.
func (m ListModel) renderSubtitleLine(text, filterLower, icon string, isSelected, doHighlight bool) string {
	if isSelected {
		prefix := "   " + icon + "  "
		prefixWidth := lipgloss.Width(prefix)
		msgWidth := m.width - 5 - prefixWidth
		if msgWidth < 1 {
			msgWidth = 1
		}
		truncated := ansi.Truncate(text, msgWidth, "…")
		baseStyle := ItemDetailStyle.Background(ColorSelectionBg)
		bgStyle := lipgloss.NewStyle().Background(ColorSelectionBg)

		var content string
		if doHighlight && filterLower != "" {
			content = baseStyle.Render(prefix) + highlightFilter(truncated, filterLower, baseStyle)
		} else {
			content = baseStyle.Render(prefix + truncated)
		}
		// Manual padding to fill width (can't use .Width().Render() on pre-highlighted content)
		contentPlainWidth := prefixWidth + lipgloss.Width(truncated)
		padWidth := m.width - 5 - contentPlainWidth
		if padWidth < 0 {
			padWidth = 0
		}
		return bgStyle.Render("  ") + SelectedBarStyle.Render("▌") + content + bgStyle.Render(strings.Repeat(" ", padWidth))
	}

	// Unselected
	prefix := "      " + icon + "  "
	prefixWidth := lipgloss.Width(prefix)
	msgWidth := m.width - 2 - prefixWidth
	if msgWidth < 1 {
		msgWidth = 1
	}
	truncated := ansi.Truncate(text, msgWidth, "…")
	if doHighlight && filterLower != "" {
		return ItemDetailStyle.Render(prefix) + highlightFilter(truncated, filterLower, ItemDetailStyle)
	}
	return ItemDetailStyle.Render(prefix + truncated)
}

// renderBadges returns inline outcome indicators for a session entry.
// Returns empty string if no badges apply.
func renderBadges(s claude.ClaudeSession) string {
	var badges []string
	if s.LastActionCommit && s.Status == claude.StatusDone {
		badges = append(badges, DiffAddedStyle.Render(IconGitCommit+" committed"))
	}
	if len(badges) == 0 {
		return ""
	}
	return strings.Join(badges, "  ")
}

func (m ListModel) renderDetail(s claude.ClaudeSession, selected bool) string {
	bg := func(st lipgloss.Style) lipgloss.Style { return selBg(st, selected) }
	if s.CommitDonePending {
		return bg(CommitDoneStyle).Render(commitDoneFrames[m.commitDoneFrame])
	}
	switch s.Status {
	case claude.StatusDone:
		return bg(ItemDetailStyle).Render(formatAge(s.LastChanged))
	case claude.StatusWorking:
		if s.PermissionMode == "plan" {
			return bg(StatPlanStyle).Render(m.spinnerView)
		}
		return bg(StatWorkingStyle).Render(m.spinnerView)
	case claude.StatusDeferred:
		if s.DeferUntil.IsZero() {
			return bg(ItemDetailStyle).Render("deferred")
		}
		remaining := time.Until(s.DeferUntil)
		if remaining < 0 {
			return bg(ItemDetailStyle).Render("expired")
		}
		return bg(ItemDetailStyle).Render(fmt.Sprintf("%dm left", int(remaining.Minutes())))
	default:
		return ""
	}
}

func formatAge(t time.Time) string {
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
