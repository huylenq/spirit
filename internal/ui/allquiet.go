package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/harmonica"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	allQuietFPS      = 12
	allQuietInterval = time.Second / allQuietFPS
	pendStringH      = 3 // rows of string between bar and bob
	numPendulums     = 3
)

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
}

// Active reports whether the animation is running.
func (a *AllQuietAnim) Active() bool { return a.active }

// Init starts the animation and returns the first tick command.
func (a *AllQuietAnim) Init() tea.Cmd {
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
		{3.0, 2.5, 0.18, 1.5, "◆", ColorLater},     // purple diamond: mid, bouncy
	}
	for i, c := range cfgs {
		a.pends[i] = quietPendulum{
			spring:  harmonica.NewSpring(td, c.angVel, c.damp),
			x:       c.init,
			xVel:    0,
			targetX: c.amp,
			bob:     c.bob,
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
		{0.05, 0.10, 1.5, 2.0, 0.22, "·", true},
		{0.08, 0.62, 1.0, 2.8, 0.26, "✧", false},
		{0.12, 0.87, 1.5, 1.8, 0.20, "·", true},
		{0.15, 0.35, 1.0, 3.0, 0.30, "∘", true},
		{0.72, 0.08, 1.5, 2.2, 0.22, "·", true},
		{0.76, 0.55, 1.0, 3.2, 0.28, "∘", false},
		{0.80, 0.90, 1.5, 1.8, 0.18, "✧", true},
		{0.88, 0.25, 1.0, 2.5, 0.24, "·", false},
	}
	a.particles = make([]quietParticle, len(pcs))
	for i, pc := range pcs {
		st := brSt
		if pc.dim {
			st = dimSt
		}
		a.particles[i] = quietParticle{
			rowFrac: pc.rF, colFrac: pc.cF,
			spring:  harmonica.NewSpring(td, pc.aV, pc.d),
			x:       0, xVel: 0,
			targetX: pc.amp,
			char:    pc.ch, style: st,
		}
	}
	return tickAllQuiet()
}

// Stop halts the animation.
func (a *AllQuietAnim) Stop() { a.active = false }

// Tick advances all springs by one frame and returns the next tick command.
func (a *AllQuietAnim) Tick() tea.Cmd {
	if !a.active {
		return nil
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

// ---- rendering ----

// placed is a styled text fragment at a specific column.
type placed struct {
	col  int
	text string
	w    int // visual width
}

// Render draws the full animated scene (mobile + particles + dashboard text).
func (a *AllQuietAnim) Render(width, height int, counts AllQuietCounts) string {
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

	// --- Mobile ---
	barW := min(36, width*45/100)
	if barW < 14 {
		barW = 14
	}
	barLeft := (width - barW) / 2
	barRow := height / 5
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
	barStyle := lipgloss.NewStyle().Foreground(ColorBorder)
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
	put(barRow, barLeft, barStyle.Render(bar.String()), barW)

	// Strings and bobs
	strStyle := lipgloss.NewStyle().Foreground(ColorBorder)
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
			put(barRow+r, col, strStyle.Render(ch), 1)
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
	dash := renderQuietDashboard(counts)
	dashRow := barRow + pendStringH + 4
	if dashRow > height-8 {
		dashRow = max(barRow+pendStringH+3, height-8)
	}
	dashLines := strings.Split(dash, "\n")
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

// renderQuietDashboard builds the section counts + keybinding hints.
func renderQuietDashboard(counts AllQuietCounts) string {
	var lines []string
	if counts.Clauding > 0 {
		lines = append(lines, GroupHeaderWorkingStyle.Render(
			fmt.Sprintf("%d sessions running", counts.Clauding)))
	} else {
		lines = append(lines, GroupHeaderDoneStyle.Render("All clear"))
	}
	lines = append(lines, "")

	sections := []struct {
		icon, label, key string
		count            int
	}{
		{IconWand, "clauding", "alt+c", counts.Clauding},
		{IconBookmark, "bookmarked", "alt+w", counts.Later},
		{IconBacklog, "in backlog", "alt+b", counts.Backlog},
	}
	for _, s := range sections {
		if s.count > 0 {
			lines = append(lines, ItemDetailStyle.Render(
				fmt.Sprintf("  %s  %d %s", s.icon, s.count, s.label)))
		}
	}
	lines = append(lines, "")
	for _, s := range sections {
		if s.count > 0 {
			lines = append(lines, ItemDetailStyle.Render(
				fmt.Sprintf("  %s  expand %s", s.key, s.label)))
		}
	}
	return strings.Join(lines, "\n")
}

// renderStaticDashboard is the fallback when the pane is too small for animation.
func renderStaticDashboard(width, height int, counts AllQuietCounts) string {
	return EmptyStyle.Width(width).Height(height).Render(renderQuietDashboard(counts))
}
