package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestQuietExitProgression drives the exit shatter and checks it terminates.
func TestQuietExitProgression(t *testing.T) {
	const w, h = 80, 24
	var src strings.Builder
	for i := 0; i < 10; i++ {
		src.WriteString("all clear · pendulums swing · 3 sessions running\n")
	}

	var a QuietExitAnim
	a.Start(src.String(), w, h)
	if !a.Active() {
		t.Fatalf("expected active after Start")
	}
	if len(a.debris) == 0 {
		t.Fatalf("expected debris seeded")
	}

	for i := 0; i < exitFrames; i++ {
		if !a.Active() {
			t.Fatalf("went inactive early at frame %d", i)
		}
		a.Tick()
	}
	if a.Active() {
		t.Fatalf("expected inactive after %d frames", exitFrames)
	}
	if a.debris != nil {
		t.Fatalf("expected debris released on completion")
	}
}

// TestQuietExitOverlayPreservesFrame checks the compositor is transparent: it
// keeps the background's line count and never widens a line (debris only replace
// existing cells or are dropped).
func TestQuietExitOverlayPreservesFrame(t *testing.T) {
	const w, h = 60, 12
	var srcB strings.Builder
	for i := 0; i < 8; i++ {
		srcB.WriteString("quiet scene debris source line here 0123456789\n")
	}

	var a QuietExitAnim
	a.Start(srcB.String(), w, h)

	// A styled background frame standing in for the normal view.
	bgLines := make([]string, h)
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#88aaff"))
	for i := range bgLines {
		bgLines[i] = style.Render(strings.Repeat("X", w))
	}
	bg := strings.Join(bgLines, "\n")
	bgWidths := make([]int, len(bgLines))
	for i, l := range bgLines {
		bgWidths[i] = ansi.StringWidth(l)
	}

	// Overlay every frame; each must preserve structure.
	for f := 0; f < exitFrames; f++ {
		out := a.Overlay(bg)
		outLines := strings.Split(out, "\n")
		if len(outLines) != len(bgLines) {
			t.Fatalf("frame %d: line count changed %d -> %d", f, len(bgLines), len(outLines))
		}
		for i, l := range outLines {
			if got := ansi.StringWidth(l); got != bgWidths[i] {
				t.Fatalf("frame %d line %d: width changed %d -> %d", f, i, bgWidths[i], got)
			}
		}
		a.Tick()
	}
}

// TestCompositeRow verifies width-preserving, single-pass stamping of cells.
func TestCompositeRow(t *testing.T) {
	line := "abcdefghij"
	out := compositeRow(line, []placedCell{{col: 2, cell: "Z"}, {col: 5, cell: "Q"}})
	if ansi.Strip(out) != "abZdeQghij" {
		t.Fatalf("composite: got %q want abZdeQghij", ansi.Strip(out))
	}
	if ansi.StringWidth(out) != ansi.StringWidth(line) {
		t.Fatalf("composite changed width: %d -> %d", ansi.StringWidth(line), ansi.StringWidth(out))
	}
	// Out-of-range and overlapping cells are dropped, not padded.
	out2 := compositeRow(line, []placedCell{{col: 99, cell: "Z"}, {col: -1, cell: "Q"}})
	if ansi.Strip(out2) != line {
		t.Fatalf("out-of-range cells mutated line: %q", ansi.Strip(out2))
	}

	// Many cells stamped in a single pass over a clean line stay byte-bounded —
	// this is how Overlay uses it (all of a row's debris composited at once),
	// unlike the exponential blowup from re-parsing accumulated output.
	styled := "x\x1b[0m"
	clean := strings.Repeat("Y", 40)
	many := make([]placedCell, 40)
	for i := range many {
		many[i] = placedCell{col: i, cell: styled}
	}
	out3 := compositeRow(clean, many)
	if w := ansi.StringWidth(out3); w != 40 {
		t.Fatalf("mass composite changed width to %d", w)
	}
	if len(out3) > 40*len(styled)+64 {
		t.Fatalf("mass composite byte length unexpectedly large: %d", len(out3))
	}
}
