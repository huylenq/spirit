package ui

import (
	"fmt"
	"math"
)

// Spatial navigation for MinimapModel.

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
