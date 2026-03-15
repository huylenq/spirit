package destroyer

import "math"

// Tool identifies which destruction tool is active.
type Tool int

const (
	ToolHammer    Tool = iota // 🔨 shatter on click
	ToolBomb                  // 💣 radial explosion
	ToolBlackHole             // 🕳️ gravity well
	ToolShake                 // 🌪️ whole screen trembles
	toolCount
)

// ToolName returns the display name with emoji for a tool.
func ToolName(t Tool) string {
	switch t {
	case ToolHammer:
		return "Hammer"
	case ToolBomb:
		return "Bomb"
	case ToolBlackHole:
		return "Black Hole"
	case ToolShake:
		return "Shake"
	default:
		return "?"
	}
}

// ToolIcon returns the emoji for a tool.
func ToolIcon(t Tool) string {
	switch t {
	case ToolHammer:
		return "\U0001f528" // 🔨
	case ToolBomb:
		return "\U0001f4a3" // 💣
	case ToolBlackHole:
		return "\U0001f573\ufe0f" // 🕳️
	case ToolShake:
		return "\U0001f32a\ufe0f" // 🌪️
	default:
		return "?"
	}
}

// CursorGlyph is a single character in a tool's cursor shape.
type CursorGlyph struct {
	DX, DY int  // offset from cursor center
	Ch     rune // character to draw
}

// ToolCursor returns ASCII art glyphs for the cursor. Each tool gets a distinct shape.
func ToolCursor(t Tool) []CursorGlyph {
	switch t {
	case ToolHammer:
		return []CursorGlyph{
			{0, 0, '+'},
			{-1, 0, '-'}, {1, 0, '-'},
			{0, -1, '|'},
		}
	case ToolBomb:
		return []CursorGlyph{
			{0, 0, '*'},
			{-1, -1, '/'}, {1, -1, '\\'},
			{-1, 1, '\\'}, {1, 1, '/'},
		}
	case ToolBlackHole:
		return []CursorGlyph{
			{0, 0, '@'},
			{1, 0, ')'}, {-1, 0, '('},
			{0, -1, '~'}, {0, 1, '~'},
		}
	case ToolShake:
		return []CursorGlyph{
			{0, 0, '#'},
			{-1, 0, '~'}, {1, 0, '~'},
		}
	default:
		return []CursorGlyph{{0, 0, '+'}}
	}
}

// NextTool cycles to the next tool.
func NextTool(t Tool) Tool {
	return (t + 1) % toolCount
}

// ApplyHammer shatters particles near (cx, cy) within radius.
// Particles are flung outward from the impact point.
func ApplyHammer(particles []Particle, cx, cy, radius float64) int {
	hits := 0
	for i := range particles {
		p := &particles[i]
		if !p.Alive {
			continue
		}
		dx := p.X - cx
		dy := p.Y - cy
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist > radius {
			continue
		}
		hits++
		// Impulse proportional to closeness (inverse distance)
		strength := (radius - dist) / radius * 8.0
		if dist < 0.1 {
			dist = 0.1
		}
		p.ApplyImpulse(dx/dist*strength, dy/dist*strength-2) // slight upward bias
	}
	return hits
}

// ApplyBomb creates a radial explosion from (cx, cy).
// Larger radius and stronger force than hammer.
func ApplyBomb(particles []Particle, cx, cy, radius float64) int {
	hits := 0
	for i := range particles {
		p := &particles[i]
		if !p.Alive {
			continue
		}
		dx := p.X - cx
		dy := p.Y - cy
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist > radius {
			continue
		}
		hits++
		strength := (radius - dist) / radius * 14.0
		if dist < 0.1 {
			dist = 0.1
		}
		p.Dislodged = true
		p.Settled = false
		p.GravityY = 0                                       // reset gravity so they fly
		p.ApplyImpulse(dx/dist*strength, dy/dist*strength-4) // strong upward
	}
	return hits
}

// ApplyBlackHole pulls particles toward (cx, cy) and absorbs nearby ones.
// Returns the number of particles absorbed this frame.
func ApplyBlackHole(particles []Particle, cx, cy, pullRadius, absorbRadius float64) int {
	absorbed := 0
	for i := range particles {
		p := &particles[i]
		if !p.Alive {
			continue
		}
		dx := cx - p.X
		dy := cy - p.Y
		dist := math.Sqrt(dx*dx + dy*dy)

		// Absorb if very close
		if dist < absorbRadius {
			p.Alive = false
			absorbed++
			continue
		}

		if dist > pullRadius {
			continue
		}

		// Gravitational pull: stronger when closer
		strength := (pullRadius - dist) / pullRadius * 3.0
		if dist < 0.5 {
			dist = 0.5
		}
		p.Dislodged = true
		p.Settled = false
		p.TgtX = cx
		p.TgtY = cy
		p.VelX += dx / dist * strength * 0.5
		p.VelY += dy / dist * strength * 0.5
	}
	return absorbed
}

// ApplyShake offsets all particle targets by random-ish spring deltas.
// Returns a (dx, dy) screen shake offset for the renderer.
func ApplyShake(particles []Particle, intensity float64, frame int) (float64, float64) {
	// Deterministic pseudo-random based on frame
	shakeDX := math.Sin(float64(frame)*7.3) * intensity
	shakeDY := math.Cos(float64(frame)*5.1) * intensity * 0.5

	for i := range particles {
		p := &particles[i]
		if !p.Alive {
			continue
		}
		p.Dislodged = true
		p.Settled = false
		// Nudge each particle slightly differently
		nudge := math.Sin(float64(i)*1.7+float64(frame)*3.0) * intensity * 0.3
		p.VelX += nudge
		p.VelY += math.Cos(float64(i)*2.3+float64(frame)*2.0) * intensity * 0.2
	}
	return shakeDX, shakeDY
}
