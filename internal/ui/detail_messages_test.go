package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/claude"
)

func TestInjectAfterPromptUsesPromptMarkers(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
	}{
		{name: "first marker", prompt: "❯"},
		{name: "second marker", prompt: "›"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			viewport := "assistant output\n\x1b[1m" + test.prompt + "\x1b[0m placeholder\nstatus"
			want := "assistant output\nRELAY INPUT\nstatus"
			if got := injectAfterPrompt(viewport, "RELAY INPUT", []string{test.prompt}); got != want {
				t.Fatalf("injectAfterPrompt() = %q, want %q", got, want)
			}
		})
	}
}

func TestNextEntryBoundaryUsesPromptMarkers(t *testing.T) {
	lines := []string{"anchor", "continuation", "  › prompt", "after"}
	if got := nextEntryBoundary(lines, 0, len(lines), []string{"›"}); got != 2 {
		t.Fatalf("nextEntryBoundary() = %d, want 2", got)
	}
}

func TestInjectAfterPromptDoesNotUseOtherProviderGlyph(t *testing.T) {
	viewport := "❯ claude prompt\nstatus"
	if got := injectAfterPrompt(viewport, "RELAY INPUT", []string{"›"}); got != viewport {
		t.Fatalf("injectAfterPrompt() = %q, want unchanged viewport", got)
	}
}

func TestInjectAfterPromptRequiresMarkerAtLineStart(t *testing.T) {
	viewport := "assistant mentioned › in output\nstatus"
	if got := injectAfterPrompt(viewport, "RELAY INPUT", []string{"›"}); got != viewport {
		t.Fatalf("injectAfterPrompt() = %q, want unchanged viewport", got)
	}
}

func TestInjectAfterPromptPreservesPromptBackground(t *testing.T) {
	const fill = "\033[48;2;58;56;75m"
	const reset = "\033[0m"
	promptLine := fill + "› prompt" + strings.Repeat(" ", 16) + reset
	viewport := "output\n" + promptLine + "\nafter"
	got := injectAfterPrompt(viewport, "✎ new name", []string{"›"})
	want := "output\n" + fill + "✎ new name" + fill + strings.Repeat(" ", 14) + reset + "\nafter"
	if got != want {
		t.Fatalf("injectAfterPrompt() = %q, want %q", got, want)
	}
}

func TestWrapLinesExtendsPromptBackgroundToViewportWidth(t *testing.T) {
	const (
		fill  = "\033[48;2;58;56;75m"
		reset = "\033[0m"
	)
	line := fill + "› Use /skills" + reset
	continued := fill + "continued" + reset
	blank := fill + reset
	got := wrapLines(line+"\n"+continued+"\n"+blank, 24, 24, []string{"›"})

	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("wrapped prompt lines = %d, want 3: %q", len(lines), got)
	}
	for i, gotLine := range lines {
		if width := ansi.StringWidth(gotLine); width != 24 {
			t.Fatalf("wrapped prompt line %d width = %d, want 24: %q", i, width, gotLine)
		}
	}
	resetAt := strings.Index(lines[0], reset)
	if resetAt < 0 || !strings.Contains(lines[0][:resetAt], "   ") {
		t.Fatalf("prompt fill was not inserted before reset: %q", lines[0])
	}
}

func TestWrapLinesDoesNotStylePlainPromptPadding(t *testing.T) {
	line := "› Use /skills"
	got := wrapLines(line, 24, 24, []string{"›"})
	if got != line {
		t.Fatalf("plain prompt changed without a captured fill: %q", got)
	}
}

func TestWrapLinesNormalizesPromptCursorBackground(t *testing.T) {
	const (
		cursor = "\033[48;2;25;24;38m"
		fill   = "\033[48;2;58;56;75m"
		reset  = "\033[0m"
	)
	line := cursor + "›" + reset + fill + " Use /skills" + reset
	got := wrapLines(line, 24, 24, []string{"›"})

	marker := strings.Index(ansi.Strip(got), "›")
	if marker < 0 {
		t.Fatalf("normalized prompt lost marker: %q", got)
	}
	fillAt := strings.Index(got, fill)
	if fillAt < 0 {
		t.Fatalf("normalized prompt lost editor fill: %q", got)
	}
	rawMarker := strings.Index(got, "›")
	if rawMarker < 0 || !strings.Contains(got[:rawMarker], reset+fill) {
		t.Fatalf("cursor background still controls marker: %q", got)
	}
}

func TestTrimTrailingBlanksKeepsFilledPromptRows(t *testing.T) {
	const fill = "\033[48;2;58;56;75m"
	const reset = "\033[0m"
	content := "output\n" + fill + "› prompt" + reset + "\n" + fill + reset + "\n"
	got := trimTrailingBlanks(content, []string{"›"})
	if !strings.HasSuffix(got, fill+reset) {
		t.Fatalf("filled prompt row was trimmed: %q", got)
	}

	plain := trimTrailingBlanks(content, nil)
	if strings.HasSuffix(plain, fill+reset) {
		t.Fatalf("unmarked filled row was preserved: %q", plain)
	}
}

func TestParseMarkerToolName(t *testing.T) {
	cases := []struct {
		line string
		name string
		ok   bool
	}{
		{"⏺ Update(/abs/path/foo.go)", "Update", true},
		{"⏺ Edit(/abs/path/foo.go)", "Edit", true},
		{"⏺ MultiEdit(/abs/path/foo.go)", "MultiEdit", true},
		{"⏺ Write(/abs/path/foo.go)", "Write", true},
		{"⏺ I'll start by reading the file.", "", false},
		{"⏺ ", "", false},
		{"⏺", "", false},
		{"⏺ update(lowercase_not_a_tool)", "", false},
		{"⏺ Foo bar baz", "", false},
	}
	for _, c := range cases {
		gotName, gotOk := parseMarkerToolName(c.line)
		if gotName != c.name || gotOk != c.ok {
			t.Errorf("parseMarkerToolName(%q) = (%q, %v), want (%q, %v)",
				c.line, gotName, gotOk, c.name, c.ok)
		}
	}
}

// TestFindEventSubOffsets_SkipsTextMarkersWithinMergedEdit covers the
// invariant that makes hide-bridged merges work: when an Edit event spans
// multiple `⏺` sub-blocks separated by an interleaved text `⏺` in the captured
// pane, the marker walk skips the text marker (kind mismatch) and assigns the
// two non-adjacent edit markers to the two sub-positions.
func TestFindEventSubOffsets_SkipsTextMarkersWithinMergedEdit(t *testing.T) {
	// Synthetic pane snippet that mirrors what Claude Code prints. The user
	// message anchors at line 0; everything below is what `findEventSubOffsets`
	// scans for `⏺` markers.
	lines := []string{
		"> the user prompt that started the turn",
		"",
		"⏺ I'll start by reading the file.",
		"",
		"⏺ Update(/abs/path/foo.go)",
		"  ⎿  Updated foo.go with 2 additions and 1 removal",
		"",
		"⏺ Now I'll also fix the other one.",
		"",
		"⏺ Update(/abs/path/foo.go)",
		"  ⎿  Updated foo.go with 1 addition",
		"",
		"⏺ Done.",
	}
	content := strings.Join(lines, "\n")

	// What the outline builds when `gi` is ON: the interleaved text between
	// the two foo.go edits is dropped, then the two edits collapse into one
	// merged event with BlockCount=2.
	visibleEvents := []claude.TurnEvent{
		{Kind: claude.TurnEventText, BlockCount: 1},
		{Kind: claude.TurnEventEdit, FilePath: "/abs/path/foo.go", BlockCount: 2},
		{Kind: claude.TurnEventText, BlockCount: 1},
	}

	offsets := findEventSubOffsets(content, 0, visibleEvents)

	if len(offsets) != 3 {
		t.Fatalf("len(offsets) = %d, want 3", len(offsets))
	}
	if got := len(offsets[0]); got != 1 {
		t.Errorf("offsets[0] len = %d, want 1 (opening text)", got)
	}
	if got := len(offsets[1]); got != 2 {
		t.Errorf("offsets[1] len = %d, want 2 (merged edit, 2 sub-blocks)", got)
	}
	if got := len(offsets[2]); got != 1 {
		t.Errorf("offsets[2] len = %d, want 1 (closing text)", got)
	}

	if offsets[0][0] != 2 {
		t.Errorf("opening text offset = %d, want 2", offsets[0][0])
	}
	if offsets[1][0] != 4 {
		t.Errorf("merged edit sub-0 offset = %d, want 4", offsets[1][0])
	}
	if offsets[1][1] != 9 {
		t.Errorf("merged edit sub-1 offset = %d, want 9 (skipping interleaved text marker at line 7)", offsets[1][1])
	}
	if offsets[2][0] != 12 {
		t.Errorf("closing text offset = %d, want 12", offsets[2][0])
	}
}

func TestFindEventSubOffsets_NoMarkersBeforeLastUserLine(t *testing.T) {
	// Anything before lastUserLine is part of an earlier turn and must not be
	// consumed.
	lines := []string{
		"⏺ Update(/old/file.go)",
		"  ⎿  earlier turn marker",
		"> the user prompt for THIS turn",
		"",
		"⏺ Update(/new/file.go)",
	}
	content := strings.Join(lines, "\n")
	events := []claude.TurnEvent{
		{Kind: claude.TurnEventEdit, FilePath: "/new/file.go", BlockCount: 1},
	}
	offsets := findEventSubOffsets(content, 2, events)
	if len(offsets) != 1 || len(offsets[0]) != 1 {
		t.Fatalf("unexpected offsets shape: %+v", offsets)
	}
	if offsets[0][0] != 4 {
		t.Errorf("offset = %d, want 4 (must skip the pre-turn marker at line 0)", offsets[0][0])
	}
}

// ── Cursor round-trip across the hide toggle ──────────────────────────────

// TestSetHideInterleavedMessages_RoundTripPreservesCursor parks the cursor on
// the second of two same-file Edits separated by an interleaved text, hides
// (the two edits merge into one row), then unhides — cursor must land back on
// the original raw edit.
func TestSetHideInterleavedMessages_RoundTripPreservesCursor(t *testing.T) {
	m := NewDetailModel()
	m.userMessages = []string{"the user message"}
	m.currentTurn = claude.CurrentTurn{Events: []claude.TurnEvent{
		{Kind: claude.TurnEventText, BlockCount: 1},
		{Kind: claude.TurnEventEdit, FilePath: "foo.go", BlockCount: 1},
		{Kind: claude.TurnEventText, BlockCount: 1}, // interleaved
		{Kind: claude.TurnEventEdit, FilePath: "foo.go", BlockCount: 1},
		{Kind: claude.TurnEventText, BlockCount: 1},
	}}
	m.recomputeVisibleEvents()

	// Park on raw event 3 (the second foo.go edit) — cursor index 1+3 = 4.
	m.msgCursor = len(m.userMessages) + 3
	m.subCursor = 0

	m.SetHideInterleavedMessages(true)

	// Visible events with hiding: [text, Edit foo.go ×2 (merged), text].
	// Raw idx 3 is now sub-position 1 of the merged row at visible idx 1.
	if got, want := m.msgCursor, len(m.userMessages)+1; got != want {
		t.Fatalf("after hide: msgCursor = %d, want %d", got, want)
	}
	if got, want := m.subCursor, 1; got != want {
		t.Errorf("after hide: subCursor = %d, want %d", got, want)
	}

	m.SetHideInterleavedMessages(false)

	if got, want := m.msgCursor, len(m.userMessages)+3; got != want {
		t.Errorf("after un-hide: msgCursor = %d, want %d", got, want)
	}
	if got, want := m.subCursor, 0; got != want {
		t.Errorf("after un-hide: subCursor = %d, want %d", got, want)
	}
}

// TestSetHideInterleavedMessages_OnHiddenText_SnapsForward parks the cursor on
// a text event that's about to be hidden — it must snap to the next visible
// event, not stay invisible.
func TestSetHideInterleavedMessages_OnHiddenText_SnapsForward(t *testing.T) {
	m := NewDetailModel()
	m.userMessages = []string{"u"}
	m.currentTurn = claude.CurrentTurn{Events: []claude.TurnEvent{
		{Kind: claude.TurnEventEdit, FilePath: "a.go", BlockCount: 1},
		{Kind: claude.TurnEventText, BlockCount: 1}, // interleaved
		{Kind: claude.TurnEventEdit, FilePath: "b.go", BlockCount: 1},
	}}
	m.recomputeVisibleEvents()

	// Park on the interleaved text (raw event 1) → cursor 1+1 = 2.
	m.msgCursor = len(m.userMessages) + 1
	m.subCursor = 0

	m.SetHideInterleavedMessages(true)

	// Visible events: [Edit a.go, Edit b.go]. Snap forward to b.go (visible idx 1).
	if got, want := m.msgCursor, len(m.userMessages)+1; got != want {
		t.Errorf("snap-forward: msgCursor = %d, want %d (Edit b.go)", got, want)
	}
}

func TestRecomputeVisibleEvents_RebuildsOnPrefChange(t *testing.T) {
	m := NewDetailModel()
	m.userMessages = []string{"u"}
	m.currentTurn = claude.CurrentTurn{Events: []claude.TurnEvent{
		{Kind: claude.TurnEventEdit, FilePath: "a.go", BlockCount: 1},
		{Kind: claude.TurnEventText, BlockCount: 1},
		{Kind: claude.TurnEventEdit, FilePath: "b.go", BlockCount: 1},
	}}
	m.recomputeVisibleEvents()
	if len(m.visibleEvents) != 3 {
		t.Errorf("with hide=false: len(visibleEvents) = %d, want 3", len(m.visibleEvents))
	}
	if m.hiddenMessageCount != 0 {
		t.Errorf("with hide=false: hiddenMessageCount = %d, want 0", m.hiddenMessageCount)
	}
	m.SetHideInterleavedMessages(true)
	if len(m.visibleEvents) != 2 {
		t.Errorf("with hide=true: len(visibleEvents) = %d, want 2", len(m.visibleEvents))
	}
	if m.hiddenMessageCount != 1 {
		t.Errorf("with hide=true: hiddenMessageCount = %d, want 1", m.hiddenMessageCount)
	}
}
