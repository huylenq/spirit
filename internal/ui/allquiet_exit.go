package ui

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// exitFrames is how long the calm quiet scene shatters outward before the
// returning normal view is left uncovered.
const exitFrames = 14

// QuietExitAnim is the mirror of the intro explosion, played when a session
// reappears in YOUR TURN and the quiet mobile scene gives way to the normal
// sidebar+detail view. Unlike the intro it has no target and no implode: the
// calm scene simply bursts from center and falls away, composited on top of the
// live normal frame so the debris appears to lift off and reveal the work
// underneath. When the burst clears, what remains is exactly the normal frame.
type QuietExitAnim struct {
	active bool
	debris []debrisParticle
	fr     int
}

// Active reports whether the exit shatter is running.
func (a *QuietExitAnim) Active() bool { return a.active }

// Start seeds the shatter from a rendered source frame (the quiet scene as it
// looked) laid out on a w×h canvas, and returns the first tick command. The
// debris coordinate space is the same w×h, so callers overlay it onto a frame of
// matching dimensions.
func (a *QuietExitAnim) Start(src string, w, h int) tea.Cmd {
	frags := decomposeStyled(src, w, h)
	if len(frags) == 0 {
		a.active = false
		a.debris = nil
		return nil
	}
	a.active = true
	a.fr = 0

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Per-burst flavor, matching the intro so the two transitions feel related.
	speedMul := 0.8 + rng.Float64()*0.7
	swirl := (rng.Float64()*2 - 1) * 0.7

	cx, cy := float64(w)/2, float64(h)/2
	a.debris = make([]debrisParticle, len(frags))
	for i, f := range frags {
		dx, dy := float64(f.x)-cx, float64(f.y)-cy
		dist := math.Hypot(dx, dy)
		if dist < 0.001 {
			dist = 0.001
		}
		ux, uy := dx/dist, dy/dist
		tx, ty := -uy, ux // tangent for the swirl
		speed := (explodeMinSpeed + rng.Float64()*explodeSpeedVar) * speedMul
		a.debris[i] = debrisParticle{
			x: float64(f.x), y: float64(f.y),
			vx: (ux+tx*swirl)*speed + (rng.Float64()-0.5)*1.5,
			// Halve vertical speed (cells are ~2× taller than wide) and add an
			// upward kick so the debris arcs before gravity takes it.
			vy:   (uy+ty*swirl)*speed*0.5 - explodeLift - rng.Float64()*0.6,
			cell: f.styled,
			ttl:  exitFrames + int(rng.Float64()*4),
		}
	}
	return tickAllQuiet()
}

// Stop halts the shatter and releases its debris.
func (a *QuietExitAnim) Stop() {
	a.active = false
	a.debris = nil
}

// Tick advances the shatter by one frame and returns the next tick command, or
// nil once the burst has run its course.
func (a *QuietExitAnim) Tick() tea.Cmd {
	if !a.active {
		return nil
	}
	a.fr++
	for i := range a.debris {
		d := &a.debris[i]
		if d.ttl <= 0 {
			continue
		}
		d.vy += explodeGravity
		d.x += d.vx
		d.y += d.vy
		d.ttl--
	}
	if a.fr >= exitFrames {
		a.active = false
		a.debris = nil
		return nil
	}
	return tickAllQuiet()
}

// Overlay composites the live debris on top of a background frame (the returning
// normal content), so the shatter appears to lift off the view underneath.
// Debris cells replace only the background cell they land on; every other cell
// shows through, and debris beyond a line's content is dropped rather than
// padding the background out.
func (a *QuietExitAnim) Overlay(bg string) string {
	if !a.active || len(a.debris) == 0 {
		return bg
	}
	lines := strings.Split(bg, "\n")
	// Group debris by row so each affected line is rebuilt in a single pass over
	// its original (clean) text. Splicing cells one at a time via ansi.Truncate
	// re-injects the SGR state on every call, which compounds exponentially —
	// rebuilding once from the untouched line keeps it linear.
	byRow := make(map[int][]placedCell)
	for i := range a.debris {
		d := &a.debris[i]
		if d.ttl <= 0 {
			continue
		}
		px, py := int(math.Round(d.x)), int(math.Round(d.y))
		if py < 0 || py >= len(lines) {
			continue
		}
		byRow[py] = append(byRow[py], placedCell{col: px, cell: d.cell})
	}
	for row, cells := range byRow {
		lines[row] = compositeRow(lines[row], cells)
	}
	return strings.Join(lines, "\n")
}

// placedCell is a styled cell to stamp at a visual column during compositing.
type placedCell struct {
	col  int
	cell string
}

// compositeRow rebuilds a styled line with the given cells stamped over it,
// preserving the untouched styled runs on either side. It walks the ORIGINAL
// line once (via ansi.Cut for each gap), so redundant escape sequences never
// compound. Cells landing past the line's content, or overlapping an earlier
// cell, are dropped.
func compositeRow(line string, cells []placedCell) string {
	bWidth := ansi.StringWidth(line)
	sort.Slice(cells, func(i, j int) bool { return cells[i].col < cells[j].col })

	var sb strings.Builder
	pos := 0 // next original column not yet emitted
	for _, c := range cells {
		if c.col < pos || c.col >= bWidth {
			continue // overlaps a prior cell or lands past the content
		}
		w := ansi.StringWidth(c.cell)
		if w < 1 {
			w = 1
		}
		if c.col+w > bWidth {
			continue
		}
		sb.WriteString(ansi.Cut(line, pos, c.col)) // clean gap from the original
		sb.WriteString(c.cell)
		pos = c.col + w
	}
	if pos < bWidth {
		sb.WriteString(ansi.Cut(line, pos, bWidth))
	}
	return sb.String()
}
