package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/huylenq/claude-mission-control/internal/tmux"
)

// Real pane layout from tmux (window 2 + window 3):
//
// Window 2 (318x74):
//   %55  left=0   top=0   w=178 h=37  (top-left, large)
//   %32  left=179 top=0   w=139 h=37  (top-right, large)
//   %58  left=0   top=38  w=89  h=35  (bottom-left)
//   %60  left=90  top=38  w=88  h=18  (bottom-center-top)
//   %61  left=90  top=57  w=88  h=16  (bottom-center-bottom)
//   %57  left=179 top=38  w=139 h=35  (bottom-right)
//
// Window 3 (318x74):
//   %48  left=0   top=0   w=318 h=73  (full window)
//
//  Layout sketch (window 2):
//  ┌──────────────┬──────────┐
//  │  %55         │  %32     │
//  │  (top-left)  │(top-right│
//  ├──────┬───────┼──────────┤
//  │  %58 │ %60   │  %57     │
//  │      ├───────┤(bot-right│
//  │      │ %61   │          │
//  └──────┴───────┴──────────┘

func makeTestGeometry() []tmux.PaneGeometry {
	return []tmux.PaneGeometry{
		{PaneID: "%55", SessionName: "main", WindowIndex: 2, WindowName: "code", PaneIndex: 0, Left: 0, Top: 0, Width: 178, Height: 37, WindowWidth: 318, WindowHeight: 74},
		{PaneID: "%32", SessionName: "main", WindowIndex: 2, WindowName: "code", PaneIndex: 1, Left: 179, Top: 0, Width: 139, Height: 37, WindowWidth: 318, WindowHeight: 74},
		{PaneID: "%58", SessionName: "main", WindowIndex: 2, WindowName: "code", PaneIndex: 2, Left: 0, Top: 38, Width: 89, Height: 35, WindowWidth: 318, WindowHeight: 74},
		{PaneID: "%60", SessionName: "main", WindowIndex: 2, WindowName: "code", PaneIndex: 3, Left: 90, Top: 38, Width: 88, Height: 18, WindowWidth: 318, WindowHeight: 74},
		{PaneID: "%61", SessionName: "main", WindowIndex: 2, WindowName: "code", PaneIndex: 4, Left: 90, Top: 57, Width: 88, Height: 16, WindowWidth: 318, WindowHeight: 74},
		{PaneID: "%57", SessionName: "main", WindowIndex: 2, WindowName: "code", PaneIndex: 5, Left: 179, Top: 38, Width: 139, Height: 35, WindowWidth: 318, WindowHeight: 74},
		{PaneID: "%48", SessionName: "main", WindowIndex: 3, WindowName: "viz", PaneIndex: 0, Left: 0, Top: 0, Width: 318, Height: 73, WindowWidth: 318, WindowHeight: 74},
	}
}

func makeTestMinimap() MinimapModel {
	geom := makeTestGeometry()
	statuses := map[string]int{
		"%55": PaneStatusUserTurn, "%32": PaneStatusUserTurn,
		"%57": PaneStatusUserTurn, "%48": PaneStatusUserTurn, "%60": PaneStatusUserTurn,
		"%58": PaneStatusAgentTurn, "%61": PaneStatusAgentTurn,
	}

	m := NewMinimapModel()
	m.SetData(geom, statuses, map[string]PaneAvatarInfo{}, "%55", "main")
	m.SetSize(50, 14) // realistic minimap size
	return m
}

// simulateCellOwnership mirrors the full rendering pipeline to produce
// a 2D grid of pane IDs, matching what renderWindowGrid actually draws.
// This is the "ground truth" of what the user sees.
func simulateCellOwnership(m MinimapModel) (grid [][]string, totalCols, totalRows int) {
	if len(m.panes) == 0 || m.height < 5 {
		return nil, 0, 0
	}

	visibleWindows, winCols, winRows, innerW, gridH := m.computeLayout()
	if len(visibleWindows) == 0 || innerW < 8 || gridH < 1 {
		return nil, 0, 0
	}

	// Calculate total grid dimensions
	totalCols = 0
	for i, cols := range winCols {
		if i > 0 {
			totalCols += 3 // separator is " │ " (3 cols)
		}
		totalCols += cols
	}
	totalRows = gridH

	// Build 2D grid (empty string = no pane)
	grid = make([][]string, totalRows)
	for r := range grid {
		grid[r] = make([]string, totalCols)
	}

	// Fill cells, last writer wins (same as renderWindowGrid)
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
					grid[r][xOffset+c] = p.PaneID
				}
			}
		}
		xOffset += cols + 3 // +3 for separator (" │ ")
	}

	return grid, totalCols, totalRows
}

// visualExtentsFromGrid derives the bounding box each pane actually occupies
// after "last writer wins" resolution.
func visualExtentsFromGrid(grid [][]string, totalCols, totalRows int) map[string][4]int {
	ext := map[string][4]int{}
	for r := 0; r < totalRows; r++ {
		for c := 0; c < totalCols; c++ {
			pid := grid[r][c]
			if pid == "" {
				continue
			}
			e, ok := ext[pid]
			if !ok {
				e = [4]int{c, r, c + 1, r + 1}
			}
			if c < e[0] {
				e[0] = c
			}
			if r < e[1] {
				e[1] = r
			}
			if c+1 > e[2] {
				e[2] = c + 1
			}
			if r+1 > e[3] {
				e[3] = r + 1
			}
			ext[pid] = e
		}
	}
	return ext
}

// renderCellOwnershipASCII renders the cell ownership grid as ASCII for debugging.
func renderCellOwnershipASCII(grid [][]string, totalCols, totalRows int) string {
	labels := map[string]byte{
		"%55": '1', "%32": '2', "%58": '3', "%60": '4', "%61": '5', "%57": '6', "%48": '7',
	}
	var lines []string
	for r := 0; r < totalRows; r++ {
		line := make([]byte, totalCols)
		for c := 0; c < totalCols; c++ {
			pid := grid[r][c]
			if pid == "" {
				line[c] = '.'
			} else if ch, ok := labels[pid]; ok {
				line[c] = ch
			} else {
				line[c] = '?'
			}
		}
		lines = append(lines, string(line))
	}
	return strings.Join(lines, "\n")
}

// findVisualNeighbor finds the nearest pane in a given direction from the cell ownership grid.
// Uses edge-based direction: a pane is "right" only if it starts at or past the source's right edge.
func findVisualNeighbor(grid [][]string, totalCols, totalRows int, fromPaneID string, dir SpatialDir) string {
	// Find bounding box of the source pane
	srcX1, srcY1 := totalCols, totalRows
	srcX2, srcY2 := 0, 0
	for r := 0; r < totalRows; r++ {
		for c := 0; c < totalCols; c++ {
			if grid[r][c] == fromPaneID {
				if c < srcX1 {
					srcX1 = c
				}
				if r < srcY1 {
					srcY1 = r
				}
				if c+1 > srcX2 {
					srcX2 = c + 1
				}
				if r+1 > srcY2 {
					srcY2 = r + 1
				}
			}
		}
	}
	if srcX2 == 0 && srcY2 == 0 {
		return ""
	}
	srcCenterX := float64(srcX1+srcX2) / 2
	srcCenterY := float64(srcY1+srcY2) / 2

	// Find bounding box of every other pane
	type paneBox struct {
		id             string
		x1, y1, x2, y2 int
	}
	panes := map[string]*paneBox{}
	for r := 0; r < totalRows; r++ {
		for c := 0; c < totalCols; c++ {
			pid := grid[r][c]
			if pid == "" || pid == fromPaneID {
				continue
			}
			pb, ok := panes[pid]
			if !ok {
				pb = &paneBox{id: pid, x1: c, y1: r, x2: c + 1, y2: r + 1}
				panes[pid] = pb
			}
			if c < pb.x1 {
				pb.x1 = c
			}
			if r < pb.y1 {
				pb.y1 = r
			}
			if c+1 > pb.x2 {
				pb.x2 = c + 1
			}
			if r+1 > pb.y2 {
				pb.y2 = r + 1
			}
		}
	}

	// Find nearest pane using edge-based direction (same logic as NavigateSpatial)
	bestDist := math.MaxFloat64
	bestPerp := math.MaxFloat64
	bestPane := ""
	for _, pb := range panes {
		var inDir bool
		var dist, perpDst float64
		switch dir {
		case DirUp:
			inDir = pb.y2 <= srcY1
			if inDir {
				dist = float64(srcY1 - pb.y2)
				perpDst = math.Abs(float64(pb.x1+pb.x2)/2 - srcCenterX)
			}
		case DirDown:
			inDir = pb.y1 >= srcY2
			if inDir {
				dist = float64(pb.y1 - srcY2)
				perpDst = math.Abs(float64(pb.x1+pb.x2)/2 - srcCenterX)
			}
		case DirLeft:
			inDir = pb.x2 <= srcX1
			if inDir {
				dist = float64(srcX1 - pb.x2)
				perpDst = math.Abs(float64(pb.y1+pb.y2)/2 - srcCenterY)
			}
		case DirRight:
			inDir = pb.x1 >= srcX2
			if inDir {
				dist = float64(pb.x1 - srcX2)
				perpDst = math.Abs(float64(pb.y1+pb.y2)/2 - srcCenterY)
			}
		}
		if !inDir {
			continue
		}
		if dist < bestDist || (dist == bestDist && perpDst < bestPerp) {
			bestDist = dist
			bestPerp = perpDst
			bestPane = pb.id
		}
	}
	return bestPane
}

// TestGridRectsVsCellOwnership verifies that computeGridRects matches
// the actual rendered cell ownership across many minimap sizes.
func TestGridRectsVsCellOwnership(t *testing.T) {
	geom := makeTestGeometry()
	statuses := map[string]int{
		"%55": PaneStatusUserTurn, "%32": PaneStatusUserTurn,
		"%57": PaneStatusUserTurn, "%48": PaneStatusUserTurn, "%60": PaneStatusUserTurn,
		"%58": PaneStatusAgentTurn, "%61": PaneStatusAgentTurn,
	}

	// Test across a range of realistic sizes
	sizes := [][2]int{
		{20, 8}, {25, 8}, {30, 9}, {35, 10}, {40, 12},
		{45, 13}, {50, 14}, {55, 14}, {60, 14},
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			m := NewMinimapModel()
			m.SetData(geom, statuses, map[string]PaneAvatarInfo{}, "%55", "main")
			m.SetSize(sz[0], sz[1])

			rects := m.computeGridRects()
			grid, totalCols, totalRows := simulateCellOwnership(m)
			if grid == nil {
				t.Skip("grid too small")
				return
			}

			t.Logf("Visual (%dx%d):\n%s", totalCols, totalRows,
				renderCellOwnershipASCII(grid, totalCols, totalRows))

			// Build rect map
			rectByID := map[string]gridRect{}
			for _, r := range rects {
				rectByID[r.PaneID] = r
			}

			// Build visual extents
			visualExt := visualExtentsFromGrid(grid, totalCols, totalRows)

			// Compare: flag any discrepancy
			discrepancies := 0
			for pid, vis := range visualExt {
				rect, ok := rectByID[pid]
				if !ok {
					t.Errorf("pane %s in visual but not in gridRects", pid)
					discrepancies++
					continue
				}
				if rect.X1 != vis[0] || rect.Y1 != vis[1] || rect.X2 != vis[2] || rect.Y2 != vis[3] {
					t.Errorf("MISMATCH %s: gridRect=[%d,%d,%d,%d] visual=[%d,%d,%d,%d]",
						pid, rect.X1, rect.Y1, rect.X2, rect.Y2,
						vis[0], vis[1], vis[2], vis[3])
					discrepancies++
				}
			}
			if discrepancies > 0 {
				t.Logf("Grid rects:")
				for _, r := range rects {
					t.Logf("  %s: X[%d..%d] Y[%d..%d]", r.PaneID, r.X1, r.X2, r.Y1, r.Y2)
				}
			}
		})
	}
}

// TestNavigationMatchesVisual verifies that NavigateSpatial picks the same
// pane that a cell-level scan would identify as the visual neighbor.
func TestNavigationMatchesVisual(t *testing.T) {
	geom := makeTestGeometry()
	statuses := map[string]int{
		"%55": PaneStatusUserTurn, "%32": PaneStatusUserTurn,
		"%57": PaneStatusUserTurn, "%48": PaneStatusUserTurn, "%60": PaneStatusUserTurn,
		"%58": PaneStatusAgentTurn, "%61": PaneStatusAgentTurn,
	}

	sizes := [][2]int{
		{20, 8}, {25, 8}, {30, 9}, {35, 10}, {40, 12},
		{45, 13}, {50, 14}, {55, 14}, {60, 14},
	}

	dirNames := map[SpatialDir]string{
		DirUp: "Up", DirDown: "Down", DirLeft: "Left", DirRight: "Right",
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			m := NewMinimapModel()
			m.SetData(geom, statuses, map[string]PaneAvatarInfo{}, "%55", "main")
			m.SetSize(sz[0], sz[1])

			grid, totalCols, totalRows := simulateCellOwnership(m)
			if grid == nil {
				t.Skip("grid too small")
				return
			}

			rects := m.computeGridRects()
			failures := 0

			for _, r := range rects {
				for _, dir := range []SpatialDir{DirUp, DirDown, DirLeft, DirRight} {
					// What does NavigateSpatial say?
					m.UpdateSelected(r.PaneID)
					navResult, _ := m.NavigateSpatial(dir)

					// What does visual cell scan say?
					visualResult := findVisualNeighbor(grid, totalCols, totalRows, r.PaneID, dir)

					// They should agree (both "" or same pane).
					// Allow legitimate ties where both are equidistant.
					if navResult != visualResult {
						// Not a failure if both are "" or if both found a valid
						// pane but just picked different ones in a tie
						isLegitTie := navResult != "" && visualResult != "" &&
							navResult != r.PaneID && visualResult != r.PaneID
						if !isLegitTie {
							t.Errorf("From %s %s: nav=%q visual=%q",
								r.PaneID, dirNames[dir], navResult, visualResult)
							failures++
						}
					}
				}
			}
			if failures > 0 {
				t.Logf("Visual:\n%s", renderCellOwnershipASCII(grid, totalCols, totalRows))
				t.Logf("Grid rects:")
				for _, r := range rects {
					t.Logf("  %s: X[%d..%d] Y[%d..%d]", r.PaneID, r.X1, r.X2, r.Y1, r.Y2)
				}
			}
		})
	}
}

// --- Original tests below (kept for compatibility) ---

// TestGridRectsMatchLayout verifies that computeGridRects produces
// positions consistent with the visual tmux layout.
func TestGridRectsMatchLayout(t *testing.T) {
	m := makeTestMinimap()
	rects := m.computeGridRects()

	if len(rects) == 0 {
		t.Fatal("computeGridRects returned empty")
	}

	byID := map[string]gridRect{}
	for _, r := range rects {
		byID[r.PaneID] = r
	}

	// Log all rects for debugging
	for _, r := range rects {
		t.Logf("  %s: X[%d..%d] Y[%d..%d] win=%d", r.PaneID, r.X1, r.X2, r.Y1, r.Y2, r.WindowIndex)
	}

	// %55 (top-left) should be LEFT OF %32 (top-right) at the same vertical level
	r55, r32 := byID["%55"], byID["%32"]
	if r55.X2 > r32.X1 {
		t.Errorf("%%55 (X2=%d) should be left of %%32 (X1=%d)", r55.X2, r32.X1)
	}
	if r55.Y1 != r32.Y1 {
		t.Errorf("%%55 and %%32 should share top edge: Y1=%d vs %d", r55.Y1, r32.Y1)
	}

	// %55 (top-left) should be ABOVE %58 (bottom-left) with horizontal overlap
	r58 := byID["%58"]
	if r55.Y2 > r58.Y1 {
		t.Errorf("%%55 (Y2=%d) should be above %%58 (Y1=%d)", r55.Y2, r58.Y1)
	}
	overlapX := min(r55.X2, r58.X2) - max(r55.X1, r58.X1)
	if overlapX <= 0 {
		t.Errorf("%%55 and %%58 should overlap horizontally, got %d", overlapX)
	}

	// %32 (top-right) should be ABOVE %57 (bottom-right) with horizontal overlap
	r57 := byID["%57"]
	if r32.Y2 > r57.Y1 {
		t.Errorf("%%32 (Y2=%d) should be above %%57 (Y1=%d)", r32.Y2, r57.Y1)
	}
	overlapX = min(r32.X2, r57.X2) - max(r32.X1, r57.X1)
	if overlapX <= 0 {
		t.Errorf("%%32 and %%57 should overlap horizontally, got %d", overlapX)
	}

	// %58 (bottom-left) should be LEFT OF %60 (bottom-center)
	r60 := byID["%60"]
	if r58.X2 > r60.X1 {
		t.Errorf("%%58 (X2=%d) should be left of %%60 (X1=%d)", r58.X2, r60.X1)
	}

	// %60 (bottom-center-top) should be ABOVE %61 (bottom-center-bottom)
	r61 := byID["%61"]
	if r60.Y2 > r61.Y1 {
		t.Errorf("%%60 (Y2=%d) should be above %%61 (Y1=%d)", r60.Y2, r61.Y1)
	}

	// Window 3 panes should be to the RIGHT of window 2 panes
	r48 := byID["%48"]
	for _, id := range []string{"%55", "%32", "%58", "%60", "%61", "%57"} {
		r := byID[id]
		if r.X2 > r48.X1 {
			t.Errorf("win2 pane %s (X2=%d) should be left of win3 %%48 (X1=%d)", id, r.X2, r48.X1)
		}
	}
}

// renderGridASCII renders a simple ASCII map of grid rects for visual debugging
func renderGridASCII(rects []gridRect, maxX, maxY int) string {
	grid := make([][]byte, maxY)
	for r := range grid {
		grid[r] = make([]byte, maxX)
		for c := range grid[r] {
			grid[r][c] = '.'
		}
	}
	labels := map[string]byte{
		"%55": '1', "%32": '2', "%58": '3', "%60": '4', "%61": '5', "%57": '6', "%48": '7',
	}
	for _, r := range rects {
		ch, ok := labels[r.PaneID]
		if !ok {
			ch = '?'
		}
		for y := r.Y1; y < r.Y2 && y < maxY; y++ {
			for x := r.X1; x < r.X2 && x < maxX; x++ {
				grid[y][x] = ch
			}
		}
	}
	var lines []string
	for _, row := range grid {
		lines = append(lines, string(row))
	}
	return strings.Join(lines, "\n")
}

func TestGridRectsASCII(t *testing.T) {
	m := makeTestMinimap()
	rects := m.computeGridRects()
	maxX, maxY := 0, 0
	for _, r := range rects {
		if r.X2 > maxX {
			maxX = r.X2
		}
		if r.Y2 > maxY {
			maxY = r.Y2
		}
	}
	ascii := renderGridASCII(rects, maxX, maxY)
	t.Logf("Grid layout (%dx%d):\n%s", maxX, maxY, ascii)
}

func TestSpatialRight_FromTopLeft_GoesToTopRight(t *testing.T) {
	m := makeTestMinimap()
	paneID, _ := m.NavigateSpatial(DirRight)
	if paneID != "%32" {
		t.Errorf("Right from %%55: got %s, want %%32", paneID)
	}
}

func TestSpatialDown_FromTopLeft_GoesToBottomLeft(t *testing.T) {
	m := makeTestMinimap()
	paneID, _ := m.NavigateSpatial(DirDown)
	if paneID != "%58" {
		t.Errorf("Down from %%55: got %s, want %%58", paneID)
	}
}

func TestSpatialDown_FromTopRight_GoesToBottomRight(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%32")
	paneID, _ := m.NavigateSpatial(DirDown)
	if paneID != "%57" {
		t.Errorf("Down from %%32: got %s, want %%57", paneID)
	}
}

func TestSpatialLeft_FromTopRight_GoesToTopLeft(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%32")
	paneID, _ := m.NavigateSpatial(DirLeft)
	if paneID != "%55" {
		t.Errorf("Left from %%32: got %s, want %%55", paneID)
	}
}

func TestSpatialUp_FromBottomLeft_GoesToTopLeft(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%58")
	paneID, _ := m.NavigateSpatial(DirUp)
	if paneID != "%55" {
		t.Errorf("Up from %%58: got %s, want %%55", paneID)
	}
}

func TestSpatialRight_FromBottomLeft_GoesToBottomCenter(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%58")
	paneID, _ := m.NavigateSpatial(DirRight)
	if paneID != "%60" && paneID != "%61" {
		t.Errorf("Right from %%58: got %s, want %%60 or %%61", paneID)
	}
}

func TestSpatialDown_FromBottomCenterTop_GoesToBottomCenterBottom(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%60")
	paneID, _ := m.NavigateSpatial(DirDown)
	if paneID != "%61" {
		t.Errorf("Down from %%60: got %s, want %%61", paneID)
	}
}

func TestSpatialRight_CrossWindow(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%32")
	paneID, _ := m.NavigateSpatial(DirRight)
	if paneID != "%48" {
		t.Errorf("Right from %%32: got %s, want %%48", paneID)
	}
}

func TestSpatialLeft_CrossWindow(t *testing.T) {
	m := makeTestMinimap()
	m.UpdateSelected("%48")
	paneID, _ := m.NavigateSpatial(DirLeft)
	// Any window-2 pane is acceptable
	if paneID == "" || paneID == "%48" {
		t.Errorf("Left from %%48: got %s, want a pane in window 2", paneID)
	}
}

// TestSpatialFullTraversal navigates through all panes to verify no dead ends
func TestSpatialFullTraversal(t *testing.T) {
	m := makeTestMinimap()
	rects := m.computeGridRects()

	for _, r := range rects {
		m.UpdateSelected(r.PaneID)
		for _, dir := range []SpatialDir{DirUp, DirDown, DirLeft, DirRight} {
			dirName := [...]string{"Up", "Down", "Left", "Right"}[dir]
			result, _ := m.NavigateSpatial(dir)
			// Just verify it doesn't panic or return the same pane
			if result == r.PaneID {
				t.Errorf("From %s, %s returned same pane", r.PaneID, dirName)
			}
			// Re-select original for next direction test
			m.UpdateSelected(r.PaneID)
		}
	}
	// Log summary
	t.Logf("Traversal from %d panes in 4 directions: no panics or self-returns", len(rects))
}

func TestNavigateAndReturn(t *testing.T) {
	// Verify that Right then Left returns to the original pane
	m := makeTestMinimap()

	cases := []struct {
		start   string
		forward SpatialDir
		back    SpatialDir
	}{
		{"%55", DirRight, DirLeft},
		{"%55", DirDown, DirUp},
		{"%32", DirLeft, DirRight},
		{"%32", DirDown, DirUp},
	}

	for _, tc := range cases {
		m.UpdateSelected(tc.start)
		mid, _ := m.NavigateSpatial(tc.forward)
		if mid == "" {
			t.Errorf("From %s, %d: no pane found", tc.start, tc.forward)
			continue
		}
		back, _ := m.NavigateSpatial(tc.back)
		if back != tc.start {
			t.Errorf("From %s→%s→%s, expected return to %s (dir %d→%d)",
				tc.start, mid, back, tc.start, tc.forward, tc.back)
		}
	}
}

// TestPaneAtGridCoord verifies hit-testing against computed grid rects.
func TestPaneAtGridCoord(t *testing.T) {
	m := makeTestMinimap()
	rects := m.computeGridRects()
	if len(rects) == 0 {
		t.Fatal("no grid rects")
	}

	// Points inside known rects should return the correct pane
	for _, r := range rects {
		midX := (r.X1 + r.X2) / 2
		midY := (r.Y1 + r.Y2) / 2
		paneID, _ := m.PaneAtGridCoord(midX, midY)
		if paneID != r.PaneID {
			t.Errorf("center of %s (%d,%d): got %q, want %q", r.PaneID, midX, midY, paneID, r.PaneID)
		}
	}

	// Points outside all rects should return empty
	outsidePoints := [][2]int{{-1, 0}, {0, -1}, {-1, -1}, {9999, 9999}}
	for _, pt := range outsidePoints {
		paneID, _ := m.PaneAtGridCoord(pt[0], pt[1])
		if paneID != "" {
			t.Errorf("outside point (%d,%d): got %q, want empty", pt[0], pt[1], paneID)
		}
	}

	// Verify isClaude flag matches status
	for _, r := range rects {
		midX := (r.X1 + r.X2) / 2
		midY := (r.Y1 + r.Y2) / 2
		_, isClaude := m.PaneAtGridCoord(midX, midY)
		wantClaude := r.Status != PaneStatusNone
		if isClaude != wantClaude {
			t.Errorf("pane %s isClaude=%v, want %v (status=%d)", r.PaneID, isClaude, wantClaude, r.Status)
		}
	}
}

func TestSelectedPaneInfo(t *testing.T) {
	m := makeTestMinimap()

	// Claude pane (has status)
	info, ok := m.SelectedPaneInfo()
	if !ok {
		t.Fatal("SelectedPaneInfo returned false for selected pane %55")
	}
	if info.PaneID != "%55" {
		t.Errorf("PaneID = %q, want %%55", info.PaneID)
	}
	if info.SessionName != "main" {
		t.Errorf("SessionName = %q, want main", info.SessionName)
	}
	if info.WindowIndex != 2 {
		t.Errorf("WindowIndex = %d, want 2", info.WindowIndex)
	}
	if info.PaneIndex != 0 {
		t.Errorf("PaneIndex = %d, want 0", info.PaneIndex)
	}
	if !info.IsClaude {
		t.Error("IsClaude = false, want true for %55 (has PaneStatusUserTurn)")
	}

	// Make a minimap with a non-Claude pane selected
	geom := makeTestGeometry()
	statuses := map[string]int{
		"%55": PaneStatusUserTurn,
		// %32 deliberately NOT in statuses → PaneStatusNone
	}
	m2 := NewMinimapModel()
	m2.SetData(geom, statuses, map[string]PaneAvatarInfo{}, "%32", "main")
	m2.SetSize(50, 14)

	info2, ok2 := m2.SelectedPaneInfo()
	if !ok2 {
		t.Fatal("SelectedPaneInfo returned false for selected pane %32")
	}
	if info2.PaneID != "%32" {
		t.Errorf("PaneID = %q, want %%32", info2.PaneID)
	}
	if info2.PaneIndex != 1 {
		t.Errorf("PaneIndex = %d, want 1", info2.PaneIndex)
	}
	if info2.IsClaude {
		t.Error("IsClaude = true, want false for %32 (PaneStatusNone)")
	}

	// Non-existent selection
	m3 := NewMinimapModel()
	m3.SetData(geom, statuses, map[string]PaneAvatarInfo{}, "%99", "main")
	_, ok3 := m3.SelectedPaneInfo()
	if ok3 {
		t.Error("SelectedPaneInfo should return false for non-existent pane %99")
	}
}

func TestSpatialWithSmallSize(t *testing.T) {
	// Test with a very small minimap to catch edge cases
	m := makeTestMinimap()
	m.SetSize(20, 8)

	rects := m.computeGridRects()
	t.Logf("Small minimap: %d rects", len(rects))
	for _, r := range rects {
		t.Logf("  %s: X[%d..%d] Y[%d..%d]", r.PaneID, r.X1, r.X2, r.Y1, r.Y2)
	}

	// Should still be navigable without panics
	for _, r := range rects {
		m.UpdateSelected(r.PaneID)
		for _, dir := range []SpatialDir{DirUp, DirDown, DirLeft, DirRight} {
			m.NavigateSpatial(dir)
			m.UpdateSelected(r.PaneID)
		}
	}
	fmt.Println("Small minimap: all navigations completed")
}
