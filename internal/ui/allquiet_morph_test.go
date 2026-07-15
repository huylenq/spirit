package ui

import (
	"math"
	"strings"
	"testing"
)

// TestIntroMorph drives the all-quiet intro through explode → implode → mobile and
// verifies the debris converges onto the precomputed dashboard target cells.
func TestIntroMorph(t *testing.T) {
	const w, h = 90, 32
	counts := AllQuietCounts{}

	// A realistic-ish source frame: several lines of styled-ish text.
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("the quick brown fox jumps over the lazy dog 0123456789\n")
	}
	src := sb.String()

	var a AllQuietAnim
	a.Init(counts, w, h, true, src)

	if a.phase != phaseExplode {
		t.Fatalf("expected phaseExplode after Init, got %v", a.phase)
	}
	if len(a.targets) == 0 {
		t.Fatalf("expected non-empty implode targets")
	}
	if len(a.debris) == 0 {
		t.Fatalf("expected debris seeded from source frame")
	}

	// Run the explosion; render each frame to ensure no panic.
	for i := 0; i < explodeFrames; i++ {
		if a.phase != phaseExplode {
			t.Fatalf("left phaseExplode early at frame %d (phase %v)", i, a.phase)
		}
		_ = a.Render(w, h, counts)
		a.Tick()
	}
	if a.phase != phaseImplode {
		t.Fatalf("expected phaseImplode after %d explode frames, got %v", explodeFrames, a.phase)
	}

	// At least one debris particle should be bound to a target.
	bound := 0
	for i := range a.debris {
		if a.debris[i].hasTarget {
			bound++
		}
	}
	if bound == 0 {
		t.Fatalf("expected some debris bound to targets during implode")
	}

	// Run the implode; render each frame.
	for i := 0; i < implodeFrames; i++ {
		if a.phase != phaseImplode {
			t.Fatalf("left phaseImplode early at frame %d (phase %v)", i, a.phase)
		}
		_ = a.Render(w, h, counts)
		a.Tick()
	}
	if a.phase != phaseMobile {
		t.Fatalf("expected phaseMobile after implode, got %v", a.phase)
	}
	if a.debris != nil {
		t.Fatalf("expected debris cleared on handoff to mobile")
	}

	// Re-run and check convergence one frame before handoff: bound debris should
	// be sitting essentially on their target cells.
	var b AllQuietAnim
	b.Init(counts, w, h, true, src)
	for i := 0; i < explodeFrames; i++ {
		b.Tick()
	}
	for i := 0; i < implodeFrames-1; i++ {
		b.Tick()
	}
	converged, checked := 0, 0
	for i := range b.debris {
		d := &b.debris[i]
		if !d.hasTarget {
			continue
		}
		checked++
		if math.Hypot(d.tx-d.x, d.ty-d.y) <= 1.0 {
			converged++
		}
	}
	if checked == 0 {
		t.Fatalf("no bound debris to check convergence")
	}
	if frac := float64(converged) / float64(checked); frac < 0.9 {
		t.Fatalf("only %d/%d (%.0f%%) bound debris converged within 1 cell", converged, checked, frac*100)
	}
}
