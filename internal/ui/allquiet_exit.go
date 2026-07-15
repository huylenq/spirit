package ui

import (
	"math"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// exitMorphFrames is how long the calm scene takes to bloom outward and coalesce
// into the returning normal view.
const exitMorphFrames = 22

const (
	exitStiff = 0.20 // spring pull toward the landing cell
	exitDamp  = 0.60 // velocity damping so particles settle rather than oscillate
	exitKick  = 0.6  // initial outward bloom from center before the spring gathers
)

// QuietExitAnim is the mirror of the intro morph, played when a session
// reappears in YOUR TURN and the calm mobile scene gives way to the normal
// sidebar+detail view. It seeds one particle per cell of the returning frame,
// starting each at the nearest glyph of the calm scene — so frame 0 reads as the
// calm scene, which then blooms outward and coalesces into the full workspace,
// every rune morphing into its landing glyph. Because there are far more target
// cells than calm glyphs, particles fan out from the clustered center to fill the
// frame; when they land, what remains is exactly the normal view.
type QuietExitAnim struct {
	active bool
	debris []debrisParticle
	fr     int
}

// Active reports whether the exit morph is running.
func (a *QuietExitAnim) Active() bool { return a.active }

// Start seeds the morph from the calm scene (calmSrc) toward the returning normal
// frame (target), both laid out on the same w×h canvas, and returns the first
// tick command. Returns nil (and stays inactive) if the target has no glyphs to
// assemble — the caller then simply shows the normal view.
func (a *QuietExitAnim) Start(calmSrc, target string, w, h int) tea.Cmd {
	targets := decomposeStyled(target, w, h)
	if len(targets) == 0 {
		a.active = false
		a.debris = nil
		return nil
	}
	calm := decomposeStyled(calmSrc, w, h)

	cx, cy := float64(w)/2, float64(h)/2
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	a.debris = make([]debrisParticle, len(targets))
	for i, t := range targets {
		// Start at the nearest calm glyph (or screen center if the calm scene was
		// empty) so the swarm appears to erupt from where the calm scene sat.
		sx, sy := cx, cy
		startCell := t.styled
		if len(calm) > 0 {
			best, bestDist := 0, math.MaxFloat64
			for j := range calm {
				dx, dy := float64(calm[j].x-t.x), float64(calm[j].y-t.y)
				if d := dx*dx + dy*dy; d < bestDist {
					best, bestDist = j, d
				}
			}
			sx, sy = float64(calm[best].x), float64(calm[best].y)
			startCell = calm[best].styled // carry the calm glyph until it lands
		}
		// A small outward kick from center gives the swarm an initial bloom before
		// the spring reels each particle onto its target cell.
		dx, dy := sx-cx, sy-cy
		dist := math.Hypot(dx, dy)
		if dist < 0.001 {
			dist = 0.001
		}
		ux, uy := dx/dist, dy/dist
		a.debris[i] = debrisParticle{
			x: sx, y: sy,
			vx:        ux*exitKick + (rng.Float64()-0.5)*1.2,
			vy:        uy*exitKick*0.5 + (rng.Float64()-0.5)*0.8,
			cell:      startCell,
			ttl:       exitMorphFrames + 5,
			tx:        float64(t.x),
			ty:        float64(t.y),
			hasTarget: true,
			landCell:  t.styled,
		}
	}
	a.active = true
	a.fr = 0
	return tickAllQuiet()
}

// Stop halts the morph and releases its particles.
func (a *QuietExitAnim) Stop() {
	a.active = false
	a.debris = nil
}

// Tick springs every particle toward its landing cell (morphing the rune into
// its target glyph on arrival) and returns the next tick command, or nil once the
// swarm has settled into the normal frame.
func (a *QuietExitAnim) Tick() tea.Cmd {
	if !a.active {
		return nil
	}
	a.fr++
	for i := range a.debris {
		d := &a.debris[i]
		// Critically-damped spring: the initial bloom is reeled back onto the cell.
		d.vx += (d.tx-d.x)*exitStiff - d.vx*exitDamp
		d.vy += (d.ty-d.y)*exitStiff - d.vy*exitDamp
		d.x += d.vx
		d.y += d.vy
		if math.Hypot(d.tx-d.x, d.ty-d.y) <= implodeSnap {
			d.cell = d.landCell // morph into the landing glyph as it arrives
		}
	}
	if a.fr >= exitMorphFrames {
		a.active = false
		a.debris = nil
		return nil
	}
	return tickAllQuiet()
}

// Render stamps the live swarm onto a width×height cell grid (last writer wins),
// padded to a full rectangle so it slots cleanly into the content area beside any
// docked panel. The final frame equals the target frame, so handing off to the
// normal layout is seamless.
func (a *QuietExitAnim) Render(width, height int) string {
	grid := make([][]string, height)
	for y := range grid {
		grid[y] = make([]string, width)
	}
	for i := range a.debris {
		d := &a.debris[i]
		px, py := int(math.Round(d.x)), int(math.Round(d.y))
		if px >= 0 && px < width && py >= 0 && py < height {
			grid[py][px] = d.cell
		}
	}
	var sb strings.Builder
	for y := 0; y < height; y++ {
		if y > 0 {
			sb.WriteByte('\n')
		}
		for x := 0; x < width; x++ {
			if grid[y][x] == "" {
				sb.WriteByte(' ')
			} else {
				sb.WriteString(grid[y][x])
			}
		}
	}
	return sb.String()
}
