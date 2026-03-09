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
	matchSet             map[string]bool                   // PaneIDs of narrow-matching items; nil = all match
	cursor               int
	height               int
	width                int
	narrow               string
	spinnerView          string
	commitDoneFrame      int
	diffStats            map[string]map[string]claude.FileDiffStat // sessionID -> file stats
	summaryLoadingPanes  map[string]bool                           // pane IDs with in-flight synthesization
	groupByProject       bool
	deselected           bool // when true, SelectedItem() returns false (minimap on non-Claude pane)
}

func (m *ListModel) SetGroupByProject(v bool) {
	m.groupByProject = v
	m.applyNarrow()
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
	// Remember currently selected PaneID before rebuilding
	var selectedPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		selectedPaneID = m.filtered[m.cursor].PaneID
	}

	m.items = items
	// Sync summary loading state from daemon-pushed SynthesizePending flags
	m.summaryLoadingPanes = make(map[string]bool)
	for _, s := range items {
		if s.SynthesizePending {
			m.summaryLoadingPanes[s.PaneID] = true
		}
	}
	m.applyNarrow()

	// Restore selection to same session; fall back to clamping
	if selectedPaneID == "" || !m.SelectByPaneID(selectedPaneID) {
		if len(m.filtered) > 0 && m.cursor >= len(m.filtered) {
			m.cursor = len(m.filtered) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
	}
}

func (m *ListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *ListModel) SetNarrow(f string) {
	m.narrow = f
	m.applyNarrow()
	m.cursor = 0
}

func (m *ListModel) ClearNarrow() {
	m.narrow = ""
	m.applyNarrow()
	m.cursor = 0
}

func (m ListModel) SelectedItem() (claude.ClaudeSession, bool) {
	if len(m.filtered) == 0 || m.deselected {
		return claude.ClaudeSession{}, false
	}
	return m.filtered[m.cursor], true
}

// Deselect marks the list as having no active selection (minimap on non-Claude pane).
func (m *ListModel) Deselect() {
	m.deselected = true
}

// Reselect restores the list selection after Deselect.
func (m *ListModel) Reselect() {
	m.deselected = false
}

func (m *ListModel) SelectByPaneID(paneID string) bool {
	for i, s := range m.filtered {
		if s.PaneID == paneID {
			m.cursor = i
			m.deselected = false
			return true
		}
	}
	return false
}

func (m *ListModel) MoveUp() {
	m.deselected = false
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *ListModel) MoveDown() {
	m.deselected = false
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
	}
}

func (m ListModel) Items() []claude.ClaudeSession {
	return m.filtered
}

func (m *ListModel) applyNarrow() {
	// Always maintain a sorted copy of all items for stable group rendering
	m.allSorted = make([]claude.ClaudeSession, len(m.items))
	copy(m.allSorted, m.items)

	if m.narrow == "" {
		m.filtered = make([]claude.ClaudeSession, len(m.items))
		copy(m.filtered, m.items)
		m.matchSet = nil // nil = all match
	} else {
		f := strings.ToLower(m.narrow)
		m.filtered = nil
		m.matchSet = make(map[string]bool)
		for _, s := range m.items {
			if matchesNarrow(s.CustomTitle, f) || matchesNarrow(s.Headline, f) || matchesNarrow(s.FirstMessage, f) || matchesNarrow(s.LastUserMessage, f) {
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
	// Primary: project name alphabetically; secondary: session order; tertiary: newest created first
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0; j-- {
			a, b := sessions[j-1], sessions[j]
			if a.Project > b.Project ||
				(a.Project == b.Project && sessionOrder(a) > sessionOrder(b)) ||
				(a.Project == b.Project && sessionOrder(a) == sessionOrder(b) && a.CreatedAt.Before(b.CreatedAt)) {
				sessions[j], sessions[j-1] = sessions[j-1], sessions[j]
			} else {
				break
			}
		}
	}
}

func sortByStatus(sessions []claude.ClaudeSession) {
	// Primary: session order (UserTurn, AgentTurn, Later); secondary: newest created first
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0; j-- {
			a, b := sessions[j-1], sessions[j]
			if sessionOrder(a) > sessionOrder(b) ||
				(sessionOrder(a) == sessionOrder(b) && a.CreatedAt.Before(b.CreatedAt)) {
				sessions[j], sessions[j-1] = sessions[j-1], sessions[j]
			} else {
				break
			}
		}
	}
}

// Session group ordering constants.
const (
	OrderUserTurn  = 0
	OrderAgentTurn = 1
	OrderLater     = 2
	OrderOther     = 3
)

func sessionOrder(s claude.ClaudeSession) int {
	if s.LaterBookmarkID != "" {
		return OrderLater
	}
	switch s.Status {
	case claude.StatusUserTurn:
		return OrderUserTurn
	case claude.StatusAgentTurn:
		return OrderAgentTurn
	default:
		return OrderOther
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
	query := strings.ToLower(m.narrow)

	// Determine selected PaneID for cursor tracking across the full list
	var selectedPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		selectedPaneID = m.filtered[m.cursor].PaneID
	}

	var lines []string
	currentProject := ""
	currentOrder := -1

	for _, s := range m.allSorted {
		// Group headers — always rendered for spatial stability during narrowing
		if m.groupByProject {
			if s.Project != currentProject {
				currentProject = s.Project
				if len(lines) > 0 {
					lines = append(lines, SeparatorStyle.Width(m.width).Render(strings.Repeat("─", m.width)))
				}
				lines = append(lines, renderGroupHeader(s.Project))
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
				lines = append(lines, renderProjectSubHeader(s.Project))
			}
		}

		// Only render items that match the narrow (matchSet nil = all match)
		if m.matchSet != nil && !m.matchSet[s.PaneID] {
			continue
		}

		isSelected := s.PaneID == selectedPaneID && !m.deselected
		lines = append(lines, m.renderItem(isSelected, s, dw, query))
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

func renderStatusGroupHeader(order int) string {
	switch order {
	case OrderUserTurn:
		return GroupHeaderDoneStyle.Render(IconFlag + " YOUR TURN")
	case OrderAgentTurn:
		return GroupHeaderWorkingStyle.Render(IconBolt + " CLAUDING")
	case OrderLater:
		return GroupHeaderLaterStyle.Render(IconBookmark + " LATER")
	default:
		return ""
	}
}

func (m ListModel) renderItem(isSelected bool, s claude.ClaudeSession, dw diffColWidths, query string) string {

	// Display name priority: custom title → headline → first message → (new session)
	var displayName string
	if s.CustomTitle != "" {
		displayName = strings.ReplaceAll(s.CustomTitle, "\n", " ")
	} else if s.Headline != "" {
		displayName = strings.ReplaceAll(s.Headline, "\n", " ")
	} else if s.FirstMessage != "" {
		displayName = strings.ReplaceAll(s.FirstMessage, "\n", " ")
	} else {
		displayName = lipgloss.NewStyle().Italic(true).Render("(New session)")
	}

	glyph := AvatarGlyph(s.AvatarAnimalIdx)

	isNewSession := s.CustomTitle == "" && s.Headline == "" && s.FirstMessage == ""
	hasQuery := query != ""

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
	iconStr := AvatarStyle(s.AvatarColorIdx).Render(glyph + "  ")
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
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, bg)
		} else {
			styledName = bg.Render(displayName)
		}
		namePart = bg.Render("  ") +
			SelectedBarStyle.Render("▌") +
			bg.Render(" ") +
			AvatarStyle(s.AvatarColorIdx).Background(ColorSelectionBg).Render(glyph + "  ") +
			styledName
		gapStr = bg.Render(strings.Repeat(" ", gap))
	} else {
		var styledName string
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, lipgloss.NewStyle())
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

	// Show queued message as a subtitle line
	if s.QueuePending != "" {
		rawMsg := strings.ReplaceAll(s.QueuePending, "\n", " ")
		line += "\n" + m.renderSubtitleLine(rawMsg, query, IconQueue, isSelected, false)
	}

	// Show last user message as subtitle (up to two lines, word-wrapped)
	if s.LastUserMessage != "" {
		rawMsg := strings.ReplaceAll(s.LastUserMessage, "\n", " ")
		doHL := hasQuery && matchesNarrow(s.LastUserMessage, query)
		line += "\n" + m.renderSubtitleTwoLines(rawMsg, query, IconQuote, isSelected, doHL)
	}

	// Match-context subtitles: show non-visible fields that matched the search
	if hasQuery {
		// Headline: shown when it's not the display name (i.e. customTitle is set) and matches
		if s.Headline != "" && s.CustomTitle != "" && matchesNarrow(s.Headline, query) {
			line += "\n" + m.renderSubtitleLine(s.Headline, query, IconHeadline, isSelected, true)
		}
		// FirstMessage: shown when it's not the display name (customTitle or headline is set) and matches
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.Headline != "") && matchesNarrow(s.FirstMessage, query) {
			rawFirst := strings.ReplaceAll(s.FirstMessage, "\n", " ")
			line += "\n" + m.renderSubtitleLine(rawFirst, query, IconQuote, isSelected, true)
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

// subtitleMsgWidth returns the available text width for a subtitle line with the given icon.
func (m ListModel) subtitleMsgWidth(icon string, isSelected bool) int {
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
func (m ListModel) renderSubtitleLine(text, query, icon string, isSelected, doHighlight bool) string {
	msgWidth := m.subtitleMsgWidth(icon, isSelected)
	truncated := ansi.Truncate(text, msgWidth, "…")

	if isSelected {
		prefix := "   " + icon + " "
		prefixWidth := lipgloss.Width(prefix)
		baseStyle := ItemDetailStyle.Background(ColorSelectionBg)
		bgStyle := lipgloss.NewStyle().Background(ColorSelectionBg)

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
		return bgStyle.Render("  ") + SelectedBarStyle.Render("▌") + content + bgStyle.Render(strings.Repeat(" ", padWidth))
	}

	// Unselected
	prefix := "      " + icon + " "
	if doHighlight && query != "" {
		return ItemDetailStyle.Render(prefix) + highlightMatch(truncated, query, ItemDetailStyle)
	}
	return ItemDetailStyle.Render(prefix + truncated)
}

// renderSubtitleTwoLines renders up to two lines for a subtitle, word-wrapping
// at word boundaries. The first line gets the icon; the second is indented with
// spaces matching the icon's width.
func (m ListModel) renderSubtitleTwoLines(text, query, icon string, isSelected, doHighlight bool) string {
	msgWidth := m.subtitleMsgWidth(icon, isSelected)
	if msgWidth < 1 {
		return m.renderSubtitleLine(text, query, icon, isSelected, doHighlight)
	}

	// Word-wrap at word boundary to split into two lines
	line1, rest := wordWrapFirst(text, msgWidth)
	if rest == "" {
		return m.renderSubtitleLine(text, query, icon, isSelected, doHighlight)
	}

	// Render first line, second line with blank icon of same width
	first := m.renderSubtitleLine(line1, query, icon, isSelected, doHighlight)
	blankIcon := strings.Repeat(" ", lipgloss.Width(icon))
	second := m.renderSubtitleLine(rest, query, blankIcon, isSelected, doHighlight)
	return first + "\n" + second
}

// hasBadges returns true if the session has any outcome badges to display.
// Cheaper than renderBadges — no string allocation, just field checks.
func hasBadges(s claude.ClaudeSession) bool {
	return (s.LastActionCommit && s.Status == claude.StatusUserTurn) ||
		(s.StopReason != "" && s.Status == claude.StatusUserTurn) ||
		s.CompactCount > 0
}

// renderBadges returns inline outcome indicators for a session entry.
// Returns empty string if no badges apply.
func renderBadges(s claude.ClaudeSession) string {
	var badges []string
	if s.LastActionCommit && s.Status == claude.StatusUserTurn {
		badges = append(badges, DiffAddedStyle.Render(IconGitCommit+" committed"))
	}
	if s.StopReason != "" && s.Status == claude.StatusUserTurn {
		badges = append(badges, StatDoneStyle.Render(s.StopReason))
	}
	if s.CompactCount > 0 {
		badges = append(badges, ItemDetailStyle.Render(fmt.Sprintf("%s%d", IconCompact, s.CompactCount)))
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
	// Waiting state: static icon (no spinner) — ball is in YOUR court
	if s.IsWaiting {
		return bg(StatWaitingStyle).Render(IconWaiting)
	}
	switch s.Status {
	case claude.StatusUserTurn:
		age := formatAge(s.LastChanged)
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

// PaneIDAtLine maps a terminal line index (relative to list content start) to
// the PaneID of the session rendered at that line. Returns "" for headers,
// separators, or out-of-bounds lines. Mirrors View()'s group/filter/item logic.
func (m ListModel) PaneIDAtLine(line int) string {
	if line < 0 || len(m.allSorted) == 0 {
		return ""
	}

	query := strings.ToLower(m.narrow)
	currentLine := 0
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

// itemLineCount returns the number of terminal lines a rendered item occupies.
// Must stay in sync with renderItem's subtitle appendages.
func (m ListModel) itemLineCount(s claude.ClaudeSession, query string) int {
	count := 1 // main line

	if m.summaryLoadingPanes[s.PaneID] {
		count++
	}

	if s.QueuePending != "" {
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
