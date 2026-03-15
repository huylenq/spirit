package destroyer

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

const (
	destroyerFPS = 20
	Interval     = time.Second / destroyerFPS
)

// TickMsg advances the destroyer animation by one frame.
type TickMsg struct{}

// Phase tracks the destroyer lifecycle.
type Phase int

const (
	PhaseDestroy Phase = iota // active destruction
	PhaseRebuild              // particles returning home
)

// Model holds all destroyer state.
type Model struct {
	Particles []Particle
	Width     int
	Height    int
	Tool      Tool
	Phase     Phase
	Score     int
	Frame     int
	Active    bool

	// Screen shake state
	ShakeX, ShakeY float64
	ShakeSpringX   harmonica.Spring
	ShakeSpringY   harmonica.Spring
	ShakeVelX      float64
	ShakeVelY      float64

	// Cursor position for tools that track it
	CursorX, CursorY int

	// Black hole continuous mode
	BlackHoleActive bool

	// Rebuild completion tracking
	homeCount int

	td float64 // time delta for springs
}

// New creates a destroyer model from a rendered TUI snapshot.
func New(rendered string, width, height int) Model {
	td := harmonica.FPS(destroyerFPS)
	frags := DecomposeStyled(rendered, width, height)

	particles := make([]Particle, len(frags))
	for i, f := range frags {
		particles[i] = NewParticle(f.X, f.Y, f.Rune, f.AnsiStyle, td)
	}

	return Model{
		Particles:    particles,
		Width:        width,
		Height:       height,
		Tool:         ToolHammer,
		Phase:        PhaseDestroy,
		Active:       true,
		ShakeSpringX: harmonica.NewSpring(td, 8.0, 0.7),
		ShakeSpringY: harmonica.NewSpring(td, 8.0, 0.7),
		CursorX:      width / 2,
		CursorY:      height / 2,
		td:           td,
	}
}

// Tick advances all particles and effects by one frame.
func (m *Model) Tick() {
	if !m.Active {
		return
	}
	m.Frame++
	floorY := m.Height - 1
	rightWall := m.Width - 1

	switch m.Phase {
	case PhaseDestroy:
		// Continuous black hole pull
		if m.BlackHoleActive && m.Tool == ToolBlackHole {
			absorbed := ApplyBlackHole(m.Particles, float64(m.CursorX), float64(m.CursorY), 20.0, 1.5)
			m.Score += absorbed * 5
		}

		// Continuous shake
		if m.Tool == ToolShake && m.Frame%3 == 0 {
			sx, sy := ApplyShake(m.Particles, 2.0, m.Frame)
			m.ShakeVelX += sx
			m.ShakeVelY += sy
		}

		for i := range m.Particles {
			m.Particles[i].Tick(floorY, rightWall)
		}

	case PhaseRebuild:
		m.homeCount = 0
		for i := range m.Particles {
			p := &m.Particles[i]
			// In rebuild, no gravity — spring pulls home
			p.X, p.VelX = p.SpringX.Update(p.X, p.VelX, p.TgtX)
			p.Y, p.VelY = p.SpringY.Update(p.Y, p.VelY, p.TgtY)
			if p.IsHome() {
				m.homeCount++
			}
		}
	}

	// Decay screen shake
	m.ShakeX, m.ShakeVelX = m.ShakeSpringX.Update(m.ShakeX, m.ShakeVelX, 0)
	m.ShakeY, m.ShakeVelY = m.ShakeSpringY.Update(m.ShakeY, m.ShakeVelY, 0)
}

// IsRebuilt reports whether all particles are back home.
func (m *Model) IsRebuilt() bool {
	return m.Phase == PhaseRebuild && m.homeCount >= len(m.Particles)-1
}

// StartRebuild initiates the return-to-origin animation.
func (m *Model) StartRebuild() {
	m.Phase = PhaseRebuild
	m.BlackHoleActive = false
	for i := range m.Particles {
		m.Particles[i].ResetToOrigin(m.td)
	}
}

// Click applies the current tool at (x, y).
// shake controls whether screen shake is applied (true on press, false on drag).
func (m *Model) Click(x, y int, shake bool) {
	if m.Phase != PhaseDestroy {
		return
	}
	m.CursorX = x
	m.CursorY = y
	cx, cy := float64(x), float64(y)

	switch m.Tool {
	case ToolHammer:
		hits := ApplyHammer(m.Particles, cx, cy, 4.0)
		m.Score += hits * 10
		if hits > 0 && shake {
			m.ShakeVelX += math.Sin(float64(m.Frame)) * 1.5
			m.ShakeVelY += 1.0
		}
	case ToolBomb:
		hits := ApplyBomb(m.Particles, cx, cy, 12.0)
		m.Score += hits * 5
		if shake {
			m.ShakeVelX += 3.0
			m.ShakeVelY += 2.0
		}
	case ToolBlackHole:
		m.BlackHoleActive = true
	case ToolShake:
		sx, sy := ApplyShake(m.Particles, 4.0, m.Frame)
		if shake {
			m.ShakeVelX += sx * 2
			m.ShakeVelY += sy * 2
		}
		m.Score += 20
	}
}

// MouseMove updates cursor position (for black hole tracking).
func (m *Model) MouseMove(x, y int) {
	m.CursorX = x
	m.CursorY = y
}

// MouseRelease stops continuous tool effects.
func (m *Model) MouseRelease() {
	m.BlackHoleActive = false
}

// CycleTool switches to the next tool.
func (m *Model) CycleTool() {
	m.Tool = NextTool(m.Tool)
	m.BlackHoleActive = false
}

// SetTool sets the active tool directly.
func (m *Model) SetTool(t Tool) {
	if t >= 0 && t < toolCount {
		m.Tool = t
		m.BlackHoleActive = false
	}
}

// View renders the current particle state as a full-screen string with colors.
func (m *Model) View() string {
	if !m.Active {
		return ""
	}

	// Styled cell grid: each cell is either "" (empty/space) or "ansiPrefix + rune + reset"
	type cell struct {
		styled string
		empty  bool
	}
	grid := make([][]cell, m.Height)
	for y := range grid {
		grid[y] = make([]cell, m.Width)
		for x := range grid[y] {
			grid[y][x] = cell{empty: true}
		}
	}

	// Place particles with their original ANSI styles.
	// Clamp shake offset to prevent drag-induced drift.
	shakeOffX := max(-3, min(3, int(math.Round(m.ShakeX))))
	shakeOffY := max(-2, min(2, int(math.Round(m.ShakeY))))

	for i := range m.Particles {
		p := &m.Particles[i]
		if !p.Alive {
			continue
		}
		px := int(math.Round(p.X)) + shakeOffX
		py := int(math.Round(p.Y)) + shakeOffY
		if px >= 0 && px < m.Width && py >= 0 && py < m.Height {
			grid[py][px] = cell{styled: p.Rendered}
		}
	}

	// Draw tool cursor at mouse position
	if m.Phase == PhaseDestroy {
		cursorStyle := "\x1b[1;33m" // bold yellow
		for _, g := range ToolCursor(m.Tool) {
			cx := m.CursorX + g.DX + shakeOffX
			cy := m.CursorY + g.DY + shakeOffY
			if cx >= 0 && cx < m.Width && cy >= 0 && cy < m.Height {
				grid[cy][cx] = cell{styled: cursorStyle + string(g.Ch) + "\x1b[0m"}
			}
		}
	}

	// Render grid to string
	var sb strings.Builder
	sb.Grow(m.Width * m.Height * 4) // extra space for ANSI sequences
	for y, row := range grid {
		if y > 0 {
			sb.WriteByte('\n')
		}
		// Find last non-empty cell to trim trailing spaces
		last := m.Width - 1
		for last >= 0 && row[last].empty {
			last--
		}
		for x := 0; x <= last; x++ {
			if row[x].empty {
				sb.WriteByte(' ')
			} else {
				sb.WriteString(row[x].styled)
			}
		}
	}

	content := sb.String()

	// Overlay HUD
	hud := m.renderHUD()
	hudW := ansi.StringWidth(hud)
	hudCol := max(m.Width-hudW-2, 0)
	content = ui.OverlayAt(content, hud, 0, hudCol)

	return content
}

// renderHUD shows the current tool and score.
func (m *Model) renderHUD() string {
	toolStyle := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#ef4444"})
	scoreStyle := lipgloss.NewStyle().Bold(true).
		Foreground(ui.ColorWorking)
	dimStyle := lipgloss.NewStyle().
		Foreground(ui.ColorMuted)

	var phaseLabel string
	if m.Phase == PhaseRebuild {
		phaseLabel = dimStyle.Render(" [rebuilding...]")
	}

	return fmt.Sprintf("%s %s  %s %s%s",
		toolStyle.Render(ToolIcon(m.Tool)),
		toolStyle.Render(ToolName(m.Tool)),
		scoreStyle.Render(fmt.Sprintf("Score: %d", m.Score)),
		dimStyle.Render("tab:tool esc:exit"),
		phaseLabel,
	)
}
