package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

// splitSess builds a minimal single-line session (short custom title, no
// subtitle) so row math in the split tests stays 1 entry == 1 visual row.
func splitSess(pane, title, project string, status claude.Status) claude.ClaudeSession {
	return claude.ClaudeSession{
		PaneID:      pane,
		CustomTitle: title,
		Project:     project,
		Status:      status,
	}
}

func rowContaining(lines []string, token string) int {
	for i, l := range lines {
		if strings.Contains(ansi.Strip(l), token) {
			return i
		}
	}
	return -1
}

// isGapRow reports whether a rendered sidebar row is a blank spacer (only the
// dim gutter glyph on an otherwise empty line) — i.e. part of the split gap.
func isGapRow(line string) bool {
	s := strings.TrimRight(ansi.Strip(line), "│┤")
	return strings.TrimSpace(s) == ""
}

// TestSidebarSplitYourTurnTopBottom verifies the default status-grouped body
// pins YOUR TURN to the ceiling and sinks the rest (CLAUDING) to the floor with
// a blank gap between, and that the row-mappers stay consistent across the gap.
func TestSidebarSplitYourTurnTopBottom(t *testing.T) {
	m := NewSidebarModel()
	m.SetHideLastMessage(true)
	m.SetSize(44, 30)
	m.SetItems([]claude.ClaudeSession{
		splitSess("%yt", "Yankee", "alpha", claude.StatusUserTurn),
		splitSess("%cl", "Charlie", "alpha", claude.StatusAgentTurn),
	})

	view := m.View()
	lines := strings.Split(view, "\n")

	if m.splitGap <= 0 {
		t.Fatalf("expected splitGap > 0 (YT top, CLAUDING bottom), got %d", m.splitGap)
	}

	ytRow := rowContaining(lines, "Yankee")
	clRow := rowContaining(lines, "Charlie")
	if ytRow < 0 || clRow < 0 {
		t.Fatalf("both sessions must render: yankee=%d charlie=%d\n%s", ytRow, clRow, ansi.Strip(view))
	}
	if ytRow >= clRow {
		t.Errorf("YOUR TURN row (%d) must be above CLAUDING row (%d)", ytRow, clRow)
	}
	if clRow < len(lines)/2 {
		t.Errorf("CLAUDING row (%d) should be bottom-aligned in %d rows", clRow, len(lines))
	}

	// A blank gap must separate the two blocks, and the mapper must treat it as
	// empty space.
	gapRow := -1
	for i := ytRow + 1; i < clRow; i++ {
		if isGapRow(lines[i]) {
			gapRow = i
			break
		}
	}
	if gapRow < 0 {
		t.Fatalf("expected a blank gap between YT and CLAUDING blocks:\n%s", ansi.Strip(view))
	}
	if got := m.PaneIDAtLine(gapRow); got != "" {
		t.Errorf("PaneIDAtLine(gap row %d) = %q, want empty", gapRow, got)
	}

	// Round-trip: each session's rendered row must map back to its pane, which
	// requires PaneIDAtLine to apply the same gap offset View() inserted.
	if got := m.PaneIDAtLine(ytRow); got != "%yt" {
		t.Errorf("PaneIDAtLine(ytRow %d) = %q, want %%yt", ytRow, got)
	}
	if got := m.PaneIDAtLine(clRow); got != "%cl" {
		t.Errorf("PaneIDAtLine(clRow %d) = %q, want %%cl", clRow, got)
	}
}

// TestSidebarSplitEmptyYourTurn verifies the "always bottom-align" decision:
// with no YOUR TURN sessions the whole body sinks to the floor.
func TestSidebarSplitEmptyYourTurn(t *testing.T) {
	m := NewSidebarModel()
	m.SetHideLastMessage(true)
	m.SetSize(44, 30)
	m.SetItems([]claude.ClaudeSession{
		splitSess("%cl", "Charlie", "alpha", claude.StatusAgentTurn),
	})

	view := m.View()
	lines := strings.Split(view, "\n")

	if m.splitGap <= 0 {
		t.Fatalf("empty YOUR TURN should still sink the body (splitGap > 0), got %d", m.splitGap)
	}
	clRow := rowContaining(lines, "Charlie")
	if clRow < 0 {
		t.Fatalf("CLAUDING session must render:\n%s", ansi.Strip(view))
	}
	if clRow < len(lines)/2 {
		t.Errorf("with empty YOUR TURN, CLAUDING (%d) should be bottom-aligned in %d rows", clRow, len(lines))
	}
	if got := m.PaneIDAtLine(clRow); got != "%cl" {
		t.Errorf("PaneIDAtLine(clRow %d) = %q, want %%cl", clRow, got)
	}
}

// TestSidebarSplitDisabledInGroupByProject verifies group-by-project mode keeps
// the classic top-anchored body (no split gap).
func TestSidebarSplitDisabledInGroupByProject(t *testing.T) {
	m := NewSidebarModel()
	m.SetHideLastMessage(true)
	m.SetGroupByProject(true)
	m.SetSize(44, 30)
	m.SetItems([]claude.ClaudeSession{
		splitSess("%yt", "Yankee", "alpha", claude.StatusUserTurn),
		splitSess("%cl", "Charlie", "alpha", claude.StatusAgentTurn),
	})

	view := m.View()
	lines := strings.Split(view, "\n")

	if m.splitGap != 0 {
		t.Errorf("group-by-project must not split (splitGap == 0), got %d", m.splitGap)
	}
	// Sessions stay top-anchored: the first one should render near the ceiling.
	yankee := rowContaining(lines, "Yankee")
	charlie := rowContaining(lines, "Charlie")
	if yankee < 0 || charlie < 0 {
		t.Fatalf("both sessions must render: yankee=%d charlie=%d", yankee, charlie)
	}
	if yankee > len(lines)/2 {
		t.Errorf("top-anchored body: first session row (%d) should be in the upper half of %d", yankee, len(lines))
	}
	if got := m.PaneIDAtLine(charlie); got != "%cl" {
		t.Errorf("PaneIDAtLine(%d) = %q, want %%cl", charlie, got)
	}
}
