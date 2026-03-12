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

// Cached worktree icon styles (avoids per-frame style allocation in renderItem).
var (
	worktreeIconStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
	worktreeIconRendered = worktreeIconStyle.Render(IconWorktree) + " "
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

type SidebarModel struct {
	items               []claude.ClaudeSession
	filtered            []claude.ClaudeSession // cursor-navigable matching items
	allSorted           []claude.ClaudeSession // all items sorted (for stable group rendering)
	matchSet            map[string]bool        // PaneIDs of narrow-matching items; nil = all match
	matchScores         map[string]int         // PaneID → best fuzzy score (only during search)
	cursor              int
	height              int
	width               int
	narrow              string
	spinnerView         string
	commitDoneFrame     int
	diffStats           map[string]map[string]claude.FileDiffStat // sessionID -> file stats
	summaryLoadingPanes map[string]bool                           // pane IDs with in-flight synthesization
	groupByProject      bool
	deselected          bool // when true, SelectedItem() returns false (minimap on non-Claude pane)
	selectionLevel      SelectionLevel
	projectCursor       int
	projects            []projectEntry   // project headers in display order
	selectedProjectRow  int              // line index of the selected project header (set during View)
	selectedItemRow     int              // line index of the selected session item (set during View)
	backlogs            []claude.Backlog // all backlog items from visible projects
	filteredBacklog     []claude.Backlog // backlog items matching narrow filter
	backlogExpanded     bool             // true = BACKLOG section visible
	laterExpanded       bool             // true = LATER section visible
	laterCount          int              // cached count of Later sessions (updated in applyNarrow)
	landPaneID          string           // pane most recently jumped to (landing flash)
	landFrame           int              // landing animation frame (0–3 visible, 4 = clear)
	trailPaneID         string           // pane most recently jumped from (ghost trail)
	trailFrame          int              // trail animation frame (0–3 visible, 4 = clear)
	inlineTagSessionID  string           // session with active inline tag input (empty = none)
	inlineTagInputView  string           // rendered textinput view for the active tag session
}

// SetLand marks paneID as the landing target for the jump-arrival animation.
func (m *SidebarModel) SetLand(paneID string) {
	m.landPaneID = paneID
	m.landFrame = 0
}

// SetTrail marks paneID as the departure origin for the ghost-trail animation.
func (m *SidebarModel) SetTrail(paneID string) {
	m.trailPaneID = paneID
	m.trailFrame = 0
}

func (m *SidebarModel) SetGroupByProject(v bool) {
	m.groupByProject = v
	m.applyNarrow()
}

func (m SidebarModel) GroupByProject() bool {
	return m.groupByProject
}

func (m *SidebarModel) SetBacklogExpanded(v bool) {
	m.backlogExpanded = v
	m.applyNarrowBacklog()
	m.rebuildProjects()
}

func (m SidebarModel) BacklogExpanded() bool {
	return m.backlogExpanded
}

func (m *SidebarModel) SetLaterExpanded(v bool) {
	m.laterExpanded = v
	m.applyNarrow()
}

func (m SidebarModel) LaterExpanded() bool {
	return m.laterExpanded
}

func NewSidebarModel() SidebarModel {
	return SidebarModel{
		diffStats:           make(map[string]map[string]claude.FileDiffStat),
		summaryLoadingPanes: make(map[string]bool),
		laterExpanded:       true,
	}
}

func (m SidebarModel) SummaryLoadingCount() int {
	return len(m.summaryLoadingPanes)
}

// SetSummaryLoading sets immediate client-side loading state for instant UI feedback.
func (m *SidebarModel) SetSummaryLoading(paneID string, loading bool) {
	if m.summaryLoadingPanes == nil {
		m.summaryLoadingPanes = make(map[string]bool)
	}
	if loading {
		m.summaryLoadingPanes[paneID] = true
	} else {
		delete(m.summaryLoadingPanes, paneID)
	}
}

func (m *SidebarModel) SetDiffStats(sessionID string, stats map[string]claude.FileDiffStat) {
	if m.diffStats == nil {
		m.diffStats = make(map[string]map[string]claude.FileDiffStat)
	}
	m.diffStats[sessionID] = stats
}

// commitDoneFrames is a distinct animation for commit-and-done pending sessions.
var commitDoneFrames = []string{"◐", "◓", "◑", "◒"}

const jumpAnimFrames = 4 // visible frames (0–3), cleared at 4

func (m *SidebarModel) SetSpinnerView(s string) {
	m.spinnerView = s
	m.commitDoneFrame = (m.commitDoneFrame + 1) % len(commitDoneFrames)
	if m.landPaneID != "" {
		m.landFrame++
		if m.landFrame >= jumpAnimFrames {
			m.landPaneID = ""
		}
	}
	if m.trailPaneID != "" {
		m.trailFrame++
		if m.trailFrame >= jumpAnimFrames {
			m.trailPaneID = ""
		}
	}
}

func (m *SidebarModel) SetItems(items []claude.ClaudeSession) {
	// Remember currently selected PaneID or backlog ID before rebuilding
	var selectedPaneID string
	var selectedBacklogID string
	if m.IsBacklogSelected() {
		if backlog, ok := m.SelectedBacklog(); ok {
			selectedBacklogID = backlog.ID
		}
	} else if m.cursor >= 0 && m.cursor < len(m.filtered) {
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

	// Restore selection: backlog first, then session, then clamp
	if selectedBacklogID != "" {
		if m.selectByBacklogID(selectedBacklogID) {
			return
		}
	}
	if selectedPaneID == "" || !m.SelectByPaneID(selectedPaneID) {
		total := m.totalItems()
		if total > 0 && m.cursor >= total {
			m.cursor = total - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
	}
}

func (m *SidebarModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *SidebarModel) SetInlineTagInput(sessionID, view string) {
	m.inlineTagSessionID = sessionID
	m.inlineTagInputView = view
}

func (m *SidebarModel) SetNarrow(f string) {
	m.narrow = f
	m.applyNarrow()
	m.cursor = 0
}

func (m *SidebarModel) ClearNarrow() {
	m.narrow = ""
	m.applyNarrow()
	m.cursor = 0
}

// CursorRef is a type-agnostic reference to a cursor position.
// Used to save/restore the cursor across list mutations (e.g., clearing search).
type CursorRef struct {
	PaneID    string // non-empty for session items
	BacklogID string // non-empty for backlog items
}

// CursorRef returns a reference to whatever is currently under the cursor.
func (m SidebarModel) CursorRef() CursorRef {
	if s, ok := m.SelectedItem(); ok {
		return CursorRef{PaneID: s.PaneID}
	}
	if b, ok := m.SelectedBacklog(); ok {
		return CursorRef{BacklogID: b.ID}
	}
	return CursorRef{}
}

// SelectByRef restores the cursor to the item identified by ref. Returns true if found.
func (m *SidebarModel) SelectByRef(ref CursorRef) bool {
	switch {
	case ref.PaneID != "":
		return m.SelectByPaneID(ref.PaneID)
	case ref.BacklogID != "":
		return m.selectByBacklogID(ref.BacklogID)
	default:
		return false
	}
}

func (m SidebarModel) SelectedItem() (claude.ClaudeSession, bool) {
	if m.deselected || m.cursor >= len(m.filtered) || len(m.filtered) == 0 {
		return claude.ClaudeSession{}, false
	}
	return m.filtered[m.cursor], true
}

// totalItems returns the combined count of sessions + backlog items for cursor range.
func (m SidebarModel) totalItems() int {
	return len(m.filtered) + len(m.filteredBacklog)
}

// IsBacklogSelected returns true if the cursor is in the backlog zone.
func (m SidebarModel) IsBacklogSelected() bool {
	return !m.deselected && len(m.filteredBacklog) > 0 && m.cursor >= len(m.filtered)
}

// SelectedBacklog returns the backlog item at the cursor, if in the backlog zone.
func (m SidebarModel) SelectedBacklog() (claude.Backlog, bool) {
	if !m.IsBacklogSelected() {
		return claude.Backlog{}, false
	}
	idx := m.cursor - len(m.filtered)
	if idx >= len(m.filteredBacklog) {
		return claude.Backlog{}, false
	}
	return m.filteredBacklog[idx], true
}

// SetBacklog stores backlog items, sorts by project then CreatedAt, applies narrow.
func (m *SidebarModel) SetBacklog(backlogs []claude.Backlog) {
	// Preserve selected backlog item across refresh
	var selectedBacklogID string
	if m.IsBacklogSelected() {
		if backlog, ok := m.SelectedBacklog(); ok {
			selectedBacklogID = backlog.ID
		}
	}

	m.backlogs = backlogs
	m.applyNarrowBacklog()
	m.rebuildProjects()

	if selectedBacklogID != "" {
		m.selectByBacklogID(selectedBacklogID)
	}
}

// selectByBacklogID sets the cursor to the backlog item with the given ID. Returns true if found.
func (m *SidebarModel) selectByBacklogID(id string) bool {
	for i, backlog := range m.filteredBacklog {
		if backlog.ID == id {
			m.cursor = len(m.filtered) + i
			m.deselected = false
			return true
		}
	}
	return false
}

// applyNarrowBacklog filters backlog items by the current narrow query.
func (m *SidebarModel) applyNarrowBacklog() {
	if !m.backlogExpanded {
		m.filteredBacklog = nil
		return
	}
	if m.narrow == "" {
		m.filteredBacklog = make([]claude.Backlog, len(m.backlogs))
		copy(m.filteredBacklog, m.backlogs)
	} else {
		f := strings.ToLower(m.narrow)
		m.filteredBacklog = nil
		for _, backlog := range m.backlogs {
			title := strings.ToLower(backlog.DisplayTitle())
			body := strings.ToLower(backlog.Body)
			if strings.Contains(title, f) || strings.Contains(body, f) {
				m.filteredBacklog = append(m.filteredBacklog, backlog)
			}
		}
	}
}

// Deselect marks the list as having no active selection (minimap on non-Claude pane).
func (m *SidebarModel) Deselect() {
	m.deselected = true
}

// Reselect restores the list selection after Deselect.
func (m *SidebarModel) Reselect() {
	m.deselected = false
}

func (m *SidebarModel) SelectByPaneID(paneID string) bool {
	for i, s := range m.filtered {
		if s.PaneID == paneID {
			m.cursor = i
			m.deselected = false
			return true
		}
	}
	return false
}

func (m *SidebarModel) MoveUp() {
	m.deselected = false
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *SidebarModel) MoveDown() {
	m.deselected = false
	if m.cursor < m.totalItems()-1 {
		m.cursor++
	}
}

func (m *SidebarModel) MoveToTop() {
	m.deselected = false
	m.cursor = 0
}

func (m *SidebarModel) MoveToBottom() {
	m.deselected = false
	total := m.totalItems()
	if total > 0 {
		m.cursor = total - 1
	}
}

// SelectionLevel returns the current navigation level.
func (m SidebarModel) SelectionLevel() SelectionLevel {
	return m.selectionLevel
}

// SelectedProjectRow returns the line index of the selected project header
// within the list's rendered output. Returns -1 if no project is selected.
func (m SidebarModel) SelectedProjectRow() int {
	return m.selectedProjectRow
}

// SelectedItemRow returns the line index of the selected session item
// within the list's rendered output. Returns -1 if no session item is selected.
func (m SidebarModel) SelectedItemRow() int {
	return m.selectedItemRow
}

// EnterProjectLevel switches to project-level navigation.
// The project cursor is set to the project entry matching the currently selected session.
func (m *SidebarModel) EnterProjectLevel() {
	if len(m.projects) == 0 {
		return
	}
	m.selectionLevel = LevelProject
	// Derive project cursor from current backlog item
	if m.IsBacklogSelected() {
		if backlog, ok := m.SelectedBacklog(); ok {
			for i, p := range m.projects {
				if p.Name == backlog.Project && p.StatusOrder == OrderBacklog {
					m.projectCursor = i
					return
				}
			}
		}
	}
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
func (m *SidebarModel) EnterSessionLevel() {
	if m.selectionLevel != LevelProject {
		return
	}
	m.selectionLevel = LevelSession
	if pe, ok := m.SelectedProject(); ok {
		// If this is a backlog project, jump to first backlog item in that project
		if pe.StatusOrder == OrderBacklog {
			for i, backlog := range m.filteredBacklog {
				if backlog.Project == pe.Name {
					m.cursor = len(m.filtered) + i
					m.deselected = false
					return
				}
			}
		}
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
func (m *SidebarModel) MoveUpProject() {
	if m.projectCursor > 0 {
		m.projectCursor--
	}
}

// MoveDownProject moves the project cursor down.
func (m *SidebarModel) MoveDownProject() {
	if m.projectCursor < len(m.projects)-1 {
		m.projectCursor++
	}
}

// SelectedProject returns the currently selected project entry when at project level.
func (m SidebarModel) SelectedProject() (projectEntry, bool) {
	if m.selectionLevel != LevelProject || len(m.projects) == 0 {
		return projectEntry{}, false
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor], true
	}
	return projectEntry{}, false
}

// FirstSessionInProject returns the first session matching a project entry.
func (m SidebarModel) FirstSessionInProject(pe projectEntry) (claude.ClaudeSession, bool) {
	for _, s := range m.filtered {
		if pe.matches(s) {
			return s, true
		}
	}
	return claude.ClaudeSession{}, false
}

// SelectedProjectSession returns the first session in the currently selected project.
// Convenience method collapsing SelectedProject + FirstSessionInProject.
func (m SidebarModel) SelectedProjectSession() (claude.ClaudeSession, bool) {
	pe, ok := m.SelectedProject()
	if !ok {
		return claude.ClaudeSession{}, false
	}
	return m.FirstSessionInProject(pe)
}

// SessionsInProject returns all sessions matching a project entry.
func (m SidebarModel) SessionsInProject(pe projectEntry) []claude.ClaudeSession {
	var result []claude.ClaudeSession
	for _, s := range m.filtered {
		if pe.matches(s) {
			result = append(result, s)
		}
	}
	return result
}

func (m SidebarModel) Items() []claude.ClaudeSession {
	return m.filtered
}

// BacklogsInProject returns all backlog items matching a project name.
func (m SidebarModel) BacklogsInProject(projectName string) []claude.Backlog {
	var result []claude.Backlog
	for _, backlog := range m.filteredBacklog {
		if backlog.Project == projectName {
			result = append(result, backlog)
		}
	}
	return result
}

// FirstBacklogCWDInProject returns the CWD from the first backlog item in a project.
func (m SidebarModel) FirstBacklogCWDInProject(projectName string) string {
	for _, backlog := range m.filteredBacklog {
		if backlog.Project == projectName {
			return backlog.CWD
		}
	}
	return ""
}

// AutoJumpTargetFromCursor returns the auto-jump target, skipping the currently
// selected session. Returns "" if no target exists.
func (m SidebarModel) AutoJumpTargetFromCursor() string {
	var skipPaneID string
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		skipPaneID = m.filtered[m.cursor].PaneID
	}
	return m.AutoJumpTarget(skipPaneID)
}

// AutoJumpTarget finds the best auto-jump target, skipping skipPaneID.
// Returns the user-turn session with the oldest LastChanged (waiting longest).
// Excludes Later-bookmarked sessions. Returns "" if no user-turn target exists.
func (m SidebarModel) AutoJumpTarget(skipPaneID string) string {
	var bestUser string
	var bestUserTime time.Time

	for _, s := range m.filtered {
		if s.PaneID == skipPaneID || s.LaterBookmarkID != "" || s.LastChanged.IsZero() {
			continue
		}
		if s.Status == claude.StatusUserTurn {
			if bestUser == "" || s.LastChanged.Before(bestUserTime) {
				bestUser = s.PaneID
				bestUserTime = s.LastChanged
			}
		}
	}
	return bestUser
}

func (m *SidebarModel) applyNarrow() {
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
	// Count Later sessions and remove them from the cursor-navigable list when collapsed
	m.laterCount = 0
	if !m.laterExpanded {
		n := 0
		for _, s := range m.filtered {
			if sessionOrder(s) == OrderLater {
				m.laterCount++
			} else {
				m.filtered[n] = s
				n++
			}
		}
		m.filtered = m.filtered[:n]
	}
	m.applyNarrowBacklog()
	m.rebuildProjects()
}

// bestNarrowScore returns the best fuzzy score of query across the session's searchable fields.
// Returns -1 if no field matches.
func bestNarrowScore(s claude.ClaudeSession, query string) int {
	best := -1
	for _, text := range []string{s.CustomTitle, s.Headline, s.FirstMessage, s.LastUserMessage, s.ProblemType} {
		if score := fuzzyScore(text, query); score > best {
			best = score
		}
	}
	for _, tag := range s.Tags {
		if score := fuzzyScore("#"+tag, query); score > best {
			best = score
		}
	}
	return best
}

// rebuildProjects extracts project entries in display order from the filtered list.
// In project-group mode: one entry per unique project name (StatusOrder=-1).
// In status-group mode: one entry per (project, statusGroup) pair.
func (m *SidebarModel) rebuildProjects() {
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

	// Add backlog projects
	for _, backlog := range m.filteredBacklog {
		k := key{backlog.Project, OrderBacklog}
		if !seen[k] {
			seen[k] = true
			m.projects = append(m.projects, projectEntry{Name: backlog.Project, StatusOrder: OrderBacklog})
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
	// tertiary: newest created first (newest at top)
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
	// Primary: session order (UserTurn, AgentTurn, Later); secondary: newest created first (newest at top)
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
	OrderBacklog   = 4
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

func (m SidebarModel) computeDiffColWidths() diffColWidths {
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

	// Pin collapsed section badges at the bottom (backlog and/or later when hidden)
	laterCount := m.laterCount // cached by applyNarrow; non-zero only when !laterExpanded
	backlogCount := 0
	if !m.backlogExpanded {
		backlogCount = len(m.backlogs)
	}
	if (laterCount > 0 || backlogCount > 0) && m.height > 0 {
		var parts []string
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

// SelectByBacklogID sets the cursor to the backlog item with the given ID.
func (m *SidebarModel) SelectByBacklogID(id string) bool {
	return m.selectByBacklogID(id)
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
