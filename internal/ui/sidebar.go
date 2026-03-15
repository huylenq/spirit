package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

// Cached worktree icon styles (avoids per-frame style allocation in renderItem).
var (
	worktreeIconStyle    = lipgloss.NewStyle().Foreground(ColorMuted)
	worktreeIconRendered = worktreeIconStyle.Render(IconWorktree) + " "
	flagItemStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	slotItemStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#eab308"))
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
	claudingExpanded    bool             // true = CLAUDING section visible
	laterCount          int              // cached count of Later sessions (updated in applyNarrow)
	claudingCount       int              // cached count of Clauding sessions (updated in applyNarrow)
	landPaneID          string           // pane most recently jumped to (landing flash)
	landBacklogID       string           // backlog item most recently jumped to (landing flash)
	landFrame           int              // landing animation frame (counts up to landMaxFrames)
	landMaxFrames       int              // total frames for landing animation (default JumpAnimFrames)
	trailPaneID         string           // pane most recently jumped from (ghost trail)
	trailFrame          int              // trail animation frame (0–3 visible, 4 = clear)
	inlineTagSessionID  string           // session with active inline tag input (empty = none)
	inlineTagInputView  string           // rendered textinput view for the active tag session
	inlineTagBacklogID  string           // backlog item with active inline tag input (empty = none)
	ShowAutoJump        bool             // when false, suppress auto-jump visual indicator
	cardMode            bool             // when true, renderItem skips the selection bar prefix (used by RenderCard)
	flaggedSessions     map[string]bool  // paneID → flagged
	flaggedBacklogs     map[string]bool  // backlog ID → flagged
	numberSlots         map[int]string   // slot (1-9) → PaneID
}

// SetLand marks paneID as the landing target for the jump-arrival animation.
// frames controls duration: JumpAnimFrames for the standard flash, more for a longer highlight.
func (m *SidebarModel) SetLand(paneID string, frames int) {
	m.landPaneID = paneID
	m.landBacklogID = ""
	m.landFrame = 0
	m.landMaxFrames = frames
}

// SetLandBacklog marks a backlog item as the landing target for the jump-arrival animation.
// frames controls duration: JumpAnimFrames for the standard flash, more for a longer highlight.
func (m *SidebarModel) SetLandBacklog(backlogID string, frames int) {
	m.landBacklogID = backlogID
	m.landPaneID = ""
	m.landFrame = 0
	m.landMaxFrames = frames
}

// landT returns the blend parameter [0,1] for the landing animation.
// Extra frames (landMaxFrames > JumpAnimFrames) hold at t=0 (peak flash) before fading,
// so the fade rate is always the same regardless of total duration.
func (m SidebarModel) landT() float64 {
	holdFrames := m.landMaxFrames - JumpAnimFrames
	fadeFrame := m.landFrame - holdFrames
	if fadeFrame < 0 {
		fadeFrame = 0
	}
	return float64(fadeFrame) / float64(JumpAnimFrames-1)
}

// SetLandByRef triggers the landing animation for whatever item CursorRef points to.
func (m *SidebarModel) SetLandByRef(ref CursorRef, frames int) {
	switch {
	case ref.PaneID != "":
		m.SetLand(ref.PaneID, frames)
	case ref.BacklogID != "":
		m.SetLandBacklog(ref.BacklogID, frames)
	}
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

func (m *SidebarModel) SetClaudingExpanded(v bool) {
	m.claudingExpanded = v
	m.applyNarrow()
}

func (m SidebarModel) ClaudingExpanded() bool {
	return m.claudingExpanded
}

func (m SidebarModel) ClaudingCount() int { return m.claudingCount }
func (m SidebarModel) LaterCount() int    { return m.laterCount }
func (m SidebarModel) BacklogCount() int  { return len(m.backlogs) }

// IsAllQuiet returns true when sessions exist but none are cursor-navigable
// (all hidden behind collapsed sections, no YOUR TURN items, not in search mode).
func (m SidebarModel) IsAllQuiet() bool {
	return len(m.items) > 0 && len(m.filtered) == 0 && len(m.filteredBacklog) == 0 && m.narrow == ""
}

func NewSidebarModel() SidebarModel {
	return SidebarModel{
		diffStats:           make(map[string]map[string]claude.FileDiffStat),
		summaryLoadingPanes: make(map[string]bool),
		laterExpanded:       true,
		claudingExpanded:    true,
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

const (
	JumpAnimFrames     = 4  // visible frames for standard jump flash
	SearchFlashFrames  = 12 // longer hold for search-confirm landing
	ActivateAnimFrames = 8  // longer flash for ctrl+tab / ctrl+space activation
)

func (m *SidebarModel) SetSpinnerView(s string) {
	m.spinnerView = s
	m.commitDoneFrame = (m.commitDoneFrame + 1) % len(commitDoneFrames)
	if m.landPaneID != "" || m.landBacklogID != "" {
		m.landFrame++
		if m.landFrame >= m.landMaxFrames {
			m.landPaneID = ""
			m.landBacklogID = ""
		}
	}
	if m.trailPaneID != "" {
		m.trailFrame++
		if m.trailFrame >= JumpAnimFrames {
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

	// Clear slots and flags for terminated, phantom, or removed sessions.
	if len(m.numberSlots) > 0 || len(m.flaggedSessions) > 0 {
		aliveIDs := make(map[string]bool, len(items))
		for _, s := range items {
			if sessionOrder(s) != OrderOther && !s.IsPhantom {
				aliveIDs[s.PaneID] = true
			}
		}
		for slot, paneID := range m.numberSlots {
			if !aliveIDs[paneID] {
				delete(m.numberSlots, slot)
			}
		}
		for paneID := range m.flaggedSessions {
			if !aliveIDs[paneID] {
				delete(m.flaggedSessions, paneID)
			}
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

func (m *SidebarModel) SetInlineTagBacklogInput(backlogID, view string) {
	m.inlineTagBacklogID = backlogID
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

func (m SidebarModel) Items() []claude.ClaudeSession {
	return m.filtered
}

// ToggleFlagSelected toggles the flag on the currently selected session or backlog item.
func (m *SidebarModel) ToggleFlagSelected() {
	if m.IsBacklogSelected() {
		if backlog, ok := m.SelectedBacklog(); ok {
			if m.flaggedBacklogs == nil {
				m.flaggedBacklogs = make(map[string]bool)
			}
			if m.flaggedBacklogs[backlog.ID] {
				delete(m.flaggedBacklogs, backlog.ID)
			} else {
				m.flaggedBacklogs[backlog.ID] = true
			}
		}
		return
	}
	if s, ok := m.SelectedItem(); ok {
		if m.flaggedSessions == nil {
			m.flaggedSessions = make(map[string]bool)
		}
		if m.flaggedSessions[s.PaneID] {
			delete(m.flaggedSessions, s.PaneID)
		} else {
			m.flaggedSessions[s.PaneID] = true
		}
	}
}

// BindSlot binds the currently selected session to slot n (1-9).
// Toggling: if the session is already bound to slot n, it's unbound.
// Rebinding: if the session was bound to a different slot, that old binding is cleared.
// Overwriting: if slot n was held by another session, it's released.
func (m *SidebarModel) BindSlot(n int) bool {
	s, ok := m.SelectedItem()
	if !ok {
		return false
	}
	if m.numberSlots == nil {
		m.numberSlots = make(map[int]string)
	}
	// Toggle: already bound to n → unbind
	if m.numberSlots[n] == s.PaneID {
		delete(m.numberSlots, n)
		return true
	}
	// Clear any existing slot this session held
	for slot, paneID := range m.numberSlots {
		if paneID == s.PaneID {
			delete(m.numberSlots, slot)
			break
		}
	}
	m.numberSlots[n] = s.PaneID
	return true
}

// SlotForSession returns the slot number (1-9) for the given paneID, or 0 if unbound.
func (m SidebarModel) SlotForSession(paneID string) int {
	for slot, id := range m.numberSlots {
		if id == paneID {
			return slot
		}
	}
	return 0
}

// PaneIDForSlot returns the paneID bound to slot n, or "" if unbound.
func (m SidebarModel) PaneIDForSlot(n int) string {
	return m.numberSlots[n]
}

// SidebarState holds the persistable parts of sidebar UI state.
type SidebarState struct {
	FlaggedSessions map[string]bool `json:"flagged_sessions,omitempty"`
	FlaggedBacklogs map[string]bool `json:"flagged_backlogs,omitempty"`
	NumberSlots     map[int]string  `json:"number_slots,omitempty"`
}

// ExportState returns a snapshot of the sidebar's persistable state.
func (m SidebarModel) ExportState() SidebarState {
	return SidebarState{
		FlaggedSessions: m.flaggedSessions,
		FlaggedBacklogs: m.flaggedBacklogs,
		NumberSlots:     m.numberSlots,
	}
}

// ImportState restores persisted sidebar state.
func (m *SidebarModel) ImportState(st SidebarState) {
	m.flaggedSessions = st.FlaggedSessions
	m.flaggedBacklogs = st.FlaggedBacklogs
	m.numberSlots = st.NumberSlots
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
// Excludes Later-marked sessions. Returns "" if no user-turn target exists.
func (m SidebarModel) AutoJumpTarget(skipPaneID string) string {
	var bestUser string
	var bestUserTime time.Time

	for _, s := range m.filtered {
		if s.PaneID == skipPaneID || s.LaterID != "" || s.LastChanged.IsZero() {
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
	// Count Clauding sessions and remove them from the cursor-navigable list when collapsed
	m.claudingCount = 0
	if !m.claudingExpanded {
		n := 0
		for _, s := range m.filtered {
			if sessionOrder(s) == OrderAgentTurn {
				m.claudingCount++
			} else {
				m.filtered[n] = s
				n++
			}
		}
		m.filtered = m.filtered[:n]
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
	for _, text := range []string{s.CustomTitle, s.SynthesizedTitle, s.FirstMessage, s.LastUserMessage, s.ProblemType} {
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
	// Primary: session order (UserTurn, AgentTurn, Later); secondary: project name alphabetically;
	// tertiary: newest created first (newest at top)
	for i := 1; i < len(sessions); i++ {
		for j := i; j > 0; j-- {
			a, b := sessions[j-1], sessions[j]
			ao, bo := sessionOrder(a), sessionOrder(b)
			if ao > bo ||
				(ao == bo && a.Project > b.Project) ||
				(ao == bo && a.Project == b.Project && a.CreatedAt.Before(b.CreatedAt)) {
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
	if s.LaterID != "" {
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

// DiffColWidths holds the max digit widths for diff stat columns across all visible items.
type DiffColWidths struct {
	files   int // digits in file count
	added   int // digits in added lines
	removed int // digits in removed lines
}

func (m SidebarModel) computeDiffColWidths() DiffColWidths {
	var dw DiffColWidths
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
