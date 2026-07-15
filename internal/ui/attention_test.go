package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func inboxFixture() *AttentionModel {
	m := &AttentionModel{}
	m.SetData(
		[]AttentionItemRow{
			{
				ID: "item-1", Category: "verify_claim", Severity: "attend", Status: "open",
				SessionLabel: "spirit-fix", Description: "turn completed: fixed the tests",
				Recommendation: "Verify: run the suite before merging.",
				AuditSummary:   "watch_triggered → policy_decision → llm_run → delivery",
			},
			{
				ID: "item-2", Category: "needs_decision", Severity: "urgent", Status: "delivered",
				SessionLabel: "odb-publish", Description: "waiting: permission_prompt",
			},
		},
		[]WatchRow{
			{ID: "w1", ScopeLabel: "spirit-fix", Condition: "completed_turn", Response: "inspect_and_recommend", State: "active", Firings: "1/20", Outcome: "recommended"},
			{ID: "w2", ScopeLabel: "fleet", Condition: "waiting", Response: "notify", State: "expired", Firings: "20/20", Outcome: "expired"},
		},
	)
	return m
}

func TestAttentionViewRendersItemsAndWatches(t *testing.T) {
	m := inboxFixture()
	view := ansi.Strip(m.View(120, 40))

	for _, want := range []string{
		"Attention",
		"Items (2 unresolved)",
		"verify_claim",
		"turn completed: fixed the tests",
		"↳ Verify: run the suite before merging.",
		"watch_triggered → policy_decision → llm_run → delivery",
		"needs_decision",
		"(seen by lulu)",
		"Watches (2)",
		"completed_turn → inspect_and_recommend on spirit-fix",
		"1/20",
		"waiting → notify on fleet",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

func TestAttentionViewEmptyStates(t *testing.T) {
	m := &AttentionModel{}
	m.SetData(nil, nil)
	view := ansi.Strip(m.View(100, 30))
	if !strings.Contains(view, "nothing needs you") {
		t.Errorf("missing empty-items line:\n%s", view)
	}
	if !strings.Contains(view, "gw or /watch") {
		t.Errorf("missing empty-watches hint:\n%s", view)
	}
}

func TestAttentionCursorAndSelection(t *testing.T) {
	m := inboxFixture()

	if it, ok := m.SelectedItem(); !ok || it.ID != "item-1" {
		t.Fatalf("initial selection = %+v ok=%v", it, ok)
	}
	if _, ok := m.SelectedWatch(); ok {
		t.Fatalf("watch selected while cursor on items")
	}

	m.MoveCursor(2) // onto first watch
	if _, ok := m.SelectedItem(); ok {
		t.Fatalf("item selected while cursor on watches")
	}
	if w, ok := m.SelectedWatch(); !ok || w.ID != "w1" {
		t.Fatalf("watch selection = %+v ok=%v", w, ok)
	}

	m.MoveCursor(10) // clamp at end
	if w, ok := m.SelectedWatch(); !ok || w.ID != "w2" {
		t.Fatalf("clamped selection = %+v ok=%v", w, ok)
	}
	m.MoveCursor(-10) // clamp at start
	if it, ok := m.SelectedItem(); !ok || it.ID != "item-1" {
		t.Fatalf("clamped-to-start selection = %+v ok=%v", it, ok)
	}
}

func TestAttentionViewError(t *testing.T) {
	m := &AttentionModel{}
	m.SetError("daemon unreachable")
	view := ansi.Strip(m.View(100, 30))
	if !strings.Contains(view, "error: daemon unreachable") {
		t.Errorf("missing error line:\n%s", view)
	}
}

func TestWatchPickerView(t *testing.T) {
	view := ansi.Strip(WatchPickerView("watch: condition?", "[t] completed turn"))
	if !strings.Contains(view, "watch: condition?") || !strings.Contains(view, "[t] completed turn") {
		t.Errorf("picker view = %s", view)
	}
}
