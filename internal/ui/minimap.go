package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

// MinimapModel renders a spatial minimap of tmux pane layout.
type MinimapModel struct {
	panes          []minimapPane
	sessionName    string
	selectedPaneID string
	height         int
	windowCols     int    // columns per window in the grid (default 40)
	collapse    bool   // collapse single-pane windows to narrower columns
	spinnerView string // current spinner animation frame (set externally)
	LastNavDebug   string // debug: last navigation attempt result
}

// PaneStatus constants for minimap rendering (UI concept, not tied to claude.Status).
const (
	PaneStatusNone      = 0 // not a Claude pane
	PaneStatusAgentTurn = 1
	PaneStatusUserTurn  = 2 // "user-turn"
	PaneStatusLater     = 3 // bookmarked pane
)

type minimapPane struct {
	PaneID      string
	SessionName string
	WindowIndex int
	WindowName  string
	PaneTitle   string
	PaneIndex   int
	// Absolute pixel coords within the window
	Left, Top, Width, Height int
	WindowWidth, WindowHeight int
	Status                    int // PaneStatus* constant
	IsSelected                bool
	AvatarColorIdx            int // avatar color index (Claude panes only)
	AvatarAnimalIdx           int // avatar animal glyph index (Claude panes only)
}

// PaneAvatarInfo bundles avatar indices for a Claude pane.
type PaneAvatarInfo struct {
	ColorIdx  int
	AnimalIdx int
}

// MinimapPaneInfo carries the info needed to switch to a minimap pane.
type MinimapPaneInfo struct {
	PaneID      string
	SessionName string
	WindowIndex int
	PaneIndex   int
	PaneTitle   string
	IsClaude    bool
}

type windowGroup struct {
	Index int
	Name  string
	Panes []minimapPane
	// Max dimensions of the window
	Width, Height int
}

var (
	minimapBorderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorBorder).
				PaddingLeft(1).
				PaddingRight(1)

	minimapSessionStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true)

	minimapTabStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	minimapPaneDimStyle = lipgloss.NewStyle().
				Foreground(ColorBorder)

	minimapPaneWorkingStyle = lipgloss.NewStyle().
				Foreground(ColorWorking)

	minimapPaneDoneStyle = lipgloss.NewStyle().
				Foreground(ColorDone)

	minimapPaneLaterStyle = lipgloss.NewStyle().
				Foreground(ColorLater)

	minimapPaneSelectedStyle = lipgloss.NewStyle().
					Foreground(ColorAccent).
					Bold(true)
)

const DefaultMinimapWindowCols = 40
const collapsedWindowCols = 8 // narrower column for single-pane windows
const collapsedGridH = 3       // minimal vertical rows for collapsed single-pane windows

func NewMinimapModel() MinimapModel {
	return MinimapModel{windowCols: DefaultMinimapWindowCols}
}

func (m *MinimapModel) SetSize(_, h int) {
	m.height = h
}

// SetCollapse enables/disables collapsing single-pane windows to narrower columns.
func (m *MinimapModel) SetCollapse(on bool) {
	m.collapse = on
}

// SetWindowCols sets the per-window column width for the minimap grid.
func (m *MinimapModel) SetWindowCols(cols int) {
	if cols < 15 {
		cols = 15
	}
	m.windowCols = cols
}

// computeLayout returns all windows, fixed per-window column/row counts, total innerW, and gridH.
// winRows[i] is the per-window grid height — collapsed single-pane windows get collapsedGridH.
func (m MinimapModel) computeLayout() (windows []windowGroup, winCols []int, winRows []int, innerW, gridH int) {
	windows = m.groupByWindow()
	if len(windows) == 0 {
		return
	}
	cols := m.windowCols
	if cols == 0 {
		cols = DefaultMinimapWindowCols
	}
	gaps := len(windows) - 1
	if gaps < 0 {
		gaps = 0
	}
	innerH := m.height - 2
	gridH = innerH - 1
	if gridH < 1 {
		gridH = 1
	}
	winCols = make([]int, len(windows))
	winRows = make([]int, len(windows))
	innerW = 0
	for i, w := range windows {
		if m.collapse && len(w.Panes) == 1 && collapsedWindowCols < cols {
			winCols[i] = collapsedWindowCols
		} else {
			winCols[i] = cols
		}
		if m.collapse && len(w.Panes) == 1 && gridH > collapsedGridH {
			winRows[i] = collapsedGridH
		} else {
			winRows[i] = gridH
		}
		innerW += winCols[i]
	}
	innerW += gaps * 3
	return
}

// SetData configures the minimap. paneStatuses maps paneID → PaneStatus* constant
// for Claude panes; panes not in the map are treated as non-Claude (PaneStatusNone).
// paneAvatars maps paneID → avatar info for identity coloring.
func (m *MinimapModel) SetData(geom []tmux.PaneGeometry, paneStatuses map[string]int, paneAvatars map[string]PaneAvatarInfo, selectedPaneID, sessionName string) {
	m.sessionName = sessionName
	m.selectedPaneID = selectedPaneID
	m.panes = make([]minimapPane, len(geom))
	for i, g := range geom {
		av := paneAvatars[g.PaneID]
		m.panes[i] = minimapPane{
			PaneID:          g.PaneID,
			SessionName:     g.SessionName,
			WindowIndex:     g.WindowIndex,
			WindowName:      g.WindowName,
			PaneTitle:       g.PaneTitle,
			PaneIndex:       g.PaneIndex,
			Left:            g.Left,
			Top:             g.Top,
			Width:           g.Width,
			Height:          g.Height,
			WindowWidth:     g.WindowWidth,
			WindowHeight:    g.WindowHeight,
			Status:          paneStatuses[g.PaneID],
			IsSelected:      g.PaneID == selectedPaneID,
			AvatarColorIdx:  av.ColorIdx,
			AvatarAnimalIdx: av.AnimalIdx,
		}
	}
}

func (m *MinimapModel) SetSpinnerView(s string) {
	m.spinnerView = s
}

func (m *MinimapModel) UpdateSelected(paneID string) {
	m.selectedPaneID = paneID
	for i := range m.panes {
		m.panes[i].IsSelected = m.panes[i].PaneID == paneID
	}
}

type SpatialDir int

const (
	DirUp SpatialDir = iota
	DirDown
	DirLeft
	DirRight
)

// gridRect is a pane's position in the rendered grid coordinate system.
type gridRect struct {
	PaneID      string
	WindowIndex int
	Status      int // PaneStatus* constant
	// Grid coordinates (global across all windows)
	X1, Y1, X2, Y2 int
}

// computeGridRects computes the rendered grid position for every pane,
// using the same scaling logic as renderWindowGrid. This ensures
// NavigateSpatial matches what the user sees on screen.
func (m MinimapModel) computeGridRects() []gridRect {
	if len(m.panes) == 0 || m.height < 5 {
		return nil
	}

	windows, winCols, winRows, innerW, gridH := m.computeLayout()
	if len(windows) == 0 || innerW < 8 || gridH < 1 {
		return nil
	}
	visibleWindows := windows

	// Build cell ownership grid — last writer wins, matching renderWindowGrid
	totalCols := 0
	for i, cols := range winCols {
		if i > 0 {
			totalCols += 3 // separator
		}
		totalCols += cols
	}
	cellGrid := make([][]string, gridH)
	for r := range cellGrid {
		cellGrid[r] = make([]string, totalCols)
	}
	statusMap := map[string]int{}
	winIdxMap := map[string]int{}

	xOffset := 0
	for i, w := range visibleWindows {
		cols := winCols[i]
		wGridH := winRows[i]
		for _, p := range w.Panes {
			x1 := int(math.Round(float64(p.Left) / float64(w.Width) * float64(cols)))
			y1 := int(math.Round(float64(p.Top) / float64(w.Height) * float64(wGridH)))
			x2 := int(math.Round(float64(p.Left+p.Width) / float64(w.Width) * float64(cols)))
			y2 := int(math.Round(float64(p.Top+p.Height) / float64(w.Height) * float64(wGridH)))
			if x2-x1 < 3 {
				x2 = x1 + 3
			}
			if y2-y1 < 1 {
				y2 = y1 + 1
			}
			if x1 < 0 {
				x1 = 0
			}
			if y1 < 0 {
				y1 = 0
			}
			if x2 > cols {
				x2 = cols
			}
			if y2 > wGridH {
				y2 = wGridH
			}
			for r := y1; r < y2; r++ {
				for c := x1; c < x2; c++ {
					cellGrid[r][xOffset+c] = p.PaneID
				}
			}
			statusMap[p.PaneID] = p.Status
			winIdxMap[p.PaneID] = p.WindowIndex
		}
		xOffset += cols + 3 // +3 for separator
	}

	// Derive visual bounding rects from cell ownership
	extents := map[string]*gridRect{}
	for r := 0; r < gridH; r++ {
		for c := 0; c < totalCols; c++ {
			pid := cellGrid[r][c]
			if pid == "" {
				continue
			}
			e, ok := extents[pid]
			if !ok {
				e = &gridRect{
					PaneID: pid, WindowIndex: winIdxMap[pid], Status: statusMap[pid],
					X1: c, Y1: r, X2: c + 1, Y2: r + 1,
				}
				extents[pid] = e
			}
			if c < e.X1 {
				e.X1 = c
			}
			if r < e.Y1 {
				e.Y1 = r
			}
			if c+1 > e.X2 {
				e.X2 = c + 1
			}
			if r+1 > e.Y2 {
				e.Y2 = r + 1
			}
		}
	}

	var rects []gridRect
	for _, e := range extents {
		rects = append(rects, *e)
	}
	sort.Slice(rects, func(i, j int) bool {
		if rects[i].Y1 != rects[j].Y1 {
			return rects[i].Y1 < rects[j].Y1
		}
		return rects[i].X1 < rects[j].X1
	})
	return rects
}

// NavigateSpatial moves selection to the nearest pane in the given direction,
// using rendered grid coordinates so navigation matches the visual layout.
// Returns (paneID, isClaude) of the new selection, or ("", false) if no move.
func (m *MinimapModel) NavigateSpatial(dir SpatialDir) (string, bool) {
	rects := m.computeGridRects()
	if len(rects) < 2 {
		m.LastNavDebug += fmt.Sprintf(" → only %d rects", len(rects))
		return "", false
	}

	var cur *gridRect
	for i := range rects {
		if rects[i].PaneID == m.selectedPaneID {
			cur = &rects[i]
			break
		}
	}
	if cur == nil {
		m.LastNavDebug += fmt.Sprintf(" → sel=%s NOT in %d rects", m.selectedPaneID, len(rects))
		return "", false
	}

	// Direction check uses EDGES, not centers.
	// A pane is "below" only if it starts at or past the source's bottom edge.
	// This prevents phantom navigation to panes that overlap the source.
	type candidate struct {
		rect    *gridRect
		dist    float64
		perpDst float64 // perpendicular center-to-center distance (tiebreaker)
	}
	var candidates []candidate

	srcCenterX := float64(cur.X1+cur.X2) / 2
	srcCenterY := float64(cur.Y1+cur.Y2) / 2

	for i := range rects {
		r := &rects[i]
		if r.PaneID == cur.PaneID {
			continue
		}

		inDir := false
		var dist, perpDst float64
		switch dir {
		case DirUp:
			inDir = r.Y2 <= cur.Y1 // candidate ends at or above source top
			if inDir {
				dist = float64(cur.Y1 - r.Y2)
				perpDst = math.Abs(float64(r.X1+r.X2)/2 - srcCenterX)
			}
		case DirDown:
			inDir = r.Y1 >= cur.Y2 // candidate starts at or below source bottom
			if inDir {
				dist = float64(r.Y1 - cur.Y2)
				perpDst = math.Abs(float64(r.X1+r.X2)/2 - srcCenterX)
			}
		case DirLeft:
			inDir = r.X2 <= cur.X1 // candidate ends at or left of source left
			if inDir {
				dist = float64(cur.X1 - r.X2)
				perpDst = math.Abs(float64(r.Y1+r.Y2)/2 - srcCenterY)
			}
		case DirRight:
			inDir = r.X1 >= cur.X2 // candidate starts at or right of source right
			if inDir {
				dist = float64(r.X1 - cur.X2)
				perpDst = math.Abs(float64(r.Y1+r.Y2)/2 - srcCenterY)
			}
		}
		if !inDir {
			continue
		}

		candidates = append(candidates, candidate{rect: r, dist: dist, perpDst: perpDst})
	}

	// Pick nearest by edge distance, then closest perpendicular center as tiebreaker
	var best *gridRect
	bestDist := math.MaxFloat64
	bestPerp := math.MaxFloat64
	for _, c := range candidates {
		if c.dist < bestDist || (c.dist == bestDist && c.perpDst < bestPerp) {
			bestDist = c.dist
			bestPerp = c.perpDst
			best = c.rect
		}
	}

	if best == nil {
		m.LastNavDebug += fmt.Sprintf(" → from %s: 0 of %d candidates", cur.PaneID, len(candidates))
		return "", false
	}

	m.LastNavDebug += fmt.Sprintf(" → %s→%s (d=%.0f p=%.1f, %d cands)", cur.PaneID, best.PaneID, bestDist, bestPerp, len(candidates))
	m.UpdateSelected(best.PaneID)
	return best.PaneID, best.Status != PaneStatusNone
}

// ViewSize returns the rendered minimap dimensions without calling View().
// Returns (0, 0) if the minimap would render empty.
func (m MinimapModel) ViewSize() (width, height int) {
	if len(m.panes) == 0 || m.height < 5 {
		return 0, 0
	}
	windows, _, _, innerW, gridH := m.computeLayout()
	if len(windows) == 0 || innerW < 8 || gridH < 1 {
		return 0, 0
	}
	// border (2) + padding (2) + session label (1) + window tab labels (1) + gridH rows
	return innerW + 4, gridH + 4
}

// PaneAtGridCoord hit-tests a grid coordinate against computeGridRects.
// Returns (paneID, isClaude) if a pane owns that cell, or ("", false) otherwise.
func (m MinimapModel) PaneAtGridCoord(gridX, gridY int) (string, bool) {
	rects := m.computeGridRects()
	for _, r := range rects {
		if gridX >= r.X1 && gridX < r.X2 && gridY >= r.Y1 && gridY < r.Y2 {
			return r.PaneID, r.Status != PaneStatusNone
		}
	}
	return "", false
}

func (m MinimapModel) SelectedPaneID() string {
	return m.selectedPaneID
}

// SelectedPaneInfo returns full switch info for the currently selected pane.
func (m MinimapModel) SelectedPaneInfo() (MinimapPaneInfo, bool) {
	for _, p := range m.panes {
		if p.PaneID == m.selectedPaneID {
			return MinimapPaneInfo{
				PaneID:      p.PaneID,
				SessionName: p.SessionName,
				WindowIndex: p.WindowIndex,
				PaneIndex:   p.PaneIndex,
				PaneTitle:   p.PaneTitle,
				IsClaude:    p.Status != PaneStatusNone,
			}, true
		}
	}
	return MinimapPaneInfo{}, false
}

// DebugInfo returns a debug string showing grid rects used for navigation.
func (m MinimapModel) DebugInfo() string {
	rects := m.computeGridRects()
	if len(rects) == 0 {
		return fmt.Sprintf("sel=%s height=%d (no rects)", m.selectedPaneID, m.height)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("sel=%s height=%d", m.selectedPaneID, m.height))
	if m.LastNavDebug != "" {
		lines = append(lines, m.LastNavDebug)
	}
	for _, r := range rects {
		marker := " "
		if r.PaneID == m.selectedPaneID {
			marker = ">"
		}
		lines = append(lines, fmt.Sprintf("%s%s X[%d..%d] Y[%d..%d]",
			marker, r.PaneID, r.X1, r.X2, r.Y1, r.Y2))
	}
	return strings.Join(lines, "\n")
}

func (m *MinimapModel) UpdateStatus(paneStatuses map[string]int) {
	for i := range m.panes {
		m.panes[i].Status = paneStatuses[m.panes[i].PaneID]
	}
}

func (m MinimapModel) View() string {
	return m.view(0)
}

// ViewDocked renders the minimap with its border stretched to the given width.
// Content that overflows the border is truncated.
func (m MinimapModel) ViewDocked(outerWidth int) string {
	return m.view(outerWidth)
}

func (m MinimapModel) view(dockWidth int) string {
	if len(m.panes) == 0 || m.height < 5 {
		return ""
	}

	windows, winCols, winRows, innerW, gridH := m.computeLayout()
	if len(windows) == 0 || innerW < 8 || gridH < 1 {
		return ""
	}

	visibleWindows := windows
	hiddenBefore, hiddenAfter := 0, 0

	// Find which window contains the selected pane and pre-build its label style
	selectedWindowIdx := -1
	selectedLabelStyle := lipgloss.NewStyle()
	for _, p := range m.panes {
		if p.PaneID == m.selectedPaneID {
			selectedWindowIdx = p.WindowIndex
			fg := lipgloss.AdaptiveColor{Light: "#374151", Dark: "#e5e7eb"}
			if p.Status != PaneStatusNone {
				fg = AvatarColor(p.AvatarColorIdx)
			}
			selectedLabelStyle = lipgloss.NewStyle().Foreground(fg).Bold(true)
			break
		}
	}

	// Render per-window columns: centered label + grid
	var windowColumns []string
	for i, w := range visibleWindows {
		cols := winCols[i]
		rows := winRows[i]

		// Centered window index label — highlight if it contains the selected pane
		labelText := truncateStr(fmt.Sprintf("%d:%s", w.Index, w.Name), cols)
		labelStyle := minimapTabStyle
		if w.Index == selectedWindowIdx {
			labelStyle = selectedLabelStyle
		}
		label := labelStyle.Render(labelText)
		labelWidth := ansi.StringWidth(label)
		pad := (cols - labelWidth) / 2
		if pad < 0 {
			pad = 0
		}
		centeredLabel := strings.Repeat(" ", pad) + label

		grid := renderWindowGrid(w, cols, rows, m.spinnerView)
		// Vertically center collapsed windows within the full grid height
		if rows < gridH {
			topPad := (gridH - rows) / 2
			botPad := gridH - rows - topPad
			blank := strings.Repeat(" ", cols)
			var padded []string
			for j := 0; j < topPad; j++ {
				padded = append(padded, blank)
			}
			padded = append(padded, strings.Split(grid, "\n")...)
			for j := 0; j < botPad; j++ {
				padded = append(padded, blank)
			}
			grid = strings.Join(padded, "\n")
		}
		windowColumns = append(windowColumns, centeredLabel+"\n"+grid)
	}

	// Hidden window indicators
	prefix := ""
	suffix := ""
	if hiddenBefore > 0 {
		prefix = minimapTabStyle.Render(fmt.Sprintf("+%d", hiddenBefore))
	}
	if hiddenAfter > 0 {
		suffix = minimapTabStyle.Render(fmt.Sprintf("+%d", hiddenAfter))
	}

	// Build vertical separator between windows: 3-char centered stub
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#e5e7eb", Dark: "#2d3341"})
	sepVisible := 2
	if m.collapse {
		sepVisible = collapsedGridH // match collapsed box height for alignment
	}
	if sepVisible > gridH {
		sepVisible = gridH
	}
	topPad := (gridH - sepVisible) / 2
	var sepLines []string
	sepLines = append(sepLines, "   ") // label row blank
	for r := 0; r < gridH; r++ {
		if r >= topPad && r < topPad+sepVisible {
			sepLines = append(sepLines, " "+sepStyle.Render("│")+" ")
		} else {
			sepLines = append(sepLines, "   ")
		}
	}
	sep := strings.Join(sepLines, "\n")

	parts := interleave(windowColumns, sep)
	if prefix != "" {
		// Pad prefix to grid height (+1 label row)
		prefixCol := prefix + strings.Repeat("\n ", gridH)
		parts = append([]string{prefixCol, sep}, parts...)
	}
	if suffix != "" {
		suffixCol := suffix + strings.Repeat("\n ", gridH)
		parts = append(parts, sep, suffixCol)
	}

	gridStr := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	// Session name, centered
	sessionLabel := minimapSessionStyle.
		Width(innerW).
		Align(lipgloss.Center).
		Render(truncateStr(m.sessionName, innerW))

	// Compose inner content
	inner := lipgloss.JoinVertical(lipgloss.Left,
		sessionLabel,
		gridStr,
	)

	// In docked mode, stretch border to fill container width;
	// truncate inner content that overflows the budget.
	borderW := innerW + 2 // +2 so padding doesn't eat into content
	if dockWidth > 0 {
		borderW = dockWidth - 2 // subtract left+right border chars
		contentW := borderW - 2 // subtract left+right padding
		if innerW > contentW {
			lines := strings.Split(inner, "\n")
			for i, line := range lines {
				if ansi.StringWidth(line) > contentW {
					lines[i] = ansi.Truncate(line, contentW, "")
				}
			}
			inner = strings.Join(lines, "\n")
		}
	}

	return minimapBorderStyle.
		Width(borderW).
		Render(inner)
}

func (m MinimapModel) groupByWindow() []windowGroup {
	wmap := map[int]*windowGroup{}
	for _, p := range m.panes {
		wg, ok := wmap[p.WindowIndex]
		if !ok {
			wg = &windowGroup{Index: p.WindowIndex, Name: p.WindowName}
			wmap[p.WindowIndex] = wg
		}
		wg.Panes = append(wg.Panes, p)
		if p.WindowWidth > wg.Width {
			wg.Width = p.WindowWidth
		}
		if p.WindowHeight > wg.Height {
			wg.Height = p.WindowHeight
		}
	}
	var result []windowGroup
	for _, wg := range wmap {
		result = append(result, *wg)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Index < result[j].Index
	})
	return result
}


// paneStatusStyles holds all visual attributes for a minimap pane status.
type paneStatusStyles struct {
	Style       lipgloss.Style
	BorderColor lipgloss.AdaptiveColor
	FillBg      lipgloss.AdaptiveColor
}

var statusStyleMap = map[int]paneStatusStyles{
	PaneStatusAgentTurn: {
		Style:       minimapPaneWorkingStyle,
		BorderColor: ColorWorking,
		FillBg:      lipgloss.AdaptiveColor{Light: "#fef3c7", Dark: "#332510"}, // amber tint
	},
	PaneStatusUserTurn: {
		Style:       minimapPaneDoneStyle,
		BorderColor: ColorDone,
		FillBg:      lipgloss.AdaptiveColor{Light: "#dbeafe", Dark: "#1e2240"}, // blue tint
	},
	PaneStatusLater: {
		Style:       minimapPaneLaterStyle,
		BorderColor: ColorLater,
		FillBg:      lipgloss.AdaptiveColor{Light: "#ede9fe", Dark: "#2a1e40"}, // purple tint
	},
}

var statusStyleDefault = paneStatusStyles{
	Style:       minimapPaneDimStyle,
	BorderColor: ColorMuted,
	FillBg:      lipgloss.AdaptiveColor{Light: "#f1f2f4", Dark: "#1e2230"}, // gray tint
}

func stylesForStatus(status int) paneStatusStyles {
	if s, ok := statusStyleMap[status]; ok {
		return s
	}
	return statusStyleDefault
}

func renderWindowGrid(w windowGroup, cols, rows int, spinnerView string) string {
	if w.Width == 0 || w.Height == 0 || cols < 3 || rows < 1 {
		return strings.Repeat("\n", rows)
	}

	// Create a 2D grid of cells
	grid := make([][]gridCell, rows)
	for r := range grid {
		grid[r] = make([]gridCell, cols)
	}

	// Track the selected pane to render as a lipgloss box overlay
	type selPaneInfo struct {
		pane            minimapPane
		x1, y1, x2, y2 int
	}
	var selPane *selPaneInfo

	for _, p := range w.Panes {
		// Scale pane coordinates to grid
		x1 := int(math.Round(float64(p.Left) / float64(w.Width) * float64(cols)))
		y1 := int(math.Round(float64(p.Top) / float64(w.Height) * float64(rows)))
		x2 := int(math.Round(float64(p.Left+p.Width) / float64(w.Width) * float64(cols)))
		y2 := int(math.Round(float64(p.Top+p.Height) / float64(w.Height) * float64(rows)))

		// Enforce minimum size
		if x2-x1 < 3 {
			x2 = x1 + 3
		}
		if y2-y1 < 1 {
			y2 = y1 + 1
		}

		// Clamp to grid
		if x1 < 0 {
			x1 = 0
		}
		if y1 < 0 {
			y1 = 0
		}
		if x2 > cols {
			x2 = cols
		}
		if y2 > rows {
			y2 = rows
		}

		if p.IsSelected {
			// Defer selected pane to lipgloss box renderer — Background() fills
			// only the content area (not border chars), so no fill extrusion.
			pCopy := p
			selPane = &selPaneInfo{pane: pCopy, x1: x1, y1: y1, x2: x2, y2: y2}
			continue
		}

		ss := stylesForStatus(p.Status)
		isClaude := p.Status != PaneStatusNone

		tl, tr, bl, br, hz, vt := "┌", "┐", "└", "┘", "─", "│"

		// For Claude panes, use avatar color for borders and avatar glyph as center icon
		var avatarSt lipgloss.Style
		if isClaude {
			avatarSt = lipgloss.NewStyle().Foreground(AvatarColor(p.AvatarColorIdx))
		}

		// Interior dimensions (excluding border)
		paneW := x2 - x1
		paneH := y2 - y1

		// Center position for avatar glyph
		centerR := (y1 + y2 - 1) / 2
		centerC := (x1 + x2 - 1) / 2

		// Spinner placement for agent-turn Claude panes
		hasSpinner := isClaude && p.Status == PaneStatusAgentTurn && spinnerView != ""
		spinR, spinC := -1, -1
		if hasSpinner {
			innerW := paneW - 2 // excluding left+right border
			innerH := paneH - 2 // excluding top+bottom border
			if innerW >= 3 {
				// Horizontal: spinner one cell right of avatar
				spinR = centerR
				spinC = centerC + 2
			} else if innerH >= 2 {
				// Vertical: spinner one row below avatar
				spinR = centerR + 1
				spinC = centerC
			}
			// else: no room, skip spinner
		}

		// Draw box characters
		for r := y1; r < y2; r++ {
			for c := x1; c < x2; c++ {
				if c >= cols || r >= rows {
					continue
				}
				var ch string
				cellStyle := ss.Style
				isTop := r == y1
				isBot := r == y2-1
				isLeft := c == x1
				isRight := c == x2-1
				isCenter := r == centerR && c == centerC
				isSpinner := r == spinR && c == spinC

				switch {
				case isTop && isLeft:
					ch = tl
				case isTop && isRight:
					ch = tr
				case isBot && isLeft:
					ch = bl
				case isBot && isRight:
					ch = br
				case isTop || isBot:
					ch = hz
				case isLeft || isRight:
					ch = vt
				case isClaude && isCenter:
					ch = AvatarGlyph(p.AvatarAnimalIdx)
				case hasSpinner && isSpinner:
					ch = spinnerView
				default:
					ch = " "
				}

				// Claude panes: use avatar color for borders, icon, and spinner
				if isClaude && (isTop || isBot || isLeft || isRight || isCenter || isSpinner) {
					cellStyle = avatarSt
				}

				grid[r][c] = gridCell{char: ch, style: cellStyle}
			}
		}
	}

	// Render non-selected panes to string
	var lines []string
	for _, row := range grid {
		var line strings.Builder
		for _, cell := range row {
			if cell.char == "" {
				line.WriteString(" ")
			} else {
				line.WriteString(cell.style.Render(cell.char))
			}
		}
		lines = append(lines, line.String())
	}
	gridStr := strings.Join(lines, "\n")

	// Overlay the selected pane as a proper lipgloss box.
	// lipgloss.Background() fills content area only — border chars stay on
	// terminal default background, giving a clean "fill inside the box" look.
	if selPane != nil {
		// innerW excludes border (2) + 1-col side gaps (2) so padding on the box
		// creates terminal-default-bg gaps between border and fill.
		innerW := (selPane.x2 - selPane.x1) - 4
		innerH := (selPane.y2 - selPane.y1) - 2
		if innerW < 1 {
			innerW = 1
		}
		if innerH < 1 {
			innerH = 1
		}

		ss := stylesForStatus(selPane.pane.Status)
		isClaude := selPane.pane.Status != PaneStatusNone

		borderColor := ss.BorderColor
		fillBg := ss.FillBg
		iconStr := ""
		hAlign := lipgloss.Center
		if isClaude {
			avatarColor := AvatarColor(selPane.pane.AvatarColorIdx)
			borderColor = avatarColor
			fillBg = AvatarFillBg(selPane.pane.AvatarColorIdx)
			glyph := AvatarGlyph(selPane.pane.AvatarAnimalIdx)
			glyphW := ansi.StringWidth(glyph)

			hasSpinner := selPane.pane.Status == PaneStatusAgentTurn && spinnerView != ""
			if hasSpinner {
				spinW := ansi.StringWidth(spinnerView)
				glyphPad := max((innerW-glyphW)/2, 0)
				if glyphPad+glyphW+1+spinW <= innerW {
					hAlign = lipgloss.Left
					iconStr = strings.Repeat(" ", glyphPad) + glyph + " " + spinnerView
				} else if innerH >= 2 {
					iconStr = glyph + "\n" + spinnerView
				} else {
					iconStr = glyph
				}
			} else {
				iconStr = glyph
			}
		}

		// Interior style carries fg+bg together so there are no ANSI reset
		// gaps between content segments (glyph, space, spinner).
		interiorSt := lipgloss.NewStyle().
			Width(innerW).
			Height(innerH).
			Align(hAlign).
			AlignVertical(lipgloss.Center).
			Background(fillBg)
		if isClaude {
			interiorSt = interiorSt.Foreground(AvatarColor(selPane.pane.AvatarColorIdx))
		}
		interior := interiorSt.Render(iconStr)
		box := lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(borderColor).
			PaddingLeft(1).
			PaddingRight(1).
			Render(interior)

		gridStr = overlayAt(gridStr, box, selPane.x1, selPane.y1)
	}

	return gridStr
}

// overlayAt composites overlay onto base starting at (col, row) in visible cell coordinates.
func overlayAt(base, overlay string, col, row int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	for i, oLine := range overlayLines {
		r := row + i
		if r < 0 || r >= len(baseLines) {
			continue
		}
		oWidth := ansi.StringWidth(oLine)
		baseLine := baseLines[r]
		baseWidth := ansi.StringWidth(baseLine)

		var prefix string
		if col > 0 {
			if col <= baseWidth {
				prefix = ansi.Truncate(baseLine, col, "")
			} else {
				prefix = baseLine + strings.Repeat(" ", col-baseWidth)
			}
		}

		suffix := ""
		if afterCol := col + oWidth; afterCol < baseWidth {
			suffix = ansi.TruncateLeft(baseLine, afterCol, "")
		}

		baseLines[r] = prefix + oLine + suffix
	}

	return strings.Join(baseLines, "\n")
}

type gridCell struct {
	char  string
	style lipgloss.Style
}

func interleave(items []string, sep string) []string {
	if len(items) <= 1 {
		return items
	}
	result := make([]string, 0, len(items)*2-1)
	for i, item := range items {
		if i > 0 {
			result = append(result, sep)
		}
		result = append(result, item)
	}
	return result
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// OverlayBottomLeft composites the overlay string onto the base at bottom-left.
func OverlayBottomLeft(base, overlay string) string {
	if overlay == "" {
		return base
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	// Start overlay at bottom of base
	startRow := len(baseLines) - len(overlayLines)
	if startRow < 0 {
		startRow = 0
	}

	for i, oLine := range overlayLines {
		row := startRow + i
		if row >= len(baseLines) {
			break
		}

		oWidth := ansi.StringWidth(oLine)
		baseWidth := ansi.StringWidth(baseLines[row])

		if oWidth >= baseWidth {
			baseLines[row] = oLine
		} else {
			// Replace the left portion of the base line with the overlay line
			// We need to truncate base from the right side after the overlay
			remainder := ansi.TruncateLeft(baseLines[row], oWidth, "")
			baseLines[row] = oLine + remainder
		}
	}

	return strings.Join(baseLines, "\n")
}
