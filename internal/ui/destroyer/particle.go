package destroyer

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

// Particle is a single rune with physics-driven position.
type Particle struct {
	// Original position (for rebuild animation)
	OrigX, OrigY int
	Rune         rune
	Rendered     string // pre-computed "ansiStyle + rune + reset" for zero-alloc rendering

	// Physics state
	X, Y       float64 // current position
	VelX, VelY float64 // velocity
	TgtX, TgtY float64 // spring target

	SpringX harmonica.Spring
	SpringY harmonica.Spring

	Alive     bool    // false = absorbed by black hole
	Settled   bool    // true = resting at floor
	Dislodged bool    // true = has been hit by a tool, now subject to gravity
	GravityY  float64 // accumulated gravity target offset
}

// NewParticle creates a particle at its original screen position.
func NewParticle(x, y int, r rune, ansiStyle string, td float64) Particle {
	return Particle{
		OrigX:    x,
		OrigY:    y,
		Rune:     r,
		Rendered: ansiStyle + string(r) + "\x1b[0m",
		X:        float64(x),
		Y:        float64(y),
		TgtX:     float64(x),
		TgtY:     float64(y),
		SpringX:  harmonica.NewSpring(td, 4.0, 0.4),
		SpringY:  harmonica.NewSpring(td, 4.0, 0.4),
		Alive:    true,
	}
}

// Tick advances the particle physics by one frame.
// floorY is the terminal bottom edge (particles settle here).
func (p *Particle) Tick(floorY, rightWall int) {
	if !p.Alive || p.Settled {
		return
	}

	// Particles stay pinned until dislodged by a tool
	if !p.Dislodged {
		return
	}

	// Apply gravity: pull target downward each frame
	p.GravityY += 0.6
	p.TgtY = float64(p.OrigY) + p.GravityY

	// Cap target at floor
	fy := float64(floorY)
	if p.TgtY > fy {
		p.TgtY = fy
	}

	// Update springs
	p.X, p.VelX = p.SpringX.Update(p.X, p.VelX, p.TgtX)
	p.Y, p.VelY = p.SpringY.Update(p.Y, p.VelY, p.TgtY)

	// Wall bounce (left)
	if p.X < 0 {
		p.X = 0
		p.VelX = -p.VelX * 0.5
	}
	// Wall bounce (right)
	rw := float64(rightWall)
	if p.X > rw {
		p.X = rw
		p.VelX = -p.VelX * 0.5
	}
	// Floor bounce
	if p.Y > fy {
		p.Y = fy
		p.VelY = -p.VelY * 0.3
	}
	// Ceiling bounce
	if p.Y < 0 {
		p.Y = 0
		p.VelY = -p.VelY * 0.5
	}

	// Settle detection: at floor with low velocity
	if p.Y >= fy-0.5 && math.Abs(p.VelY) < 0.1 && math.Abs(p.VelX) < 0.1 {
		p.Y = fy
		p.VelX = 0
		p.VelY = 0
		p.Settled = true
	}
}

// ApplyImpulse adds an instantaneous velocity change and dislodges the particle.
func (p *Particle) ApplyImpulse(dvx, dvy float64) {
	p.Dislodged = true
	p.Settled = false
	p.VelX += dvx
	p.VelY += dvy
	// Detach from original position
	p.TgtX = p.X + dvx*2
}

// ResetToOrigin sets targets back to original positions (rebuild animation).
func (p *Particle) ResetToOrigin(td float64) {
	p.Alive = true
	p.Settled = false
	p.GravityY = 0
	p.TgtX = float64(p.OrigX)
	p.TgtY = float64(p.OrigY)
	// Use a snappy spring for the return trip
	p.SpringX = harmonica.NewSpring(td, 5.0, 0.5)
	p.SpringY = harmonica.NewSpring(td, 5.0, 0.5)
}

// IsHome reports whether the particle is back at its original position.
func (p *Particle) IsHome() bool {
	return math.Abs(p.X-float64(p.OrigX)) < 0.5 &&
		math.Abs(p.Y-float64(p.OrigY)) < 0.5 &&
		math.Abs(p.VelX) < 0.2 && math.Abs(p.VelY) < 0.2
}
