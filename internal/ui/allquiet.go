package ui

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// claudingDetailWidth caps the per-session recap/assistant subtitle so a long
// recap doesn't blow out the centered dashboard width.
const claudingDetailWidth = 72

const (
	allQuietFPS      = 12
	allQuietInterval = time.Second / allQuietFPS
	pendStringH      = 3 // rows of string between bar and bob
	numPendulums     = 3
)

// Intro explosion: when quiet mode is entered, the dashboard text shatters
// outward — letters burst from screen center on a light gravity arc — before
// the calm pendulum mobile takes over.
const (
	explodeFrames   = 16   // explosion duration in frames before the mobile reveals
	explodeGravity  = 0.12 // per-frame downward pull on debris
	explodeMinSpeed = 1.8  // minimum outward burst speed (cells/frame)
	explodeSpeedVar = 2.4  // additional speed spread on top of the minimum
	explodeLift     = 0.8  // initial upward kick so debris arcs before falling
)

type quietPhase int

const (
	phaseMobile quietPhase = iota // calm pendulum scene (steady state)
	phaseExplode                  // intro: dashboard text bursting apart
)

// debrisParticle is one styled rune flung outward during the intro explosion.
type debrisParticle struct {
	x, y   float64
	vx, vy float64
	cell   string // pre-rendered "ansiPrefix + rune + reset"
	ttl    int    // frames remaining before the rune vanishes
}

// AllQuietTickMsg advances the all-quiet animation by one frame.
type AllQuietTickMsg struct{}

func tickAllQuiet() tea.Cmd {
	return tea.Tick(allQuietInterval, func(time.Time) tea.Msg {
		return AllQuietTickMsg{}
	})
}

// ---- internal types ----

type quietPendulum struct {
	spring   harmonica.Spring
	x        float64 // current x offset from attachment
	xVel     float64
	targetX  float64
	bob      string
	bobStyle lipgloss.Style
}

type quietParticle struct {
	rowFrac float64 // position as fraction of height
	colFrac float64 // position as fraction of width
	spring  harmonica.Spring
	x       float64 // current x offset from base column
	xVel    float64
	targetX float64
	char    string
	style   lipgloss.Style
}

// AllQuietAnim manages the animated mobile + starfield scene.
type AllQuietAnim struct {
	pends     [numPendulums]quietPendulum
	particles []quietParticle
	active    bool
	frame     int // monotonically increasing tick counter (drives the shimmer)

	// Intro explosion state (phaseExplode → phaseMobile).
	phase   quietPhase
	debris  []debrisParticle
	introFr int
}

// Active reports whether the animation is running.
func (a *AllQuietAnim) Active() bool { return a.active }

// Init starts the animation and returns the first tick command. When intro is
// true, counts and the w×h canvas size seed the entrance explosion — the quiet
// dashboard is laid out, shattered into debris, and flung outward before the
// mobile scene settles in. When false (e.g. the TUI simply opened into a quiet
// state), the calm mobile renders straight away with no explosion.
func (a *AllQuietAnim) Init(counts AllQuietCounts, w, h int, intro bool) tea.Cmd {
	if a.active {
		return nil
	}
	a.active = true
	td := harmonica.FPS(allQuietFPS)

	// Pendulum configs: amplitude, angularVelocity, damping, bob char, color, initial offset.
	// Each pendulum uses different spring parameters so they swing out of phase,
	// creating organic, non-synchronized motion — the signature of real mobiles.
	type pcfg struct {
		amp, angVel, damp, init float64
		bob                     string
		color                   lipgloss.TerminalColor
	}
	cfgs := [numPendulums]pcfg{
		{3.0, 2.0, 0.15, -2.0, "★", ColorWorking}, // amber star: slow, very bouncy
		{2.0, 3.0, 0.25, 0.0, "☽", ColorDone},     // blue moon: medium speed
		{3.0, 2.5, 0.18, 1.5, "◆", ColorLater},    // purple diamond: mid, bouncy
	}
	for i, c := range cfgs {
		a.pends[i] = quietPendulum{
			spring:   harmonica.NewSpring(td, c.angVel, c.damp),
			x:        c.init,
			xVel:     0,
			targetX:  c.amp,
			bob:      c.bob,
			bobStyle: lipgloss.NewStyle().Foreground(c.color),
		}
	}

	// Background particles — dim stars scattered in top and bottom regions.
	dimSt := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#d1d5db", Dark: "#374151"})
	brSt := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#6b7280"})
	type qcfg struct {
		rF, cF, amp, aV, d float64
		ch                 string
		dim                bool
	}
	pcs := []qcfg{
		// Top band
		{0.04, 0.08, 1.5, 2.0, 0.22, "·", true},
		{0.05, 0.30, 1.0, 3.1, 0.28, "∘", true},
		{0.06, 0.52, 1.5, 2.4, 0.24, "·", true},
		{0.07, 0.72, 1.0, 2.8, 0.26, "✧", false},
		{0.08, 0.93, 1.5, 1.9, 0.20, "·", true},
		{0.12, 0.18, 1.0, 3.0, 0.30, "∘", true},
		{0.13, 0.45, 1.5, 2.2, 0.21, "·", true},
		{0.14, 0.66, 1.0, 2.6, 0.27, "·", false},
		{0.15, 0.87, 1.5, 1.8, 0.20, "✧", true},
		{0.18, 0.05, 1.5, 2.3, 0.23, "·", true},
		{0.19, 0.38, 1.0, 2.9, 0.29, "∘", true},
		{0.20, 0.78, 1.5, 2.1, 0.22, "·", true},
		// Mid band (around the mobile edges)
		{0.40, 0.06, 1.5, 2.2, 0.22, "·", true},
		{0.44, 0.94, 1.0, 2.7, 0.26, "·", true},
		{0.50, 0.04, 1.5, 1.9, 0.20, "∘", true},
		{0.54, 0.96, 1.5, 2.4, 0.23, "·", true},
		// Bottom band
		{0.70, 0.10, 1.5, 2.2, 0.22, "·", true},
		{0.72, 0.33, 1.0, 2.9, 0.28, "∘", true},
		{0.74, 0.55, 1.0, 3.2, 0.28, "∘", false},
		{0.76, 0.78, 1.5, 2.0, 0.21, "·", true},
		{0.78, 0.92, 1.5, 1.8, 0.18, "✧", true},
		{0.82, 0.20, 1.0, 2.5, 0.24, "·", false},
		{0.84, 0.43, 1.5, 2.3, 0.22, "·", true},
		{0.86, 0.64, 1.0, 2.7, 0.26, "·", true},
		{0.88, 0.85, 1.5, 1.9, 0.20, "·", true},
		{0.92, 0.14, 1.5, 2.1, 0.23, "∘", true},
		{0.94, 0.50, 1.0, 2.8, 0.27, "·", false},
		{0.95, 0.73, 1.5, 2.0, 0.21, "·", true},
	}
	a.particles = make([]quietParticle, len(pcs))
	for i, pc := range pcs {
		st := brSt
		if pc.dim {
			st = dimSt
		}
		a.particles[i] = quietParticle{
			rowFrac: pc.rF, colFrac: pc.cF,
			spring: harmonica.NewSpring(td, pc.aV, pc.d),
			x:      0, xVel: 0,
			targetX: pc.amp,
			char:    pc.ch, style: st,
		}
	}

	// Seed the intro explosion. If the canvas is too small to lay out the
	// dashboard, skip straight to the calm scene.
	a.phase = phaseMobile
	a.debris = nil
	a.introFr = 0
	if intro && w >= 24 && h >= 12 {
		a.initExplosion(counts, w, h)
	}
	return tickAllQuiet()
}

// initExplosion lays out the quiet dashboard centered in the canvas, decomposes
// it into styled runes, and gives each an outward velocity from screen center.
// Every invocation draws a fresh random seed plus a randomized "flavor" — overall
// speed, rotational swirl, and upward bias — so no two bursts look alike.
func (a *AllQuietAnim) initExplosion(counts AllQuietCounts, w, h int) {
	dash := renderQuietDashboard(counts, 0)
	src := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, dash)
	frags := decomposeStyled(src, w, h)
	if len(frags) == 0 {
		return
	}
	a.phase = phaseExplode

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Per-burst flavor: a global speed scale, a tangential swirl that bends the
	// debris into a rotation (sign = direction), and an upward-kick multiplier
	// that ranges from a slight downward slump to a strong fountain.
	speedMul := 0.8 + rng.Float64()*0.7
	swirl := (rng.Float64()*2 - 1) * 0.7
	liftMul := rng.Float64()*1.6 - 0.2

	cx, cy := float64(w)/2, float64(h)/2
	a.debris = make([]debrisParticle, len(frags))
	for i, f := range frags {
		dx, dy := float64(f.x)-cx, float64(f.y)-cy
		dist := math.Hypot(dx, dy)
		if dist < 0.001 {
			dist = 0.001
		}
		ux, uy := dx/dist, dy/dist
		// Tangent (perpendicular) for the swirl component.
		tx, ty := -uy, ux
		speed := (explodeMinSpeed + rng.Float64()*explodeSpeedVar) * speedMul
		a.debris[i] = debrisParticle{
			x: float64(f.x), y: float64(f.y),
			vx: (ux+tx*swirl)*speed + (rng.Float64()-0.5)*1.5,
			// Cells are ~2× taller than wide, so halve vertical speed to keep
			// the burst circular in screen space; add a randomized upward kick.
			vy:   (uy+ty*swirl)*speed*0.5 - explodeLift*liftMul - rng.Float64()*0.6,
			cell: f.styled,
			ttl:  explodeFrames + int(rng.Float64()*5),
		}
	}
}

// Stop halts the animation.
func (a *AllQuietAnim) Stop() { a.active = false }

// Tick advances all springs by one frame and returns the next tick command.
func (a *AllQuietAnim) Tick() tea.Cmd {
	if !a.active {
		return nil
	}
	a.frame++
	if a.phase == phaseExplode {
		a.tickExplosion()
		return tickAllQuiet()
	}
	for i := range a.pends {
		p := &a.pends[i]
		p.x, p.xVel = p.spring.Update(p.x, p.xVel, p.targetX)
		// Flip target when pendulum nears its destination with low velocity —
		// this creates perpetual oscillation instead of settling at the target.
		if math.Abs(p.x-p.targetX) < 0.4 && math.Abs(p.xVel) < 0.5 {
			p.targetX = -p.targetX
		}
	}
	for i := range a.particles {
		p := &a.particles[i]
		p.x, p.xVel = p.spring.Update(p.x, p.xVel, p.targetX)
		if math.Abs(p.x-p.targetX) < 0.3 && math.Abs(p.xVel) < 0.3 {
			p.targetX = -p.targetX
		}
	}
	return tickAllQuiet()
}

// tickExplosion advances debris by one frame and hands off to the mobile scene
// once the burst has run its course.
func (a *AllQuietAnim) tickExplosion() {
	a.introFr++
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
	if a.introFr >= explodeFrames {
		a.phase = phaseMobile
		a.debris = nil
	}
}

// ---- rendering ----

// placed is a styled text fragment at a specific column.
type placed struct {
	col  int
	text string
	w    int // visual width
}

// Render draws the full animated scene (mobile + particles + dashboard text).
func (a *AllQuietAnim) Render(width, height int, counts AllQuietCounts) string {
	if a.phase == phaseExplode {
		return a.renderExplosion(width, height)
	}
	if height < 12 || width < 24 {
		return renderStaticDashboard(width, height, counts)
	}

	rows := make(map[int][]placed)
	put := func(row, col int, text string, w int) {
		if row >= 0 && row < height && col >= 0 && col+w <= width {
			rows[row] = append(rows[row], placed{col, text, w})
		}
	}

	// --- Background particles ---
	for _, p := range a.particles {
		row := int(p.rowFrac * float64(height))
		col := int(p.colFrac*float64(width)) + int(math.Round(p.x))
		put(row, col, p.style.Render(p.char), 1)
	}

	// Dashboard is built first so the whole mobile+dashboard block can be
	// vertically centered as one unit.
	dash := renderQuietDashboard(counts, a.frame)
	dashLines := strings.Split(dash, "\n")
	dashH := len(dashLines)

	// --- Mobile ---
	barW := min(36, width*45/100)
	if barW < 14 {
		barW = 14
	}
	barLeft := (width - barW) / 2

	// Vertically center the composition: mobile (bar + strings + bob) spans
	// pendStringH+2 rows, then a 3-row gap, then the dashboard.
	blockH := (pendStringH + 2) + 3 + dashH
	barRow := (height - blockH) / 2
	if barRow < 2 {
		barRow = 2
	}

	// Attachment points (evenly spaced along bar)
	spacing := barW / (numPendulums + 1)
	var attachCols [numPendulums]int
	for i := range attachCols {
		attachCols[i] = barLeft + spacing*(i+1)
	}

	// Clamp swing amplitude so bobs never overlap
	maxSwing := spacing/2 - 1
	if maxSwing < 1 {
		maxSwing = 1
	}

	// Build and place bar
	var bar strings.Builder
	for c := 0; c < barW; c++ {
		absC := barLeft + c
		isAttach := false
		for _, ac := range attachCols {
			if absC == ac {
				isAttach = true
				break
			}
		}
		switch {
		case c == 0:
			bar.WriteRune('╶')
		case c == barW-1:
			bar.WriteRune('╴')
		case isAttach:
			bar.WriteRune('┬')
		default:
			bar.WriteRune('─')
		}
	}
	put(barRow, barLeft, BorderCharStyle.Render(bar.String()), barW)

	// Strings and bobs
	for pi := range a.pends {
		pend := &a.pends[pi]
		bobOff := int(math.Round(pend.x))
		// Clamp to avoid overlapping adjacent bobs
		if bobOff > maxSwing {
			bobOff = maxSwing
		} else if bobOff < -maxSwing {
			bobOff = -maxSwing
		}

		// Draw string segments with interpolated columns
		for r := 1; r <= pendStringH; r++ {
			frac := float64(r) / float64(pendStringH+1)
			col := attachCols[pi] + int(math.Round(frac*float64(bobOff)))

			prevFrac := float64(r-1) / float64(pendStringH+1)
			prevCol := attachCols[pi] + int(math.Round(prevFrac*float64(bobOff)))

			var ch string
			switch diff := col - prevCol; {
			case diff > 0:
				ch = "╲"
			case diff < 0:
				ch = "╱"
			default:
				ch = "│"
			}
			put(barRow+r, col, BorderCharStyle.Render(ch), 1)
		}

		// Bob
		bobCol := attachCols[pi] + bobOff
		put(barRow+pendStringH+1, bobCol, pend.bobStyle.Render(pend.bob), 1)
	}

	// --- Build lines ---
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		pts, ok := rows[row]
		if !ok {
			continue
		}
		sort.Slice(pts, func(i, j int) bool { return pts[i].col < pts[j].col })
		var sb strings.Builder
		pos := 0
		for _, pt := range pts {
			if pt.col > pos {
				sb.WriteString(strings.Repeat(" ", pt.col-pos))
			}
			sb.WriteString(pt.text)
			pos = pt.col + pt.w
		}
		lines[row] = sb.String()
	}
	bg := strings.Join(lines, "\n")

	// --- Dashboard overlay (centered below mobile) ---
	dashRow := barRow + pendStringH + 4
	if dashRow+dashH > height {
		dashRow = max(barRow+pendStringH+3, height-dashH)
	}
	dashMaxW := 0
	for _, l := range dashLines {
		if w := lipgloss.Width(l); w > dashMaxW {
			dashMaxW = w
		}
	}
	dashCol := (width - dashMaxW) / 2
	if dashCol < 0 {
		dashCol = 0
	}
	return OverlayAt(bg, dash, dashRow, dashCol)
}

// renderQuietDashboard builds the running-session list + section counts.
// phase drives the shimmer animation on the Clauding session names.
func renderQuietDashboard(counts AllQuietCounts, phase int) string {
	var lines []string
	if n := len(counts.ClaudingSessions); n > 0 {
		lines = append(lines, GroupHeaderWorkingStyle.Render(
			fmt.Sprintf("%d sessions running", n)))
	} else {
		lines = append(lines, GroupHeaderDoneStyle.Render("All clear"))
	}
	lines = append(lines, "")

	// Clauding sessions are listed individually: a static colored avatar glyph
	// followed by a shimmering display name (shimmer offset staggered per row so
	// they don't pulse in lockstep). Below each: the recap rendered in full
	// (★-marked, word-wrapped, when the session has an away_summary) and the
	// latest assistant message (chevron-marked, single line) — both dim and
	// indented under the name.
	for i, e := range counts.ClaudingSessions {
		lines = append(lines, "  "+e.Glyph+"  "+shimmer(e.Name, phase+i*3))
		for j, wl := range e.RecapLines {
			if j == 0 {
				lines = append(lines, "     "+recapMarkerStyle.Render("★ ")+ItemDetailStyle.Render(wl))
			} else {
				lines = append(lines, "       "+ItemDetailStyle.Render(wl)) // align under recap text past "★ "
			}
		}
		if e.Assistant != "" {
			lines = append(lines, "     "+ItemDetailStyle.Render(IconText+" "+e.Assistant))
		}
	}
	// Blank line separates the running sessions from the section counts below.
	if len(counts.ClaudingSessions) > 0 {
		lines = append(lines, "")
	}

	sections := []struct {
		icon, label string
		count       int
	}{
		{IconLater, "marked later", counts.Later},
	}
	for _, s := range sections {
		if s.count > 0 {
			lines = append(lines, ItemDetailStyle.Render(
				fmt.Sprintf("  %s  %d %s", s.icon, s.count, s.label)))
		}
	}
	return strings.Join(lines, "\n")
}

// shimmer renders text with a bright band sweeping left→right across it, on a
// muted amber base — the visual cue that a listed session is actively working.
func shimmer(text string, phase int) string {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return text
	}
	// The band center sweeps across the text plus a trailing gap so there's a
	// brief dark pause between passes.
	span := n + 10
	center := ((phase % span) + span) % span
	var b strings.Builder
	for i, r := range runes {
		d := i - center
		if d < 0 {
			d = -d
		}
		var st lipgloss.Style
		switch {
		case d == 0:
			st = shimmerHotStyle
		case d <= 1:
			st = shimmerWarmStyle
		case d <= 3:
			st = shimmerMidStyle
		default:
			st = shimmerBaseStyle
		}
		b.WriteString(st.Render(string(r)))
	}
	return b.String()
}

// renderStaticDashboard is the fallback when the pane is too small for animation.
func renderStaticDashboard(width, height int, counts AllQuietCounts) string {
	return EmptyStyle.Width(width).Height(height).Render(renderQuietDashboard(counts, 0))
}

// renderExplosion stamps live debris onto a cell grid — overlapping particles
// resolve cleanly (last writer wins) and dead runes leave empty space.
func (a *AllQuietAnim) renderExplosion(width, height int) string {
	grid := make([][]string, height)
	for y := range grid {
		grid[y] = make([]string, width)
	}
	for i := range a.debris {
		d := &a.debris[i]
		if d.ttl <= 0 {
			continue
		}
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
		row := grid[y]
		last := width - 1
		for last >= 0 && row[last] == "" {
			last--
		}
		for x := 0; x <= last; x++ {
			if row[x] == "" {
				sb.WriteByte(' ')
			} else {
				sb.WriteString(row[x])
			}
		}
	}
	return sb.String()
}

// styledFrag is a positioned rune carrying its active ANSI style, ready to print.
type styledFrag struct {
	x, y   int
	styled string // ansiPrefix + rune + reset
}

// decomposeStyled walks rendered TUI output, tracking the active ANSI SGR state,
// and emits one styledFrag per visible non-space rune with its color preserved.
func decomposeStyled(rendered string, width, height int) []styledFrag {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	var frags []styledFrag
	for y, line := range lines {
		col := 0
		var style string
		b := []byte(line)
		i := 0
		for i < len(b) {
			// ANSI escape sequence: \x1b[ ... <terminator letter>
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[' {
				j := i + 2
				for j < len(b) && !isSGRTerminator(b[j]) {
					j++
				}
				if j < len(b) {
					if b[j] == 'm' {
						if seq := string(b[i : j+1]); seq == "\x1b[0m" || seq == "\x1b[m" {
							style = "" // reset clears all
						} else {
							style += seq
						}
					}
					i = j + 1
					continue
				}
				i++ // malformed — skip the ESC byte
				continue
			}
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size <= 1 {
				i++
				continue
			}
			if col >= width {
				break
			}
			if r != ' ' && r != '\t' {
				frags = append(frags, styledFrag{x: col, y: y, styled: style + string(r) + "\x1b[0m"})
			}
			col += runewidth.RuneWidth(r)
			i += size
		}
	}
	return frags
}
