package claude

import (
	"bytes"
	_ "embed"
	"reflect"
	"testing"
)

//go:embed testdata/turn_implementing_feature.jsonl
var realTurnFixture []byte

// parseTurnFromBytes mirrors ReadCurrentTurn's body without file I/O so tests
// can drive the parser from an embedded fixture.
func parseTurnFromBytes(data []byte) CurrentTurn {
	lines := bytes.Split(data, []byte{'\n'})
	lastUserIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) == 0 {
			continue
		}
		if extractUserText(lines[i]) != "" {
			lastUserIdx = i
			break
		}
	}
	var turn CurrentTurn
	if lastUserIdx < 0 {
		return turn
	}
	for i := lastUserIdx + 1; i < len(lines); i++ {
		if len(lines[i]) == 0 {
			continue
		}
		if !bytes.Contains(lines[i], []byte(`"type":"assistant"`)) {
			continue
		}
		appendAssistantEvents(lines[i], &turn.Events)
	}
	return turn
}

// summarizeEvents renders a short shape-only view of the events for failure
// messages.
func summarizeEvents(events []TurnEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		fp := e.FilePath
		if j := bytes.LastIndexByte([]byte(fp), '/'); j >= 0 {
			fp = fp[j+1:]
		}
		if fp == "" {
			out[i] = string(e.Kind)
		} else {
			out[i] = string(e.Kind) + "(" + fp + "×" + itoa(e.BlockCount) + ")"
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ── Real-data integration ─────────────────────────────────────────────────

func TestParseRealTurn_ShapeMatchesExpectedMerging(t *testing.T) {
	turn := parseTurnFromBytes(realTurnFixture)
	// The fixture (1 user + 10 real assistant lines) should parse to:
	//   text, Edit detail_messages.go ×3, text, Edit detail_view.go ×2,
	//   text, Edit model.go, text
	// — 7 events, with parse-time same-file merging.
	if got, want := len(turn.Events), 7; got != want {
		t.Fatalf("event count = %d, want %d; events: %v", got, want, summarizeEvents(turn.Events))
	}
	expectKind := []TurnEventKind{
		TurnEventText, TurnEventEdit, TurnEventText, TurnEventEdit,
		TurnEventText, TurnEventEdit, TurnEventText,
	}
	for i, want := range expectKind {
		if turn.Events[i].Kind != want {
			t.Errorf("event[%d] kind = %q, want %q", i, turn.Events[i].Kind, want)
		}
	}
	if got := turn.Events[1].BlockCount; got != 3 {
		t.Errorf("event[1] BlockCount = %d, want 3 (three consecutive Edits to detail_messages.go)", got)
	}
	if got := turn.Events[3].BlockCount; got != 2 {
		t.Errorf("event[3] BlockCount = %d, want 2 (two consecutive Edits to detail_view.go)", got)
	}
	if got := turn.Events[5].BlockCount; got != 1 {
		t.Errorf("event[5] BlockCount = %d, want 1 (single Edit to model.go)", got)
	}
}

func TestFilteredEvents_RealTurn_HideDropsInterleavedText(t *testing.T) {
	turn := parseTurnFromBytes(realTurnFixture)
	vis, sources, hidden := FilteredEvents(turn.Events, true)

	if hidden != 2 {
		t.Errorf("hidden = %d, want 2 (texts at raw idx 2 and 4)", hidden)
	}
	if got, want := len(vis), 5; got != want {
		t.Fatalf("visible count = %d, want %d; visible: %v", got, want, summarizeEvents(vis))
	}
	// Opening and closing text preserved.
	if vis[0].Kind != TurnEventText {
		t.Errorf("visible[0] should be opening text, got %q", vis[0].Kind)
	}
	if vis[len(vis)-1].Kind != TurnEventText {
		t.Errorf("visible[last] should be closing text, got %q", vis[len(vis)-1].Kind)
	}
	// File-tool rows in the middle, no merging across files (paths differ).
	if vis[1].BlockCount != 3 {
		t.Errorf("visible[1] BlockCount = %d, want 3", vis[1].BlockCount)
	}
	if vis[2].BlockCount != 2 {
		t.Errorf("visible[2] BlockCount = %d, want 2", vis[2].BlockCount)
	}
	if vis[3].BlockCount != 1 {
		t.Errorf("visible[3] BlockCount = %d, want 1", vis[3].BlockCount)
	}
	// Sources for the cross-file events are single-element (no hide-bridged merge here).
	for i, srcs := range sources {
		if len(srcs) != 1 {
			t.Errorf("sources[%d] = %v, want single-element (no merging across distinct files)", i, srcs)
		}
	}
	// The closing text was at raw index 6.
	if sources[4][0] != 6 {
		t.Errorf("sources[4][0] = %d, want 6", sources[4][0])
	}
}

// ── Hand-crafted unit tests for FilteredEvents ────────────────────────────

func TestFilteredEvents_NoHide_IdentitySources(t *testing.T) {
	events := []TurnEvent{
		{Kind: TurnEventText, Text: "hi", BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "a.go", BlockCount: 1},
	}
	vis, sources, hidden := FilteredEvents(events, false)
	if !reflect.DeepEqual(vis, events) {
		t.Errorf("vis = %v, want unchanged input", vis)
	}
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0", hidden)
	}
	if !reflect.DeepEqual(sources, [][]int{{0}, {1}}) {
		t.Errorf("sources = %v, want identity [[0],[1]]", sources)
	}
}

func TestFilteredEvents_Hide_PreservesFirstAndLastText(t *testing.T) {
	events := []TurnEvent{
		{Kind: TurnEventText, Text: "open", BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "a.go", BlockCount: 1},
		{Kind: TurnEventText, Text: "interleaved", BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "b.go", BlockCount: 1},
		{Kind: TurnEventText, Text: "close", BlockCount: 1},
	}
	vis, _, hidden := FilteredEvents(events, true)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	if len(vis) != 4 {
		t.Fatalf("len(vis) = %d, want 4; vis = %v", len(vis), summarizeEvents(vis))
	}
	if vis[0].Text != "open" {
		t.Errorf("opening text not preserved: got %q", vis[0].Text)
	}
	if vis[3].Text != "close" {
		t.Errorf("closing text not preserved: got %q", vis[3].Text)
	}
}

func TestFilteredEvents_Hide_BridgedSameFileMerge(t *testing.T) {
	// E(a.go) → text → E(a.go): hide drops the text, then the two same-file
	// edits merge into one row with BlockCount 2 and summed stats.
	events := []TurnEvent{
		{Kind: TurnEventText, Text: "open", BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "a.go", Added: 1, Removed: 0, BlockCount: 1},
		{Kind: TurnEventText, Text: "between", BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "a.go", Added: 2, Removed: 1, BlockCount: 1},
		{Kind: TurnEventText, Text: "close", BlockCount: 1},
	}
	vis, sources, hidden := FilteredEvents(events, true)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	if len(vis) != 3 {
		t.Fatalf("len(vis) = %d, want 3; vis = %v", len(vis), summarizeEvents(vis))
	}
	merged := vis[1]
	if merged.FilePath != "a.go" {
		t.Errorf("merged.FilePath = %q, want %q", merged.FilePath, "a.go")
	}
	if merged.BlockCount != 2 {
		t.Errorf("merged.BlockCount = %d, want 2", merged.BlockCount)
	}
	if merged.Added != 3 || merged.Removed != 1 {
		t.Errorf("merged stats = +%d -%d, want +3 -1", merged.Added, merged.Removed)
	}
	if !reflect.DeepEqual(sources[1], []int{1, 3}) {
		t.Errorf("sources[1] = %v, want [1,3] (raw indices of the merged edits)", sources[1])
	}
}

func TestFilteredEvents_Hide_CrossFile_DoesNotMerge(t *testing.T) {
	events := []TurnEvent{
		{Kind: TurnEventEdit, FilePath: "a.go", BlockCount: 1},
		{Kind: TurnEventText, Text: "between", BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "b.go", BlockCount: 1},
	}
	vis, _, hidden := FilteredEvents(events, true)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	if len(vis) != 2 {
		t.Fatalf("len(vis) = %d, want 2", len(vis))
	}
	if vis[0].FilePath != "a.go" || vis[1].FilePath != "b.go" {
		t.Errorf("paths = %q/%q, want a.go/b.go", vis[0].FilePath, vis[1].FilePath)
	}
}

func TestFilteredEvents_PureTextTurn_NoHiding(t *testing.T) {
	events := []TurnEvent{{Kind: TurnEventText, Text: "lone", BlockCount: 1}}
	vis, _, hidden := FilteredEvents(events, true)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0 for pure-text turn", hidden)
	}
	if len(vis) != 1 {
		t.Errorf("len(vis) = %d, want 1", len(vis))
	}
}

func TestFilteredEvents_TrailingTextAfterTool_Kept(t *testing.T) {
	events := []TurnEvent{
		{Kind: TurnEventText, BlockCount: 1},
		{Kind: TurnEventEdit, FilePath: "a.go", BlockCount: 1},
		{Kind: TurnEventText, Text: "trailing", BlockCount: 1},
	}
	vis, _, hidden := FilteredEvents(events, true)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0 (trailing text has no tool after it)", hidden)
	}
	if len(vis) != 3 {
		t.Errorf("len(vis) = %d, want 3", len(vis))
	}
}

// ── appendFileEvent + isFileToolEvent ─────────────────────────────────────

func TestAppendFileEvent_MergesSameFile(t *testing.T) {
	var events []TurnEvent
	appendFileEvent(&events, TurnEvent{Kind: TurnEventEdit, FilePath: "a.go", Added: 1, BlockCount: 1})
	appendFileEvent(&events, TurnEvent{Kind: TurnEventEdit, FilePath: "a.go", Added: 2, BlockCount: 1})
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].BlockCount != 2 || events[0].Added != 3 {
		t.Errorf("merged event = %+v, want BlockCount=2 Added=3", events[0])
	}
}

func TestAppendFileEvent_NoMergeDifferentFile(t *testing.T) {
	var events []TurnEvent
	appendFileEvent(&events, TurnEvent{Kind: TurnEventEdit, FilePath: "a.go", BlockCount: 1})
	appendFileEvent(&events, TurnEvent{Kind: TurnEventEdit, FilePath: "b.go", BlockCount: 1})
	if len(events) != 2 {
		t.Errorf("len(events) = %d, want 2 (different files)", len(events))
	}
}

func TestIsFileToolEvent(t *testing.T) {
	for _, k := range []TurnEventKind{TurnEventEdit, TurnEventWrite, TurnEventMultiEdit} {
		if !isFileToolEvent(TurnEvent{Kind: k}) {
			t.Errorf("isFileToolEvent(%q) = false, want true", k)
		}
	}
	if isFileToolEvent(TurnEvent{Kind: TurnEventText}) {
		t.Error("isFileToolEvent(text) = true, want false")
	}
}
