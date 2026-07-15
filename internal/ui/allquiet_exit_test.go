package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestQuietExitMorph drives the exit morph and checks it seeds one particle per
// target cell, renders at fixed dimensions, converges onto the target frame, and
// terminates.
func TestQuietExitMorph(t *testing.T) {
	const w, h = 80, 24

	var calmB strings.Builder
	for i := 0; i < 4; i++ {
		calmB.WriteString("   · all clear · pendulums swing ·   \n")
	}
	var targetB strings.Builder
	for i := 0; i < h; i++ {
		targetB.WriteString(strings.Repeat("abc ", 18)) // dense returning frame
		targetB.WriteByte('\n')
	}
	target := targetB.String()
	wantCells := len(decomposeStyled(target, w, h))
	if wantCells == 0 {
		t.Fatalf("test target produced no cells")
	}

	var a QuietExitAnim
	a.Start(calmB.String(), target, w, h)
	if !a.Active() {
		t.Fatalf("expected active after Start")
	}
	if len(a.debris) != wantCells {
		t.Fatalf("expected one particle per target cell: got %d want %d", len(a.debris), wantCells)
	}

	// Render every frame: fixed height, fixed width.
	for i := 0; i < exitMorphFrames; i++ {
		if !a.Active() {
			t.Fatalf("went inactive early at frame %d", i)
		}
		out := a.Render(w, h)
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("frame %d: expected %d rows, got %d", i, h, len(lines))
		}
		for r, l := range lines {
			if got := ansi.StringWidth(l); got != w {
				t.Fatalf("frame %d row %d: width %d != %d", i, r, got, w)
			}
		}
		a.Tick()
	}
	if a.Active() {
		t.Fatalf("expected inactive after %d frames", exitMorphFrames)
	}
	if a.debris != nil {
		t.Fatalf("expected debris released on completion")
	}
}

// TestQuietExitConverges checks that just before handoff the particles have
// landed on their target cells and morphed into the target glyphs, so the last
// morph frame matches the frame the normal layout renders next.
func TestQuietExitConverges(t *testing.T) {
	const w, h = 80, 24
	calm := strings.Repeat("· quiet ·\n", 3)
	var targetB strings.Builder
	for i := 0; i < h; i++ {
		targetB.WriteString(strings.Repeat("xy ", 24))
		targetB.WriteByte('\n')
	}

	var a QuietExitAnim
	a.Start(calm, targetB.String(), w, h)
	for i := 0; i < exitMorphFrames-1; i++ {
		a.Tick()
	}

	converged, morphed := 0, 0
	for i := range a.debris {
		d := &a.debris[i]
		if math.Hypot(d.tx-d.x, d.ty-d.y) <= 1.0 {
			converged++
		}
		if d.cell == d.landCell {
			morphed++
		}
	}
	n := len(a.debris)
	if frac := float64(converged) / float64(n); frac < 0.9 {
		t.Fatalf("only %d/%d (%.0f%%) particles converged within 1 cell", converged, n, frac*100)
	}
	if frac := float64(morphed) / float64(n); frac < 0.9 {
		t.Fatalf("only %d/%d (%.0f%%) particles morphed to their landing glyph", morphed, n, frac*100)
	}
}
