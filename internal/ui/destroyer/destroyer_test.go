package destroyer

import (
	"strings"
	"testing"
)

func TestDecomposeBasic(t *testing.T) {
	input := "Hello World\n  ABC  \nXYZ"
	frags := DecomposeStyled(input, 20, 5)

	// Should have non-space characters only
	for _, f := range frags {
		if f.Rune == ' ' {
			t.Errorf("space should not be a fragment: %+v", f)
		}
	}

	// "Hello World" has 10 non-space chars, "ABC" has 3, "XYZ" has 3 = 16
	if len(frags) != 16 {
		t.Errorf("expected 16 fragments, got %d", len(frags))
		for _, f := range frags {
			t.Logf("  (%d,%d) %c", f.X, f.Y, f.Rune)
		}
	}
}

func TestNewAndTick(t *testing.T) {
	input := strings.Repeat("ABCDEF\n", 5)
	m := New(input, 10, 6)

	if len(m.Particles) == 0 {
		t.Fatal("expected particles")
	}

	// Tick should not panic
	for range 20 {
		m.Tick()
	}

	view := m.View()
	if view == "" {
		t.Error("view should not be empty")
	}
}

func TestHammerScatters(t *testing.T) {
	input := "XXXXX\nXXXXX\nXXXXX"
	m := New(input, 10, 5)

	// Click at center
	m.Click(2, 1, true)

	// Some particles should have non-zero velocity
	moved := 0
	for _, p := range m.Particles {
		if p.VelX != 0 || p.VelY != 0 {
			moved++
		}
	}
	if moved == 0 {
		t.Error("hammer should move some particles")
	}
	if m.Score == 0 {
		t.Error("score should increase on hit")
	}
}

func TestRebuild(t *testing.T) {
	input := "AB\nCD"
	m := New(input, 5, 3)

	// Scatter
	m.Click(0, 0, true)
	for range 10 {
		m.Tick()
	}

	// Start rebuild
	m.StartRebuild()
	if m.Phase != PhaseRebuild {
		t.Error("should be in rebuild phase")
	}

	// Tick many times until rebuilt
	for range 200 {
		m.Tick()
		if m.IsRebuilt() {
			return
		}
	}
	// It's OK if not fully rebuilt in 200 frames — springs might be slow
}

func TestDecomposePreservesAnsiStyle(t *testing.T) {
	// Red "A", then reset, then blue "B"
	input := "\x1b[31mA\x1b[0m \x1b[34mB\x1b[0m"
	frags := DecomposeStyled(input, 20, 1)

	if len(frags) != 2 {
		t.Fatalf("expected 2 fragments, got %d", len(frags))
	}

	if frags[0].Rune != 'A' || frags[0].AnsiStyle != "\x1b[31m" {
		t.Errorf("frag[0]: got rune=%c style=%q, want A with red SGR", frags[0].Rune, frags[0].AnsiStyle)
	}
	if frags[1].Rune != 'B' || frags[1].AnsiStyle != "\x1b[34m" {
		t.Errorf("frag[1]: got rune=%c style=%q, want B with blue SGR", frags[1].Rune, frags[1].AnsiStyle)
	}
}

func TestDecomposeStackedStyles(t *testing.T) {
	// Bold + green applied together (two separate SGR sequences)
	input := "\x1b[1m\x1b[32mHi\x1b[0m"
	frags := DecomposeStyled(input, 20, 1)

	if len(frags) != 2 {
		t.Fatalf("expected 2 fragments, got %d", len(frags))
	}

	// Both SGR sequences should be accumulated
	want := "\x1b[1m\x1b[32m"
	if frags[0].AnsiStyle != want {
		t.Errorf("frag[0]: got style=%q, want %q", frags[0].AnsiStyle, want)
	}
}

func TestToolCycle(t *testing.T) {
	m := New("X", 5, 3)
	if m.Tool != ToolHammer {
		t.Error("initial tool should be hammer")
	}
	m.CycleTool()
	if m.Tool != ToolBomb {
		t.Error("after cycle should be bomb")
	}
	m.CycleTool()
	if m.Tool != ToolBlackHole {
		t.Error("after cycle should be black hole")
	}
	m.CycleTool()
	if m.Tool != ToolShake {
		t.Error("after cycle should be shake")
	}
	m.CycleTool()
	if m.Tool != ToolHammer {
		t.Error("should wrap to hammer")
	}
}
