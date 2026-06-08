package claude

import (
	"strings"
	"testing"
)

// TestStripANSI_CursorAddressedLayout reproduces the Claude Code v2.1.x TUI,
// which lays out text with Cursor Horizontal Absolute (\x1b[NG) for word
// spacing and full-screen clears (\x1b[2J) between redraws — rather than
// emitting literal spaces. A naive ANSI stripper collapses the spacing
// (breaking the "Claude Code v" readiness needle) and never erases stale
// frames (parsing the wrong percentages).
func TestStripANSI_CursorAddressedLayout(t *testing.T) {
	// A stale first frame the redraw must erase via \x1b[2J.
	stale := "Current session\r\n██ 99% used\r\nResets 1pm (UTC)\r\n"
	clear := "\x1b[2J\x1b[H"
	// Banner: words positioned by absolute column, no literal spaces between them.
	banner := "Claude\x1b[8GCode\x1b[13Gv2.1.168\r\n"
	dialog := "Current session\r\n" +
		"██\x1b[20G33% used\r\n" +
		"Resets 4:50pm (Asia/Saigon)\r\n" +
		"Current week (all models)\r\n" +
		"██\x1b[20G16% used\r\n" +
		"Resets Jun 14 at 4am (Asia/Saigon)\r\n" +
		"Esc to cancel"

	rendered := stripANSI(stale + clear + banner + dialog)

	// Spacing reconstructed → readiness needle matches.
	if !strings.Contains(rendered, "Claude Code v") {
		t.Fatalf("rendered output missing %q needle:\n%s", "Claude Code v", rendered)
	}

	stats, err := parseUsageDialog(rendered)
	if err != nil {
		t.Fatalf("parse: %v\nrendered:\n%s", err, rendered)
	}
	// The stale 99% frame must have been erased by \x1b[2J.
	if stats.SessionPct != 33 {
		t.Errorf("SessionPct = %d, want 33 (stale frame not erased?)", stats.SessionPct)
	}
	if stats.WeekAllPct != 16 {
		t.Errorf("WeekAllPct = %d, want 16", stats.WeekAllPct)
	}
	if stats.SessionResets != "4:50pm (Asia/Saigon)" {
		t.Errorf("SessionResets = %q, want %q", stats.SessionResets, "4:50pm (Asia/Saigon)")
	}
}

func TestParseUsageDialog_SectionsOnOneLine(t *testing.T) {
	// Simulates what happens when ANSI-stripping collapses the /usage dialog
	// into a single line with all sections side by side.
	input := `Current session  1% used  Resets 6pm (Asia/Saigon)    Current week (all models)  5% used  Resets Mon 6pm (Asia/Saigon)    Current week (Sonnet only)  10% used  Resets Mon 6pm (Asia/Saigon)`

	stats, err := parseUsageDialog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.SessionPct != 1 {
		t.Errorf("SessionPct = %d, want 1", stats.SessionPct)
	}
	if stats.WeekAllPct != 5 {
		t.Errorf("WeekAllPct = %d, want 5", stats.WeekAllPct)
	}
	if stats.WeekSonnetPct != 10 {
		t.Errorf("WeekSonnetPct = %d, want 10", stats.WeekSonnetPct)
	}
}

func TestParseUsageDialog_VerticalSections(t *testing.T) {
	// Traditional vertical format (each section on its own lines)
	input := `
Current session
████  1% used
Resets 6pm (Asia/Saigon)

Current week (all models)
█████████  5% used
Resets Mon 6pm (Asia/Saigon)

Current week (Sonnet only)
██████████████  10% used
Resets Mon 6pm (Asia/Saigon)
`
	stats, err := parseUsageDialog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.SessionPct != 1 {
		t.Errorf("SessionPct = %d, want 1", stats.SessionPct)
	}
	if stats.WeekAllPct != 5 {
		t.Errorf("WeekAllPct = %d, want 5", stats.WeekAllPct)
	}
	if stats.WeekSonnetPct != 10 {
		t.Errorf("WeekSonnetPct = %d, want 10", stats.WeekSonnetPct)
	}
}

func TestParseUsageDialog_MarkerLineWithPctOnly(t *testing.T) {
	// Marker line has % but resets is on the next line
	input := `
Current session  1% used
Resets 6pm (Asia/Saigon)

Current week (all models)  5% used
Resets Mon 6pm (Asia/Saigon)
`
	stats, err := parseUsageDialog(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.SessionPct != 1 {
		t.Errorf("SessionPct = %d, want 1", stats.SessionPct)
	}
	if stats.WeekAllPct != 5 {
		t.Errorf("WeekAllPct = %d, want 5", stats.WeekAllPct)
	}
	if stats.SessionResets != "6pm (Asia/Saigon)" {
		t.Errorf("SessionResets = %q, want %q", stats.SessionResets, "6pm (Asia/Saigon)")
	}
	if stats.WeekAllResets != "Mon 6pm (Asia/Saigon)" {
		t.Errorf("WeekAllResets = %q, want %q", stats.WeekAllResets, "Mon 6pm (Asia/Saigon)")
	}
}
