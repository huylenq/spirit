package ui

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
)

// DinoTickMsg drives the dino game animation. Routed by the app's Update.
type DinoTickMsg struct{}

// DinoTickInterval is one game frame.
const DinoTickInterval = 70 * time.Millisecond

// Dino game world is a 5-row strip. Rows top→bottom:
//
//	0  header (score + hint)
//	1  jump apex
//	2  jump mid
//	3  ground row — dino + cacti live here
//	4  ground line + footer hint
const (
	dinoRowHeader = 0
	dinoRowApex   = 1
	dinoRowMid    = 2
	dinoRowGround = 3
	dinoRowFloor  = 4

	dinoColX = 6 // horizontal column where the dino lives

	// Jump physics — harmonica.Projectile in row-units (TerminalGravity
	// convention: y grows downward, so ground sits at the largest y). With
	// these numbers the apex peaks at ~row 1 and the full arc takes ~13 ticks.
	dinoGravity = 26.0 // rows / s² downward
	dinoJumpV0  = 10.0 // rows / s upward impulse on jump

	dinoMinSpawnGap = 12 // min ticks between spawns; jump arc ≈ 11 ticks, so this leaves ~1 tick of run time — packed but landable
	dinoMaxSpawnGap = 18

	dinoSpeedupEvery = 200 // every N score points, shave a tick off spawn cadence
)

// Scroll uses fractional cells-per-tick (accumulator) so we can go slower
// than 1 cell/tick on wide terminals. Tunables in floats:
const (
	dinoBaseScrollSpeed    = 0.3 // cells per tick at base width — terminal animation isn't smooth at high speeds, so we go slow
	dinoTargetTransitTicks = 400 // target ticks for an obstacle to cross the play area

	// When the road is empty (game start / restart), the closest obstacle sits
	// this many ticks of scroll-distance away from the dino. Gives the player
	// ~2 seconds to react before the first jump is required.
	dinoFirstSpawnWarmupTicks = 28
)

var (
	dinoGroundY = float64(dinoRowGround) // resting y position of the dino
)

type dinoObstacle struct {
	x    int  // column in world space (decreases over time)
	tall bool // tall cactus = 2 cells, short = 1
}

// DinoGame holds the dino-jump game state for the empty work-queue strip.
type DinoGame struct {
	// Continuous vertical physics. yPos is the dino's row position
	// (row units, increases downward). When grounded, yPos == dinoGroundY
	// and proj is nil. While airborne, proj integrates gravity each tick.
	yPos     float64
	yVel     float64
	proj     *harmonica.Projectile
	grounded bool

	obstacles    []dinoObstacle
	tickCount    int
	nextSpawn    int     // tick at which to spawn next obstacle
	scrollAccum  float64 // pending sub-cell scroll
	totalScroll  int     // total cells scrolled (for floor pattern phase)
	score        int
	hi           int
	prepopulated bool // road has been seeded with obstacles for cold start
	gameOver  bool
	width     int  // world width set by SetWidth (defaults to 80)
	rng       *rand.Rand

	// OnNewHi is called whenever the high score is beaten. Callers can use
	// this to persist the value; it runs on the game-tick goroutine.
	OnNewHi func(int)
}

// SetHi seeds the all-time high score (e.g. loaded from persistent storage).
func (g *DinoGame) SetHi(hi int) { g.hi = hi }

// SetWidth informs the game how wide the world is. Obstacles spawn just past
// the right edge so they scroll in from off-screen at any terminal width.
// On the first call after creation/reset, also seeds the road with a uniform
// stream of obstacles so the player sees a populated track instantly — no
// pop-in animation and no cluster gap before edge-spawned obstacles catch up.
func (g *DinoGame) SetWidth(w int) {
	if w < 1 {
		w = 1
	}
	g.width = w
	if !g.prepopulated && w > dinoColX {
		g.populateRoad()
		g.prepopulated = true
	}
}

// populateRoad seeds the road with obstacles spaced as if they had been
// scrolling continuously. The closest one sits ~dinoFirstSpawnWarmupTicks
// of scroll-distance from the dino (≈2s reaction time on first jump).
func (g *DinoGame) populateRoad() {
	speed := g.scrollSpeed()
	if speed <= 0 || g.width <= dinoColX {
		return
	}
	distance := float64(dinoFirstSpawnWarmupTicks) * speed
	edge := float64(g.width + 2)
	for distance <= edge {
		tall := g.rng.Intn(3) == 0
		g.obstacles = append(g.obstacles, dinoObstacle{
			x:    dinoColX + int(distance),
			tall: tall,
		})
		gap := dinoMinSpawnGap + g.rng.Intn(dinoMaxSpawnGap-dinoMinSpawnGap)
		distance += float64(gap) * speed
	}
	// Schedule the next spawn so we don't immediately add another at the edge.
	gap := dinoMinSpawnGap + g.rng.Intn(dinoMaxSpawnGap-dinoMinSpawnGap)
	g.nextSpawn = g.tickCount + gap
}

// scrollSpeed returns the per-tick obstacle scroll in cells (fractional).
// Wide terminals scale up so transit time stays roughly constant
// (≈ dinoTargetTransitTicks). Floored at dinoBaseScrollSpeed so narrow
// terminals don't grind to a halt.
func (g *DinoGame) scrollSpeed() float64 {
	travel := float64(g.width - dinoColX)
	if travel <= 0 {
		return dinoBaseScrollSpeed
	}
	s := travel / float64(dinoTargetTransitTicks)
	if s < dinoBaseScrollSpeed {
		s = dinoBaseScrollSpeed
	}
	return s
}

// NewDinoGame returns a fresh, live game. SetWidth populates the road with a
// warmup buffer (~2s) before the first obstacle reaches the dino.
func NewDinoGame() *DinoGame {
	return &DinoGame{
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		nextSpawn: 0,
		grounded:  true,
		yPos:      dinoGroundY,
	}
}

// Active reports whether the game wants tick events. Always true so that
// while the empty strip is visible, the world feels alive (cacti scroll
// even before the player jumps).
func (g *DinoGame) Active() bool { return true }

// HandleKey processes a key while the game is showing. Returns true if the
// key was consumed (caller should not propagate). Consumes jump (space/up/k/w)
// and restart (r).
func (g *DinoGame) HandleKey(s string) bool {
	switch s {
	case " ", "space", "up", "k", "w":
		if g.gameOver {
			g.reset()
			return true
		}
		if g.grounded {
			g.startJump()
		}
		return true
	case "r", "R":
		g.reset()
		return true
	}
	return false
}

// startJump kicks the dino into the air with an upward impulse. Allocates a
// fresh harmonica projectile so each jump integrates from a clean state.
func (g *DinoGame) startJump() {
	dt := DinoTickInterval.Seconds()
	g.proj = harmonica.NewProjectile(
		dt,
		harmonica.Point{X: 0, Y: dinoGroundY, Z: 0},
		harmonica.Vector{X: 0, Y: -dinoJumpV0, Z: 0}, // negative Y = upward in TerminalGravity convention
		harmonica.Vector{X: 0, Y: dinoGravity, Z: 0},
	)
	g.yPos = dinoGroundY
	g.yVel = -dinoJumpV0
	g.grounded = false
}

func (g *DinoGame) reset() {
	g.obstacles = nil
	g.tickCount = 0
	g.nextSpawn = 0
	g.scrollAccum = 0
	g.totalScroll = 0
	g.score = 0
	g.gameOver = false
	g.grounded = true
	g.yPos = dinoGroundY
	g.yVel = 0
	g.proj = nil
	g.prepopulated = false // next SetWidth will re-seed the road
}

// Tick advances one game frame. No-op if game-over.
func (g *DinoGame) Tick() tea.Cmd {
	if g.gameOver {
		return nil
	}
	g.tickCount++

	// Advance jump physics via harmonica.Projectile.
	if !g.grounded && g.proj != nil {
		pos := g.proj.Update()
		g.yPos = pos.Y
		g.yVel = g.proj.Velocity().Y
		// Land when falling back through the ground line.
		if g.yPos >= dinoGroundY && g.yVel >= 0 {
			g.yPos = dinoGroundY
			g.yVel = 0
			g.grounded = true
			g.proj = nil
		}
	}

	// Accumulate sub-cell scroll, then advance integer cells. Sub-stepping
	// the integer moves keeps collision robust even at high scroll speeds.
	g.scrollAccum += g.scrollSpeed()
	moved := int(g.scrollAccum)
	g.scrollAccum -= float64(moved)
	g.totalScroll += moved
	for step := 0; step < moved; step++ {
		for i := range g.obstacles {
			g.obstacles[i].x--
		}
		if g.collide() {
			g.gameOver = true
			return nil
		}
	}
	// Drop obstacles that have scrolled fully off-screen.
	surviving := g.obstacles[:0]
	for _, o := range g.obstacles {
		if o.x >= -2 {
			surviving = append(surviving, o)
		}
	}
	g.obstacles = surviving

	// Spawn cadence — at the actual right edge so obstacles enter from
	// off-screen. Cold-start density is handled by populateRoad in SetWidth,
	// not here.
	if g.tickCount >= g.nextSpawn {
		tall := g.rng.Intn(3) == 0
		spawnX := g.width + 2
		if spawnX < 1 {
			spawnX = 80
		}
		g.obstacles = append(g.obstacles, dinoObstacle{x: spawnX, tall: tall})
		gap := dinoMinSpawnGap + g.rng.Intn(dinoMaxSpawnGap-dinoMinSpawnGap)
		gap -= g.score / dinoSpeedupEvery
		if gap < dinoMinSpawnGap-4 {
			gap = dinoMinSpawnGap - 4
		}
		g.nextSpawn = g.tickCount + gap
	}

	g.score++
	if g.score > g.hi {
		g.hi = g.score
		if g.OnNewHi != nil {
			g.OnNewHi(g.hi)
		}
	}

	return tea.Tick(DinoTickInterval, func(time.Time) tea.Msg { return DinoTickMsg{} })
}

// dinoAirRow rounds the continuous y position to a screen row in
// [dinoRowApex, dinoRowGround].
func (g *DinoGame) dinoAirRow() int {
	r := int(g.yPos + 0.5)
	if r < dinoRowApex {
		r = dinoRowApex
	}
	if r > dinoRowGround {
		r = dinoRowGround
	}
	return r
}

// dinoGlyph returns the dino's character pair for the current frame.
// Two-cell wide so it has a body + head, swaps feet on running frames.
func (g *DinoGame) dinoGlyph() string {
	if g.gameOver {
		return "x_"
	}
	if !g.grounded {
		return "d>"
	}
	if g.tickCount%2 == 0 {
		return "d>"
	}
	return "D>"
}

// View renders the game into exactly 5 lines, areaWidth columns wide.
// Game world spans the full areaWidth so obstacles enter from the real
// right edge (no mid-screen pop-in).
func (g *DinoGame) View(areaWidth int) string {
	if areaWidth < 20 {
		// Too narrow to play; degrade to the static empty banner.
		empty := EmptyStyle.Width(areaWidth).Render("No sessions waiting")
		lines := strings.Split(empty, "\n")
		for len(lines) < WorkQueueHeight {
			lines = append(lines, strings.Repeat(" ", areaWidth))
		}
		return strings.Join(lines[:WorkQueueHeight], "\n")
	}

	// Build the 5 row buffers (rune slices for easy in-place writes).
	rows := make([][]rune, WorkQueueHeight)
	for i := range rows {
		rows[i] = []rune(strings.Repeat(" ", areaWidth))
	}

	// Floor line (row 4): subtle ground texture, broken every 4 cells.
	// Shift = totalScroll so the ground always advances exactly in lockstep
	// with obstacles — including the fractional accumulator.
	floor := rows[dinoRowFloor]
	shift := g.totalScroll
	for i := 0; i < areaWidth; i++ {
		if (i+shift)%4 == 0 {
			floor[i] = '.'
		} else {
			floor[i] = '_'
		}
	}

	// Obstacles — render only those falling within the visible area.
	for _, o := range g.obstacles {
		col := o.x
		if col < 0 || col >= areaWidth {
			continue
		}
		rows[dinoRowGround][col] = '#'
		if o.tall {
			rows[dinoRowMid][col] = '#'
		}
	}

	// Dino: 2-cell glyph at column dinoColX on its current air-row.
	dinoRow := g.dinoAirRow()
	glyph := []rune(g.dinoGlyph())
	for i, r := range glyph {
		col := dinoColX + i
		if col >= 0 && col < areaWidth {
			rows[dinoRow][col] = r
		}
	}

	// Header (row 0): score on left, hint on right.
	header := fmt.Sprintf("SCORE %05d   HI %05d", g.score, g.hi)
	hint := "space=jump  r=restart"
	if g.gameOver {
		hint = "GAME OVER — SPACE / R to restart"
	}
	writeLeft(rows[dinoRowHeader], header, areaWidth)
	writeRight(rows[dinoRowHeader], hint, areaWidth)

	// Render rows with styling.
	out := make([]string, WorkQueueHeight)
	muted := lipgloss.NewStyle().Foreground(ColorMuted)
	accent := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)

	for i, row := range rows {
		s := string(row)
		switch i {
		case dinoRowHeader:
			out[i] = muted.Render(s)
		case dinoRowFloor:
			out[i] = muted.Render(s)
		case dinoRowGround:
			// Highlight dino in accent color so it pops.
			out[i] = renderDinoRow(s, dinoColX, len(glyph), accent, muted)
		default:
			// Apex / mid rows: dino during jump should also pop.
			if i == dinoRow {
				out[i] = renderDinoRow(s, dinoColX, len(glyph), accent, muted)
			} else {
				out[i] = muted.Render(s)
			}
		}
	}

	return strings.Join(out, "\n")
}

// writeLeft writes s into row starting at col 0 (truncates if too long).
func writeLeft(row []rune, s string, width int) {
	rs := []rune(s)
	for i, r := range rs {
		if i >= width {
			return
		}
		row[i] = r
	}
}

// writeRight writes s right-aligned into row.
func writeRight(row []rune, s string, width int) {
	rs := []rune(s)
	start := width - len(rs)
	if start < 0 {
		rs = rs[-start:]
		start = 0
	}
	for i, r := range rs {
		col := start + i
		if col >= width {
			return
		}
		row[col] = r
	}
}

// renderDinoRow splits row into (before, dino, after) and styles the dino slice
// with `accent` and the rest with `muted`. Plain string concatenation —
// the row is plain runes so byte-indexing matches.
func renderDinoRow(s string, col, glyphLen int, accent, muted lipgloss.Style) string {
	rs := []rune(s)
	if col < 0 || col >= len(rs) {
		return muted.Render(s)
	}
	end := col + glyphLen
	if end > len(rs) {
		end = len(rs)
	}
	before := string(rs[:col])
	dino := string(rs[col:end])
	after := string(rs[end:])
	return muted.Render(before) + accent.Render(dino) + muted.Render(after)
}

// Collide checks whether the dino currently overlaps an obstacle.
// Called from Tick to decide game-over.
func (g *DinoGame) collide() bool {
	if g.gameOver {
		return false
	}
	dinoRow := g.dinoAirRow()
	for _, o := range g.obstacles {
		// Dino occupies cols [dinoColX, dinoColX+1].
		if o.x < dinoColX-1 || o.x > dinoColX+1 {
			continue
		}
		// Ground obstacle hits when dino is on ground row.
		if dinoRow == dinoRowGround {
			return true
		}
		// Tall obstacle also hits the mid row.
		if o.tall && dinoRow == dinoRowMid {
			return true
		}
	}
	return false
}
