package ui

import (
	"math"
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// Layout computation and spatial data for MinimapModel.

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
			x1 := scaleEdge(p.Left, w.Width, cols)
			y1 := scaleEdge(p.Top, w.Height, wGridH)
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

// scaleEdge scales a pane coordinate (left or top) to grid cells.
// For non-edge panes (coord > 0), subtracts 1 to compensate for the tmux
// separator row/column between adjacent panes, eliminating rounding gaps.
func scaleEdge(coord, windowDim, gridDim int) int {
	adj := coord
	if adj > 0 {
		adj--
	}
	return int(math.Round(float64(adj) / float64(windowDim) * float64(gridDim)))
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
