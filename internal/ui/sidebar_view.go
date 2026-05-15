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

// passesViewFilters reports whether s should be rendered in the main session
// list. View(), PaneIDAtLine, and BacklogIDAtLine all funnel through this so
// their visible rows and line counts stay aligned.
func (m SidebarModel) passesViewFilters(s claude.ClaudeSession, projectFilter string) bool {
	order := sessionOrder(s)
	if !m.claudingExpanded && order == OrderAgentTurn {
		return false
	}
	if !m.laterExpanded && order == OrderLater {
		return false
	}
	if m.focusMode && !m.IsEffectivelyFlagged(s) {
		return false
	}
	if projectFilter != "" && !strings.Contains(strings.ToLower(s.Project), projectFilter) {
		return false
	}
	return true
}

// transitionLines counts the divider lines View() emits when moving from
// section prev to section next: YT close (heavy ━+┛) when prev was YT, plus
// a dim ─ top rule when next is non-YT.
func transitionLines(prev, next int) int {
	n := 0
	if prev == OrderUserTurn {
		n++
	}
	if next != OrderUserTurn {
		n++
	}
	return n
}

func (m *SidebarModel) View() string {
	if len(m.items) == 0 {
		return EmptyStyle.Width(m.width).Render("No Claude sessions found\n\nStart Claude in a tmux pane to see it here.")
	}

	dw := m.computeDiffColWidths()
	query := m.searchTextQuery()
	projectFilter := m.searchProjectFilter()
	singleProject := m.singleProjectFilter()

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

	// activeOrder is the section that owns the current selection — its outline
	// stays at full color; every other section's outline gets muted. -1 means
	// nothing is selected, so no muting is applied.
	//
	// activeProject identifies the project header that should be drawn in the
	// active variant (the project that contains the selected session/backlog).
	// Zero value (Name=="") means no project-level highlight — also the case
	// at project-level nav, where the cursor highlight already marks the row.
	activeOrder := -1
	var activeProject projectEntry
	if !m.deselected {
		if s, ok := m.SelectedItem(); ok {
			activeOrder = sessionOrder(s)
			if !atProjectLevel {
				statusOrder := activeOrder
				if m.groupByProject && statusOrder != OrderLater {
					statusOrder = -1
				}
				activeProject = projectEntry{Name: s.Project, StatusOrder: statusOrder}
			}
		} else if b, ok := m.SelectedBacklog(); ok {
			activeOrder = OrderBacklog
			if !atProjectLevel {
				activeProject = projectEntry{Name: b.Project, StatusOrder: OrderBacklog}
			}
		}
	}

	var lines []string
	var kinds []gutterInfo
	add := func(line string, g gutterInfo) {
		lines = append(lines, line)
		kinds = append(kinds, g)
	}
	mutedFor := func(order int) bool {
		return activeOrder != -1 && order != -1 && order != activeOrder
	}
	addSeparator := func(order int) {
		// Only YOUR TURN owns a closing line — its full outline must terminate
		// with a heavy ━ + ┛ corner. Non-YT sections have no closing chrome;
		// the next section's addTopRule (or nothing) handles the boundary.
		if order != OrderUserTurn {
			return
		}
		muted := mutedFor(order)
		add(sectionSeparator(order, m.width, muted), gutterInfo{order: order, kind: kindBottom, muted: muted})
	}
	// addTopRule emits a dim ─ above a non-YT section header so it has a clear
	// boundary with whatever came before — including YT's own ┛ close, which
	// produces a stacked heavy+dim pair at the YT→other-section boundary. YT
	// itself never gets a top rule (its colored ━ header is its own top).
	// Suppressed when nothing has been emitted yet, so the very first section
	// in the list doesn't get a leading rule.
	addTopRule := func(order int) {
		if order == OrderUserTurn || len(lines) == 0 {
			return
		}
		add(sectionSeparator(-1, m.width, false), gutterInfo{order: -1, kind: kindBottom})
	}
	// Defensive: never emit the same section's top-border twice per render.
	// If we ever do, it's a logic bug elsewhere (the loop's order-transition
	// guard should already prevent re-entering a section) — but the symptom
	// is a stacked duplicate header at the top of the sidebar that's visually
	// catastrophic, so we hard-block it here.
	emittedHeader := map[int]bool{}
	addHeader := func(order int) {
		if emittedHeader[order] {
			return
		}
		emittedHeader[order] = true
		muted := mutedFor(order)
		add(renderStatusGroupHeader(order, m.width, muted), gutterInfo{order: order, kind: kindTop, muted: muted})
	}
	sectionGutter := func(order int) gutterInfo {
		return gutterInfo{order: order, muted: mutedFor(order)}
	}

	// onlyProject(order) reports whether section `order` holds exactly one
	// distinct project — sub-headers in that case skip the trailing dim ─
	// rule since the section chrome already brackets the row. In groupByProject
	// mode every non-LATER status lifts its projects to the top level, so we
	// only track LATER (and BACKLOG, which always groups internally). Search
	// mode renders flat and never consults this.
	var firstProject [5]string
	var multiProject [5]bool
	trackProject := func(order int, project string) {
		if order < 0 || order >= len(firstProject) || multiProject[order] {
			return
		}
		switch firstProject[order] {
		case "":
			firstProject[order] = project
		case project:
		default:
			multiProject[order] = true
		}
	}
	if query == "" {
		for _, s := range m.allSorted {
			if !m.passesViewFilters(s, projectFilter) {
				continue
			}
			order := sessionOrder(s)
			if m.groupByProject && order != OrderLater {
				continue
			}
			trackProject(order, s.Project)
		}
		for _, b := range m.filteredBacklog {
			trackProject(OrderBacklog, b.Project)
		}
	}
	onlyProject := func(order int) bool {
		if order < 0 || order >= len(firstProject) {
			return false
		}
		return firstProject[order] != "" && !multiProject[order]
	}

	m.selectedProjectRow = -1
	m.selectedItemRow = -1

	// currentOrder tracks the most recent section order emitted into lines.
	// Carried out of the loop so the backlog separator can color itself with
	// the section it follows.
	currentOrder := -1

	if query != "" {
		// Search mode: render from m.filtered directly (score-sorted, flat)
		for _, s := range m.filtered {
			isSelected := s.PaneID == selectedPaneID && !m.deselected
			isAutoJump := !isSelected && s.PaneID == autoJumpTargetID
			add(m.renderItemWithStats(isSelected, isAutoJump, s, dw, query), sectionGutter(sessionOrder(s)))
		}
	} else {
		currentProject := ""

		for _, s := range m.allSorted {
			if !m.passesViewFilters(s, projectFilter) {
				continue
			}
			// Group headers — always rendered for spatial stability during narrowing
			if m.groupByProject {
				order := sessionOrder(s)
				// Emit LATER header when entering the Later zone
				if order == OrderLater && currentOrder != OrderLater {
					if len(lines) > 0 {
						addSeparator(currentOrder)
					}
					currentOrder = OrderLater
					currentProject = "" // reset to force project sub-header
					addTopRule(OrderLater)
					addHeader(OrderLater)
				}
				if !singleProject && s.Project != currentProject {
					currentProject = s.Project
					if currentOrder == OrderLater {
						pe := projectEntry{Name: s.Project, StatusOrder: OrderLater}
						// Project sub-header within Later section
						if atProjectLevel && pe == selectedProject {
							m.selectedProjectRow = len(lines)
							add(renderSelectedProjectHeader(s.Project, m.width, m.flaggedProjects[s.Project]), sectionGutter(OrderLater))
						} else if pe == activeProject {
							add(renderActiveProjectSubHeader(s.Project, m.width, m.flaggedProjects[s.Project], !onlyProject(OrderLater)), sectionGutter(OrderLater))
						} else {
							add(renderProjectSubHeader(s.Project, m.width, m.flaggedProjects[s.Project], !onlyProject(OrderLater)), sectionGutter(OrderLater))
						}
					} else {
						pe := projectEntry{Name: s.Project, StatusOrder: -1}
						if atProjectLevel && pe == selectedProject {
							m.selectedProjectRow = len(lines)
							add(renderSelectedProjectHeader(s.Project, m.width, m.flaggedProjects[s.Project]), gutterInfo{order: -1})
						} else if pe == activeProject {
							add(renderActiveGroupHeader(s.Project, m.width, m.flaggedProjects[s.Project]), gutterInfo{order: -1})
						} else {
							add(renderGroupHeader(s.Project, m.width, m.flaggedProjects[s.Project]), gutterInfo{order: -1})
						}
					}
				}
			} else {
				order := sessionOrder(s)
				if order != currentOrder {
					if len(lines) > 0 {
						addSeparator(currentOrder)
					}
					currentOrder = order
					currentProject = "" // reset project tracking for new status group
					addTopRule(order)
					addHeader(order)
				}
				if !singleProject && s.Project != currentProject {
					currentProject = s.Project
					pe := projectEntry{Name: s.Project, StatusOrder: currentOrder}
					if atProjectLevel && pe == selectedProject {
						m.selectedProjectRow = len(lines)
						add(renderSelectedProjectHeader(s.Project, m.width, m.flaggedProjects[s.Project]), sectionGutter(currentOrder))
					} else if pe == activeProject {
						add(renderActiveProjectSubHeader(s.Project, m.width, m.flaggedProjects[s.Project], !onlyProject(currentOrder)), sectionGutter(currentOrder))
					} else {
						add(renderProjectSubHeader(s.Project, m.width, m.flaggedProjects[s.Project], !onlyProject(currentOrder)), sectionGutter(currentOrder))
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
			add(m.renderItemWithStats(isSelected, isAutoJump, s, dw, query), sectionGutter(sessionOrder(s)))
		}
	}

	// Render backlog section (after sessions)
	if len(m.filteredBacklog) > 0 {
		if len(lines) > 0 {
			addSeparator(currentOrder)
		}
		addTopRule(OrderBacklog)
		addHeader(OrderBacklog)

		currentBacklogProject := ""
		for i, backlog := range m.filteredBacklog {
			if !singleProject && backlog.Project != currentBacklogProject {
				currentBacklogProject = backlog.Project
				backlogPE := projectEntry{Name: backlog.Project, StatusOrder: OrderBacklog}
				if atProjectLevel && selectedProject == backlogPE {
					m.selectedProjectRow = len(lines)
					add(renderSelectedProjectHeader(backlog.Project, m.width, m.flaggedProjects[backlog.Project]), sectionGutter(OrderBacklog))
				} else if backlogPE == activeProject {
					add(renderActiveProjectSubHeader(backlog.Project, m.width, m.flaggedProjects[backlog.Project], !onlyProject(OrderBacklog)), sectionGutter(OrderBacklog))
				} else {
					add(renderProjectSubHeader(backlog.Project, m.width, m.flaggedProjects[backlog.Project], !onlyProject(OrderBacklog)), sectionGutter(OrderBacklog))
				}
			}
			backlogCursor := len(m.filtered) + i
			isSelected := backlogCursor == m.cursor && !m.deselected && !atProjectLevel
			add(m.renderBacklogItem(isSelected, backlog), sectionGutter(OrderBacklog))
		}
		currentOrder = OrderBacklog
	}

	// Only YOUR TURN needs a trailing close — its full outline must terminate
	// with a ┛ corner. Other sections fade into the bottom-pinned region (or
	// the sidebar floor) without a closing rule, matching the pre-outline look.
	if currentOrder == OrderUserTurn {
		addSeparator(currentOrder)
	}

	// Skeleton placeholder when all sections are collapsed
	if len(lines) == 0 && m.IsAllQuiet() {
		add("", gutterInfo{order: -1})
		add(ItemDetailStyle.Render("    All clear"), gutterInfo{order: -1})
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
		kinds = kinds[:cutAt]

		// Pad blank lines until the pinned region floats to the floor.
		for visual < upperBudget {
			lines = append(lines, "")
			kinds = append(kinds, gutterInfo{order: -1})
			visual++
		}

		// Pulse + badges are aggregates, not section content → dim gutter.
		for _, row := range pinned {
			lines = append(lines, row)
			kinds = append(kinds, gutterInfo{order: -1})
		}
	}

	// Truncate to fit available height
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
		kinds = kinds[:m.height]
	}

	return strings.Join(m.applyGutters(lines, kinds), "\n")
}

// gutterKind selects which glyph the gutter uses for a row.
type gutterKind uint8

const (
	kindBody   gutterKind = iota // ┃ — the continuous side of the section
	kindTop                      // ┓ — top-right corner, on the section's title row
	kindBottom                   // ┛ — bottom-right corner, on the closing separator
)

// gutterInfo describes how to render the right-edge gutter for one entry.
// muted swaps the section's color for its muted variant — used for sections
// that don't contain the currently-selected item.
type gutterInfo struct {
	order int
	kind  gutterKind
	muted bool
}

// sectionPalette is the single source of truth for section-semantic colors and
// header labels. Indexed by Order constant. OrderOther (3) is intentionally
// blank — sessions in that state never render section chrome.
var sectionPalette = [5]struct {
	color, mutedColor lipgloss.AdaptiveColor
	headerStyle       lipgloss.Style
	label             string
}{
	OrderUserTurn:  {ColorDone, ColorDoneMuted, GroupHeaderDoneStyle, IconHandRaise + " YOUR TURN"},
	OrderAgentTurn: {ColorWorking, ColorWorkingMuted, GroupHeaderWorkingStyle, IconWand + " CLAUDING"},
	OrderLater:     {ColorLater, ColorLaterMuted, GroupHeaderLaterStyle, IconLater + " LATER"},
	OrderBacklog:   {ColorBacklog, ColorBacklogMuted, GroupHeaderBacklogStyle, IconBacklog + " BACKLOG"},
}

// gutterGlyphs[order][kind][muted] holds the pre-rendered string for every
// possible section gutter cell. Built once at init so renderGutter is a
// constant-time lookup with zero per-frame allocs.
var gutterGlyphs = func() [5][3][2]string {
	var table [5][3][2]string
	glyphs := [3]string{kindBody: "┃", kindTop: "┓", kindBottom: "┛"}
	for ord, entry := range sectionPalette {
		if entry.label == "" {
			continue // OrderOther — skip
		}
		for k := range glyphs {
			table[ord][k][0] = lipgloss.NewStyle().Foreground(entry.color).Render(glyphs[k])
			table[ord][k][1] = lipgloss.NewStyle().Foreground(entry.mutedColor).Render(glyphs[k])
		}
	}
	return table
}()

// Dim fallbacks for non-section rows (orders outside the palette).
var (
	gutterDim = lipgloss.NewStyle().Foreground(ColorBorder).Render("│")
	gutterTee = lipgloss.NewStyle().Foreground(ColorBorder).Render("┤")
)

func renderGutter(g gutterInfo) string {
	// Only YOUR TURN renders the colored outline glyphs. Every other section
	// falls back to the dim │ (or ┤ on a separator row) so the surrounding
	// chrome reads as a simple horizontal-divider layout.
	if g.order == OrderUserTurn {
		mi := 0
		if g.muted {
			mi = 1
		}
		return gutterGlyphs[g.order][g.kind][mi]
	}
	if g.kind == kindBottom {
		return gutterTee
	}
	return gutterDim
}

// sectionColor returns the foreground color for a section's outline, picking
// the muted variant when requested. Returns nil for non-section orders so
// callers can fall back to the dim border style.
func sectionColor(order int, muted bool) lipgloss.TerminalColor {
	if order < 0 || order >= len(sectionPalette) || sectionPalette[order].label == "" {
		return nil
	}
	if muted {
		return sectionPalette[order].mutedColor
	}
	return sectionPalette[order].color
}

// sectionSeparator renders the horizontal rule that caps a section. Colored
// sections use heavy ━ so it lines up with the heavy ┃ gutter and ┛ corner;
// non-section boundaries fall back to dim light ─.
func sectionSeparator(order int, width int, muted bool) string {
	fg := sectionColor(order, muted)
	if fg == nil {
		return SeparatorStyle.Width(width).Render(strings.Repeat("─", width))
	}
	return lipgloss.NewStyle().Foreground(fg).Width(width).Render(strings.Repeat("━", width))
}

// applyGutters expands each entry into its visual lines, pads them to m.width,
// and appends a colored gutter character. Each returned line is exactly
// m.width+1 columns wide, which is what SidebarPanelStyle expects.
func (m SidebarModel) applyGutters(entries []string, kinds []gutterInfo) []string {
	out := make([]string, 0, len(entries))
	// Shared pad buffer — sliced into a per-line trailing-space string without
	// re-allocating strings.Repeat() per visual line.
	pad := strings.Repeat(" ", m.width)
	fit := func(s string) string {
		w := lipgloss.Width(s)
		if w > m.width {
			// Truncate to keep the gutter aligned. Letting an over-wide row
			// through causes SidebarPanelStyle to soft-wrap, which cascades
			// the whole sidebar layout down by one row per overflow.
			return ansi.Truncate(s, m.width, "…")
		}
		if w < m.width {
			return s + pad[:m.width-w]
		}
		return s
	}
	for i, entry := range entries {
		gut := renderGutter(kinds[i])
		// Fast path: single-line entry (no newline). Skips strings.Split alloc.
		if !strings.Contains(entry, "\n") {
			out = append(out, fit(entry)+gut)
			continue
		}
		for _, vis := range strings.Split(entry, "\n") {
			out = append(out, fit(vis)+gut)
		}
	}
	return out
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

// padHeaderBar appends a thin dim ‧ fill to a pre-rendered header label so the
// row spans `width` columns. Returns the label unchanged if it already does.
func padHeaderBar(label string, width int) string {
	w := lipgloss.Width(label)
	if w >= width {
		return label
	}
	return label + SeparatorStyle.Render(strings.Repeat("‧", width-w))
}

// padHeaderBarIf is padHeaderBar gated on a flag — callers use it to suppress
// the trailing dim ─ rule (e.g. on a sub-header that's the only one in its
// section, where the section chrome already brackets the row).
func padHeaderBarIf(label string, width int, withBar bool) string {
	if !withBar {
		return label
	}
	return padHeaderBar(label, width)
}

func renderGroupHeader(project string, width int, flagged bool) string {
	return padHeaderBar(GroupHeaderProjectStyle.Render(projectLabel(project, flagged)), width)
}

// selectedProjectHeaderStyle is the highlight style for project headers at project-level nav.
var selectedProjectHeaderStyle = GroupHeaderStyle.
	Foreground(ColorAccent).
	Background(ColorSelectionBg)

func renderSelectedProjectHeader(project string, width int, flagged bool) string {
	return selectedProjectHeaderStyle.Width(width).Render(projectLabel(project, flagged))
}

func renderProjectSubHeader(project string, width int, flagged, withBar bool) string {
	return padHeaderBarIf(ProjectSubHeaderStyle.Render(projectLabel(project, flagged)), width, withBar)
}

// activeProjectSubHeaderStyle is ProjectSubHeaderStyle with the muted foreground
// dropped, so the active project header reads at the terminal's default text
// color — bright enough to mark the parent of the cursor, without competing
// with the cursor's own highlight.
var activeProjectSubHeaderStyle = ProjectSubHeaderStyle.UnsetForeground()

func renderActiveGroupHeader(project string, width int, flagged bool) string {
	return padHeaderBar(GroupHeaderStyle.Render(projectLabel(project, flagged)), width)
}

func renderActiveProjectSubHeader(project string, width int, flagged, withBar bool) string {
	return padHeaderBarIf(activeProjectSubHeaderStyle.Render(projectLabel(project, flagged)), width, withBar)
}

func renderStatusGroupHeader(order, width int, muted bool) string {
	if order < 0 || order >= len(sectionPalette) || sectionPalette[order].label == "" {
		return ""
	}
	entry := sectionPalette[order]
	// Title text stays at full color so the section's identity remains visible
	// even when the surrounding outline is muted; only the trailing dashes
	// follow the muted flag. Only YOUR TURN gets a heavy ━ fill — other
	// sections render just the label and let applyGutters pad with spaces.
	rendered := entry.headerStyle.Render(entry.label)
	if order != OrderUserTurn {
		return rendered
	}
	w := lipgloss.Width(rendered)
	if w >= width {
		return rendered
	}
	return rendered + lipgloss.NewStyle().Foreground(sectionColor(order, muted)).Render(strings.Repeat("━", width-w))
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
	projectFilter := m.searchProjectFilter()
	singleProject := m.singleProjectFilter()
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
		if !m.passesViewFilters(s, projectFilter) {
			continue
		}
		order := sessionOrder(s)

		// Group headers — must mirror View()'s logic exactly
		if m.groupByProject {
			if order == OrderLater && currentOrder != OrderLater {
				if anyLinesEmitted {
					currentLine += transitionLines(currentOrder, OrderLater)
				}
				currentOrder = OrderLater
				currentProject = ""
				anyLinesEmitted = true
				currentLine++ // LATER status group header
			}
			if !singleProject && s.Project != currentProject {
				currentProject = s.Project
				if currentOrder == OrderLater {
					currentLine++ // project sub-header (no separator within Later)
				} else {
					anyLinesEmitted = true
					currentLine++ // group header (inline separator, no standalone line)
				}
			}
		} else {
			if order != currentOrder {
				if anyLinesEmitted {
					currentLine += transitionLines(currentOrder, order)
				}
				currentOrder = order
				currentProject = ""
				anyLinesEmitted = true
				currentLine++ // status group header
			}
			if !singleProject && s.Project != currentProject {
				currentProject = s.Project
				currentLine++ // project sub-header
			}
		}

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
	projectFilter := m.searchProjectFilter()
	singleProject := m.singleProjectFilter()
	currentLine := 0

	lastOrder := -1

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
			if !m.passesViewFilters(s, projectFilter) {
				continue
			}
			order := sessionOrder(s)

			if m.groupByProject {
				if order == OrderLater && currentOrder != OrderLater {
					if anyLinesEmitted {
						currentLine += transitionLines(currentOrder, OrderLater)
					}
					currentOrder = OrderLater
					currentProject = ""
					anyLinesEmitted = true
					currentLine++ // LATER status group header
				}
				if !singleProject && s.Project != currentProject {
					currentProject = s.Project
					if currentOrder == OrderLater {
						currentLine++ // project sub-header (no separator within Later)
					} else {
						anyLinesEmitted = true
						currentLine++ // group header (inline separator, no standalone line)
					}
				}
			} else {
				if order != currentOrder {
					if anyLinesEmitted {
						currentLine += transitionLines(currentOrder, order)
					}
					currentOrder = order
					currentProject = ""
					anyLinesEmitted = true
					currentLine++ // status group header
				}
				if !singleProject && s.Project != currentProject {
					currentProject = s.Project
					currentLine++ // project sub-header
				}
			}
			if m.matchSet != nil && !m.matchSet[s.PaneID] {
				continue
			}
			currentLine += m.itemLineCount(s, query)
		}
		lastOrder = currentOrder
	}

	if currentLine > 0 {
		currentLine += transitionLines(lastOrder, OrderBacklog)
	}
	currentLine++ // "BACKLOG" group header

	// Walk backlog items: project header → items.
	currentBacklogProject := ""
	for _, backlog := range m.filteredBacklog {
		if !singleProject && backlog.Project != currentBacklogProject {
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
