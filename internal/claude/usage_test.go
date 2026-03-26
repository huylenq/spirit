package claude

import "testing"

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
