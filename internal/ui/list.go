package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/claude"
)

// SelectionLevel tracks whether the cursor is on a session or a project group.
type SelectionLevel int

const (
	LevelSession SelectionLevel = iota
	LevelProject
)

// projectEntry identifies a project header as it appears in the rendered list.
// In status-group mode the same project name can appear under multiple status groups.
type projectEntry struct {
	Name        string
	StatusOrder int // -1 in project-group mode; sessionOrder value in status-group mode
}

// matches returns true if the session belongs to this project entry.
func (pe projectEntry) matches(s claude.ClaudeSession) bool {
	if pe.StatusOrder == -1 {
		return s.Project == pe.Name && sessionOrder(s) != OrderLater
	}
	return s.Project == pe.Name && sessionOrder(s) == pe.StatusOrder
}

// selBg adds avatar-tinted background to st when selected, otherwise returns st unchanged.
func selBg(st lipgloss.Style, selected bool, colorIdx int) lipgloss.Style {
	if selected {
		return st.Background(AvatarFillBg(colorIdx))
	}
	return st
}

type ListModel struct {
	items                []claude.ClaudeSession
	filtered             []claude.ClaudeSession            // cursor-navigable matching items
	allSorted            []claude.ClaudeSession            // all items sorted (for stable group rendering)
	matchSet             map[string]bool                   // PaneIDs of narrow-matching items; nil = all match
	matchScores          map[string]int                    // PaneID → best fuzzy score (only during search)
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
	selectionLevel       SelectionLevel
	projectCursor        int
	projects             []projectEntry // project headers in display order
	selectedProjectRow   int            // line index of the selected project header (set during View)
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

func (m *ListModel) MoveToTop() {
	m.deselected = false
	m.cursor = 0
}

func (m *ListModel) MoveToBottom() {
	m.deselected = false
	if len(m.filtered) > 0 {
		m.cursor = len(m.filtered) - 1
	}
}

// SelectionLevel returns the current navigation level.
func (m ListModel) SelectionLevel() SelectionLevel {
	return m.selectionLevel
}

// SelectedProjectRow returns the line index of the selected project header
// within the list's rendered output. Returns -1 if no project is selected.
func (m ListModel) SelectedProjectRow() int {
	return m.selectedProjectRow
}

// EnterProjectLevel switches to project-level navigation.
// The project cursor is set to the project entry matching the currently selected session.
func (m *ListModel) EnterProjectLevel() {
	if len(m.projects) == 0 {
		return
	}
	m.selectionLevel = LevelProject
	// Derive project cursor from current session
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		s := m.filtered[m.cursor]
		order := -1
		if !m.groupByProject {
			order = sessionOrder(s)
		} else if sessionOrder(s) == OrderLater {
			order = OrderLater
		}
		for i, p := range m.projects {
			if p.Name == s.Project && p.StatusOrder == order {
				m.projectCursor = i
				return
			}
		}
	}
	m.projectCursor = 0
}

// EnterSessionLevel switches to session-level navigation.
// The cursor moves to the first session matching the selected project entry.
func (m *ListModel) EnterSessionLevel() {
	if m.selectionLevel != LevelProject {
		return
	}
	m.selectionLevel = LevelSession
	if pe, ok := m.SelectedProject(); ok {
		for i, s := range m.filtered {
			if pe.matches(s) {
				m.cursor = i
				m.deselected = false
				return
			}
		}
	}
}

// MoveUpProject moves the project cursor up.
func (m *ListModel) MoveUpProject() {
	if m.projectCursor > 0 {
		m.projectCursor--
	}
}

// MoveDownProject moves the project cursor down.
func (m *ListModel) MoveDownProject() {
	if m.projectCursor < len(m.projects)-1 {
		m.projectCursor++
	}
}

// SelectedProject returns the currently selected project entry when at project level.
func (m ListModel) SelectedProject() (projectEntry, bool) {
	if m.selectionLevel != LevelProject || len(m.projects) == 0 {
		return projectEntry{}, false
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor], true
	}
	return projectEntry{}, false
}

// FirstSessionInProject returns the first session matching a project entry.
func (m ListModel) FirstSessionInProject(pe projectEntry) (claude.ClaudeSession, bool) {
	for _, s := range m.filtered {
		if pe.matches(s) {
			return s, true
		}
	}
	return claude.ClaudeSession{}, false
}

// SelectedProjectSession returns the first session in the currently selected project.
// Convenience method collapsing SelectedProject + FirstSessionInProject.
func (m ListModel) SelectedProjectSession() (claude.ClaudeSession, bool) {
	pe, ok := m.SelectedProject()
	if !ok {
		return claude.ClaudeSession{}, false
	}
	return m.FirstSessionInProject(pe)
}

// SessionsInProject returns all sessions matching a project entry.
func (m ListModel) SessionsInProject(pe projectEntry) []claude.ClaudeSession {
	var result []claude.ClaudeSession
	for _, s := range m.filtered {
		if pe.matches(s) {
			result = append(result, s)
		}
	}
	return result
}

func (m ListModel) Items() []claude.ClaudeSession {
	return m.filtered
}

// SnapTargetFromCursor returns the snap target, skipping the currently selected
// session. Returns "" if no target exists.
func (m ListModel) SnapTargetFromCursor() string {
	var skipPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		skipPaneID = m.filtered[m.cursor].PaneID
	}
	return m.SnapTarget(skipPaneID)
}

// SnapTarget finds the best snap-to-default target, skipping skipPaneID.
// Priority: user-turn with oldest LastChanged (waiting longest), then agent-turn
// with oldest LastChanged. Excludes Later-bookmarked sessions.
func (m ListModel) SnapTarget(skipPaneID string) string {
	var bestUser, bestAgent string
	var bestUserTime, bestAgentTime time.Time

	for _, s := range m.filtered {
		if s.PaneID == skipPaneID || s.LaterBookmarkID != "" || s.LastChanged.IsZero() {
			continue
		}
		switch s.Status {
		case claude.StatusUserTurn:
			if bestUser == "" || s.LastChanged.Before(bestUserTime) {
				bestUser = s.PaneID
				bestUserTime = s.LastChanged
			}
		case claude.StatusAgentTurn:
			if bestAgent == "" || s.LastChanged.Before(bestAgentTime) {
				bestAgent = s.PaneID
				bestAgentTime = s.LastChanged
			}
		}
	}
	if bestUser != "" {
		return bestUser
	}
	return bestAgent
}

func (m *ListModel) applyNarrow() {
	// Always maintain a sorted copy of all items for stable group rendering
	m.allSorted = make([]claude.ClaudeSession, len(m.items))
	copy(m.allSorted, m.items)

	if m.narrow == "" {
		m.filtered = make([]claude.ClaudeSession, len(m.items))
		copy(m.filtered, m.items)
		m.matchSet = nil // nil = all match
		m.matchScores = nil
	} else {
		f := strings.ToLower(m.narrow)
		m.filtered = nil
		m.matchSet = make(map[string]bool)
		m.matchScores = make(map[string]int)
		for _, s := range m.items {
			best := bestNarrowScore(s, f)
			if best >= 0 {
				m.filtered = append(m.filtered, s)
				m.matchSet[s.PaneID] = true
				m.matchScores[s.PaneID] = best
			}
		}
		sort.SliceStable(m.filtered, func(i, j int) bool {
			return m.matchScores[m.filtered[i].PaneID] > m.matchScores[m.filtered[j].PaneID]
		})
	}
	if m.groupByProject {
		sortByProject(m.allSorted)
	} else {
		sortByStatus(m.allSorted)
	}
	// When not searching, sort filtered same as allSorted
	if m.narrow == "" {
		if m.groupByProject {
			sortByProject(m.filtered)
		} else {
			sortByStatus(m.filtered)
		}
	}
	m.rebuildProjects()
}

// bestNarrowScore returns the best fuzzy score of query across the session's searchable fields.
// Returns -1 if no field matches.
func bestNarrowScore(s claude.ClaudeSession, query string) int {
	best := -1
	for _, text := range []string{s.CustomTitle, s.Headline, s.FirstMessage, s.LastUserMessage} {
		if score := fuzzyScore(text, query); score > best {
			best = score
		}
	}
	return best
}

// rebuildProjects extracts project entries in display order from the filtered list.
// In project-group mode: one entry per unique project name (StatusOrder=-1).
// In status-group mode: one entry per (project, statusGroup) pair.
func (m *ListModel) rebuildProjects() {
	var prev projectEntry
	havePrev := m.projectCursor >= 0 && m.projectCursor < len(m.projects)
	if havePrev {
		prev = m.projects[m.projectCursor]
	}

	type key struct {
		name  string
		order int
	}
	seen := make(map[key]bool)
	m.projects = nil

	for _, s := range m.filtered {
		order := -1
		if !m.groupByProject {
			order = sessionOrder(s)
		} else if sessionOrder(s) == OrderLater {
			order = OrderLater
		}
		k := key{s.Project, order}
		if !seen[k] {
			seen[k] = true
			m.projects = append(m.projects, projectEntry{Name: s.Project, StatusOrder: order})
		}
	}

	// Restore project cursor by matching previous entry
	if havePrev {
		for i, p := range m.projects {
			if p == prev {
				m.projectCursor = i
				return
			}
		}
	}
	if m.projectCursor >= len(m.projects) {
		m.projectCursor = max(len(m.projects)-1, 0)
	}
}

func sortByProject(sessions []claude.ClaudeSession) {
	// Primary: Later sessions sink to bottom; secondary: project name alphabetically;
	// tertiary: newest created first
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0; j-- {
			a, b := sessions[j-1], sessions[j]
			aLater := sessionOrder(a) == OrderLater
			bLater := sessionOrder(b) == OrderLater
			if (aLater && !bLater) ||
				(aLater == bLater && a.Project > b.Project) ||
				(aLater == bLater && a.Project == b.Project && a.CreatedAt.Before(b.CreatedAt)) {
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

	// Pre-calculate snap-to-default target
	snapTargetID := m.SnapTargetFromCursor()

	// Project-level selection
	selectedProject, atProjectLevel := m.SelectedProject()

	var lines []string
	m.selectedProjectRow = -1

	if m.narrow != "" {
		// Search mode: render from m.filtered directly (score-sorted, flat)
		for _, s := range m.filtered {
			isSelected := s.PaneID == selectedPaneID && !m.deselected
			isSnapTarget := !isSelected && s.PaneID == snapTargetID
			lines = append(lines, m.renderItem(isSelected, isSnapTarget, s, dw, query))
		}
	} else {
		currentProject := ""
		currentOrder := -1

		for _, s := range m.allSorted {
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
			isSnapTarget := !isSelected && s.PaneID == snapTargetID
			lines = append(lines, m.renderItem(isSelected, isSnapTarget, s, dw, query))
		}
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
	default:
		return ""
	}
}

func (m ListModel) renderItem(isSelected, isSnapTarget bool, s claude.ClaudeSession, dw diffColWidths, query string) string {

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
	avatarBg := AvatarFillBg(s.AvatarColorIdx)
	var selBgSt, barSt, snapBarSt lipgloss.Style
	if isSelected {
		selBgSt = lipgloss.NewStyle().Background(avatarBg)
		barSt = lipgloss.NewStyle().Foreground(avatarColor).Background(avatarBg)
	} else if isSnapTarget {
		snapBarSt = lipgloss.NewStyle().Foreground(avatarColor)
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
		bg := selBgSt
		var styledName string
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, bg)
		} else {
			styledName = bg.Render(displayName)
		}
		namePart = bg.Render("  ") +
			barSt.Render("▌") +
			bg.Render(" ") +
			AvatarStyle(s.AvatarColorIdx).Background(avatarBg).Render(glyph + "  ") +
			styledName
		gapStr = bg.Render(strings.Repeat(" ", gap))
	} else {
		var styledName string
		if hasQuery && !isNewSession {
			styledName = highlightMatch(displayName, query, lipgloss.NewStyle())
		} else {
			styledName = displayName
		}
		if isSnapTarget {
			namePart = "  " + snapBarSt.Render("▯") + " " + iconStr + styledName
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
			withBg(style).Width(m.width - 5).Render(content)
	}

	// snapSubtitle wraps an unselected subtitle with the snap-target bar at col 2.
	// Visually: "  │" + content — same width as "      " (6 spaces).
	snapSubtitle := func(style lipgloss.Style, content string) string {
		return "  " + snapBarSt.Render("▯") + style.Render("   "+content)
	}

	if m.summaryLoadingPanes[s.PaneID] {
		if isSelected {
			line += "\n" + selSubtitle(selBgSt.Foreground(ColorMuted).Italic(true), "   "+m.spinnerView+" synthesizing…")
		} else if isSnapTarget {
			line += "\n" + snapSubtitle(SummaryStyle, m.spinnerView+" synthesizing…")
		} else {
			line += "\n" + SummaryStyle.Render("      "+m.spinnerView+" synthesizing…")
		}
	}

	// Show queue badge with count
	if len(s.QueuePending) > 0 {
		queueBadge := fmt.Sprintf("%s %d", IconQueue, len(s.QueuePending))
		line += "\n" + m.renderSubtitleLine(queueBadge, query, "", isSelected, isSnapTarget, false, s.AvatarColorIdx)
	}

	// Show last user message as subtitle (up to two lines, word-wrapped)
	if s.LastUserMessage != "" {
		rawMsg := strings.ReplaceAll(s.LastUserMessage, "\n", " ")
		doHL := hasQuery && matchesNarrow(s.LastUserMessage, query)
		line += "\n" + m.renderSubtitleTwoLines(rawMsg, query, IconQuote, isSelected, isSnapTarget, doHL, s.AvatarColorIdx)
	}

	// Match-context subtitles: show non-visible fields that matched the search
	if hasQuery {
		// Headline: shown when it's not the display name (i.e. customTitle is set) and matches
		if s.Headline != "" && s.CustomTitle != "" && matchesNarrow(s.Headline, query) {
			line += "\n" + m.renderSubtitleLine(s.Headline, query, IconHeadline, isSelected, isSnapTarget, true, s.AvatarColorIdx)
		}
		// FirstMessage: shown when it's not the display name (customTitle or headline is set) and matches
		if s.FirstMessage != "" && (s.CustomTitle != "" || s.Headline != "") && matchesNarrow(s.FirstMessage, query) {
			rawFirst := strings.ReplaceAll(s.FirstMessage, "\n", " ")
			line += "\n" + m.renderSubtitleLine(rawFirst, query, IconQuote, isSelected, isSnapTarget, true, s.AvatarColorIdx)
		}
	}

	// Badges line — outcome indicators (git commit, etc.)
	if badges := renderBadges(s); badges != "" {
		if isSelected {
			line += "\n" + selSubtitle(ItemDetailStyle, "   "+badges)
		} else if isSnapTarget {
			line += "\n" + snapSubtitle(ItemDetailStyle, badges)
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
func (m ListModel) renderSubtitleLine(text, query, icon string, isSelected, isSnapTarget, doHighlight bool, avatarColorIdx int) string {
	msgWidth := m.subtitleMsgWidth(icon, isSelected)
	truncated := ansi.Truncate(text, msgWidth, "…")

	if isSelected {
		prefix := "   " + icon + " "
		prefixWidth := lipgloss.Width(prefix)
		fillBg := AvatarFillBg(avatarColorIdx)
		baseStyle := ItemDetailStyle.Background(fillBg)
		bgStyle := lipgloss.NewStyle().Background(fillBg)
		localBarSt := lipgloss.NewStyle().Foreground(AvatarColor(avatarColorIdx)).Background(fillBg)

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
		return bgStyle.Render("  ") + localBarSt.Render("▌") + content + bgStyle.Render(strings.Repeat(" ", padWidth))
	}

	// Unselected — with optional snap-target bar at col 2
	if isSnapTarget {
		localSnapSt := lipgloss.NewStyle().Foreground(AvatarColor(avatarColorIdx))
		prefix := "   " + icon + " "
		if doHighlight && query != "" {
			return "  " + localSnapSt.Render("▯") + ItemDetailStyle.Render(prefix) + highlightMatch(truncated, query, ItemDetailStyle)
		}
		return "  " + localSnapSt.Render("▯") + ItemDetailStyle.Render(prefix+truncated)
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
func (m ListModel) renderSubtitleTwoLines(text, query, icon string, isSelected, isSnapTarget, doHighlight bool, avatarColorIdx int) string {
	msgWidth := m.subtitleMsgWidth(icon, isSelected)
	if msgWidth < 1 {
		return m.renderSubtitleLine(text, query, icon, isSelected, isSnapTarget, doHighlight, avatarColorIdx)
	}

	// Word-wrap at word boundary to split into two lines
	line1, rest := wordWrapFirst(text, msgWidth)
	if rest == "" {
		return m.renderSubtitleLine(text, query, icon, isSelected, isSnapTarget, doHighlight, avatarColorIdx)
	}

	// Render first line, second line with blank icon of same width
	first := m.renderSubtitleLine(line1, query, icon, isSelected, isSnapTarget, doHighlight, avatarColorIdx)
	blankIcon := strings.Repeat(" ", lipgloss.Width(icon))
	second := m.renderSubtitleLine(rest, query, blankIcon, isSelected, isSnapTarget, doHighlight, avatarColorIdx)
	return first + "\n" + second
}

// hasBadges returns true if the session has any outcome badges to display.
// Cheaper than renderBadges — no string allocation, just field checks.
func hasBadges(s claude.ClaudeSession) bool {
	return (s.LastActionCommit && s.Status == claude.StatusUserTurn) ||
		(s.StopReason != "" && s.Status == claude.StatusUserTurn) ||
		s.SkillName != "" ||
		s.ProblemType != "" ||
		s.CompactCount > 0
}

// renderBadges returns inline outcome indicators for a session entry.
// Returns empty string if no badges apply.
func renderBadges(s claude.ClaudeSession) string {
	var badges []string
	if s.LastActionCommit && s.Status == claude.StatusUserTurn {
		badges = append(badges, DiffAddedStyle.Render(IconGitCommit+" committed"))
	}
	// Skill badge shows during both agent-turn and user-turn — unlike outcome
	// badges (committed, stopReason) it's context about what was triggered,
	// not a result. Cleared on next non-skill prompt.
	if s.SkillName != "" {
		badges = append(badges, DiffAddedStyle.Render(IconSkill+" "+s.SkillName))
	}
	if s.StopReason != "" && s.Status == claude.StatusUserTurn {
		badges = append(badges, StatDoneStyle.Render(s.StopReason))
	}
	if s.ProblemType != "" {
		badges = append(badges, problemTypeBadge(s.ProblemType))
	}
	if s.CompactCount > 0 {
		badges = append(badges, ItemDetailStyle.Render(fmt.Sprintf("%s %d", IconCompact, s.CompactCount)))
	}
	if len(badges) == 0 {
		return ""
	}
	return strings.Join(badges, "  ")
}

// problemTypeBadge renders a color-coded pill for the synthesized problem type.
func problemTypeBadge(pt string) string {
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
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Render(" " + pt + " ")
}

func (m ListModel) renderDetail(s claude.ClaudeSession, selected bool) string {
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

// itemLineCount returns the number of terminal lines a rendered item occupies.
// Must stay in sync with renderItem's subtitle appendages.
func (m ListModel) itemLineCount(s claude.ClaudeSession, query string) int {
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
