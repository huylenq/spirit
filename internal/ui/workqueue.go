package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

const (
	WorkQueueHeight     = 5  // fixed strip height in lines (including card borders)
	workQueueCardW      = 62 // card width per session (including border)
	workQueueCardInnerW = 60 // card content width (cardW - 2 for border sides)
	workQueueCardInnerH = 3  // card content height (height - 2 for border top/bottom)
	workQueueBenchW     = 26 // width for the compacted "bench" section (avatar + status + title)
	workQueueCardGap    = 1  // margin between cards
)

// WorkQueueModel manages the horizontal work queue strip that shows user-turn
// sessions as a conveyor belt with bench (non-user-turn) sessions docked on the right.
type WorkQueueModel struct {
	queue        []claude.ClaudeSession // user-turn sessions, oldest-first
	bench        []claude.ClaudeSession // non-user-turn sessions (Claude's turn)
	cursor       int                    // index into queue
	scrollOffset int                    // first visible card index (horizontal scroll)
	width        int                    // total available width
	autoJumpID   string                 // pane ID of the autojump "next" target
	dino         *DinoGame              // empty-state mini game (lazy-init)
}

// IsEmpty reports whether the queue has zero user-turn sessions. Used by the
// app to decide whether to drive the empty-state dino game's tick + keys.
func (m *WorkQueueModel) IsEmpty() bool { return len(m.queue) == 0 }

// Dino returns the empty-state game model, creating it on first access.
func (m *WorkQueueModel) Dino() *DinoGame {
	if m.dino == nil {
		m.dino = NewDinoGame()
	}
	return m.dino
}

// SetSize sets the total available width for the work queue strip.
func (m *WorkQueueModel) SetSize(width int) {
	m.width = width
}

// SetItems partitions sessions into queue (user-turn, oldest-first) and bench (non-user-turn).
// Preserves cursor by PaneID across updates.
func (m *WorkQueueModel) SetItems(sessions []claude.ClaudeSession, autoJumpTargetID string) {
	// Save current selection
	var selectedPaneID string
	if m.cursor >= 0 && m.cursor < len(m.queue) {
		selectedPaneID = m.queue[m.cursor].PaneID
	}

	m.queue = nil
	m.bench = nil
	m.autoJumpID = autoJumpTargetID

	for _, s := range sessions {
		if s.LaterID != "" {
			continue // hide Later sessions entirely from the work queue
		}
		if s.Status == claude.StatusUserTurn {
			m.queue = append(m.queue, s)
		} else {
			m.bench = append(m.bench, s)
		}
	}

	// Sort queue: oldest LastChanged first (leftmost = longest waiting)
	sort.SliceStable(m.queue, func(i, j int) bool {
		return m.queue[i].LastChanged.Before(m.queue[j].LastChanged)
	})

	// Restore cursor by PaneID
	m.cursor = 0
	if selectedPaneID != "" {
		for i, s := range m.queue {
			if s.PaneID == selectedPaneID {
				m.cursor = i
				break
			}
		}
	}

	// Clamp
	if m.cursor >= len(m.queue) {
		m.cursor = max(len(m.queue)-1, 0)
	}
	m.clampScroll()
}

// SelectedItem returns the currently selected queue session, if any.
func (m *WorkQueueModel) SelectedItem() (claude.ClaudeSession, bool) {
	if m.cursor >= 0 && m.cursor < len(m.queue) {
		return m.queue[m.cursor], true
	}
	return claude.ClaudeSession{}, false
}

// SelectByPaneID moves the cursor to the queue item with the given PaneID.
// Returns true if found.
func (m *WorkQueueModel) SelectByPaneID(paneID string) bool {
	for i, s := range m.queue {
		if s.PaneID == paneID {
			m.cursor = i
			m.clampScroll()
			return true
		}
	}
	return false
}

// SelectTop moves the cursor to the first (leftmost/oldest) queue item.
func (m *WorkQueueModel) SelectTop() {
	m.cursor = 0
	m.clampScroll()
}

// MoveLeft moves the cursor left in the queue.
func (m *WorkQueueModel) MoveLeft() {
	if m.cursor > 0 {
		m.cursor--
		m.clampScroll()
	}
}

// MoveRight moves the cursor right in the queue.
func (m *WorkQueueModel) MoveRight() {
	if m.cursor < len(m.queue)-1 {
		m.cursor++
		m.clampScroll()
	}
}

// MoveToEnd moves the cursor to the last (rightmost/newest) queue item.
func (m *WorkQueueModel) MoveToEnd() {
	if len(m.queue) > 0 {
		m.cursor = len(m.queue) - 1
		m.clampScroll()
	}
}

// QueueLen returns the number of user-turn sessions in the queue.
func (m *WorkQueueModel) QueueLen() int {
	return len(m.queue)
}

// clampScroll ensures the cursor is within the visible viewport.
func (m *WorkQueueModel) clampScroll() {
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	visibleCards := m.visibleCardCount()
	if visibleCards > 0 && m.cursor >= m.scrollOffset+visibleCards {
		m.scrollOffset = m.cursor - visibleCards + 1
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

// visibleCardCount returns how many cards can fit in the queue area
// (total width minus the bench section).
func (m *WorkQueueModel) visibleCardCount() int {
	othersW := m.benchWidth()
	queueArea := m.width - othersW
	if queueArea <= 0 {
		return 0
	}
	return max(queueArea/(workQueueCardW+workQueueCardGap), 1)
}

// benchWidth returns the width allocated to the compacted bench section.
func (m *WorkQueueModel) benchWidth() int {
	if len(m.bench) == 0 {
		return 0
	}
	// Fixed width: enough for "<avatar><status> <title…>"
	return workQueueBenchW
}

// View renders the work queue strip. The sidebar parameter is used to render
// cards via RenderCard (reusing the sidebar's item renderer).
func (m *WorkQueueModel) View(sidebar *SidebarModel) string {
	if m.width <= 0 {
		return ""
	}

	dw := sidebar.ComputeDiffColWidths()

	benchView := m.renderBench(sidebar)
	benchW := lipgloss.Width(benchView)

	queueArea := m.width - benchW
	if queueArea < 0 {
		queueArea = 0
	}

	queueView := m.renderQueue(sidebar, dw, queueArea)

	// Join: queue on left, bench docked on right
	if benchView == "" {
		return queueView
	}

	// Pad queue to fill queueArea
	queueLines := strings.Split(queueView, "\n")
	for i := range queueLines {
		lineW := lipgloss.Width(queueLines[i])
		if lineW < queueArea {
			queueLines[i] += strings.Repeat(" ", queueArea-lineW)
		}
	}
	queueView = strings.Join(queueLines, "\n")

	return lipgloss.JoinHorizontal(lipgloss.Top, queueView, benchView)
}

// renderQueue renders the scrollable queue cards area.
func (m *WorkQueueModel) renderQueue(sidebar *SidebarModel, dw DiffColWidths, areaWidth int) string {
	if len(m.queue) == 0 {
		game := m.Dino()
		game.SetWidth(areaWidth)
		return game.View(areaWidth)
	}

	// Determine card width: use workQueueCardW but shrink if only 1 card and lots of space
	cardW := workQueueCardW
	if cardW > areaWidth {
		cardW = areaWidth
	}
	innerW := cardW - 2 // subtract border sides
	if innerW < 4 {
		innerW = 4
	}

	var cards []string
	visible := m.visibleCardCount()
	end := min(m.scrollOffset+visible, len(m.queue))

	for i := m.scrollOffset; i < end; i++ {
		s := m.queue[i]
		isSelected := i == m.cursor
		isAutoJump := !isSelected && s.PaneID == m.autoJumpID
		content := sidebar.RenderCard(innerW, workQueueCardInnerH, isSelected, isAutoJump, s, dw)

		// Wrap in border
		border := lipgloss.RoundedBorder()
		borderColor := ColorBorder
		if isSelected {
			borderColor = AvatarColor(s.AvatarColorIdx)
		}
		borderStyle := lipgloss.NewStyle().
			Border(border).
			BorderForeground(borderColor).
			Width(innerW).
			Height(workQueueCardInnerH)
		card := borderStyle.Render(content)
		cards = append(cards, card)
	}

	if len(cards) == 0 {
		return strings.Repeat(" ", areaWidth)
	}

	// Join cards horizontally with gap
	gap := strings.Repeat(" ", workQueueCardGap)
	gapLines := make([]string, WorkQueueHeight)
	for i := range gapLines {
		gapLines[i] = gap
	}
	gapStr := strings.Join(gapLines, "\n")

	result := cards[0]
	for _, card := range cards[1:] {
		result = lipgloss.JoinHorizontal(lipgloss.Top, result, gapStr, card)
	}

	// Add scroll indicators if there are hidden cards
	if m.scrollOffset > 0 || end < len(m.queue) {
		resultLines := strings.Split(result, "\n")
		// Add left arrow indicator on middle line
		mid := WorkQueueHeight / 2
		if m.scrollOffset > 0 && mid < len(resultLines) {
			resultLines[mid] = "◂" + resultLines[mid][1:]
		}
		if end < len(m.queue) && mid < len(resultLines) {
			line := resultLines[mid]
			lineW := lipgloss.Width(line)
			if lineW > 1 {
				resultLines[mid] = ansi.Truncate(line, lineW-1, "") + "▸"
			}
		}
		result = strings.Join(resultLines, "\n")
	}

	return result
}

// renderBench renders the compacted bench section (non-user-turn sessions)
// as one item per line: avatar + status + truncated title.
func (m *WorkQueueModel) renderBench(sidebar *SidebarModel) string {
	if len(m.bench) == 0 {
		return ""
	}

	// Each line: "<avatar> <status> <title>"
	// Reserve: avatar(2) + space(1) + status(1) + space(1) = 5 chars before title
	const prefixCols = 5

	lines := make([]string, 0, WorkQueueHeight)
	for i, s := range m.bench {
		if i >= WorkQueueHeight {
			break
		}
		glyph := AvatarGlyph(s.AvatarAnimalIdx)
		styled := AvatarStyle(s.AvatarColorIdx).Render(glyph)

		var indicator string
		switch s.Status {
		case claude.StatusAgentTurn:
			indicator = StatWorkingStyle.Render(sidebar.SpinnerView())
		default:
			indicator = ItemDetailStyle.Render("·")
		}

		title := s.DisplayName()
		if title == "" {
			title = "(New session)"
		}
		title = strings.ReplaceAll(title, "\n", " ")

		// Truncate title to fit available width
		titleW := m.benchWidth() - prefixCols
		if titleW > 0 {
			title = ansi.Truncate(title, titleW, "…")
		} else {
			title = ""
		}

		line := styled + " " + indicator + " " + ItemDetailStyle.Render(title)
		lines = append(lines, line)
	}

	// Pad to WorkQueueHeight
	for len(lines) < WorkQueueHeight {
		lines = append(lines, "")
	}

	// Overflow indicator on last line if more items than visible
	if len(m.bench) > WorkQueueHeight {
		extra := len(m.bench) - WorkQueueHeight
		lines[WorkQueueHeight-1] = ItemDetailStyle.Render(fmt.Sprintf("+%d more", extra))
	}

	return strings.Join(lines, "\n")
}

// SpinnerView returns the current spinner frame string.
func (m *SidebarModel) SpinnerView() string {
	return m.spinnerView
}
