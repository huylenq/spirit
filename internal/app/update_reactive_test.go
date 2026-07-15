package app

import "testing"

func TestNextReactiveVerbCycle(t *testing.T) {
	cases := []struct {
		enabled, paused bool
		want            string
	}{
		{false, false, "enable"}, // off → enable
		{true, false, "pause"},   // running → pause
		{true, true, "resume"},   // paused → resume
	}
	for _, c := range cases {
		if got := nextReactiveVerb(c.enabled, c.paused); got != c.want {
			t.Errorf("nextReactiveVerb(enabled=%v,paused=%v) = %q, want %q", c.enabled, c.paused, got, c.want)
		}
	}
}

func TestReactiveIndicatorGlyph(t *testing.T) {
	// Off → empty (no glyph competes with the ⚡N attention badge).
	m := Model{}
	if got := m.reactiveIndicator(); got != "" {
		t.Errorf("off indicator = %q, want empty", got)
	}
	// Enabled → non-empty; paused → non-empty and distinct.
	m.reactiveStatus.Enabled = true
	on := m.reactiveIndicator()
	if on == "" {
		t.Fatal("enabled indicator is empty")
	}
	m.reactiveStatus.Paused = true
	paused := m.reactiveIndicator()
	if paused == "" || paused == on {
		t.Fatalf("paused indicator = %q not distinct from on = %q", paused, on)
	}
}
