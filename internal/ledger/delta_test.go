package ledger

import (
	"fmt"
	"strings"
	"testing"
)

func TestConsumeDeltaCursorSemantics(t *testing.T) {
	l := openTest(t)

	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "spirit", map[string]any{"claim": "refactored the poller"}, "")
	l.Ingest(SignalWaitingInput, "s2/w1", "s2", "lifeos", map[string]any{"waiting_kind": "permission_prompt"}, "")

	// First turn of a fresh conversation: snapshot delivered.
	block, ok := l.ConsumeDelta("hermes-A", "req-1")
	if !ok {
		t.Fatalf("expected delta on first consume")
	}
	if !strings.Contains(block, "refactored the poller") || !strings.Contains(block, "permission_prompt") {
		t.Fatalf("delta missing content:\n%s", block)
	}
	if !strings.Contains(block, "<away-delta") || !strings.Contains(block, "</away-delta>") {
		t.Fatalf("delta not tagged:\n%s", block)
	}

	// Items are stamped with the delivering request id.
	for _, it := range l.items {
		if it.Status != StatusDelivered {
			t.Fatalf("item not delivered: %+v", it)
		}
		if len(it.Deliveries) != 1 || it.Deliveries[0].RequestID != "req-1" {
			t.Fatalf("delivery stamp wrong: %+v", it.Deliveries)
		}
	}

	// Second turn with nothing new: no re-delivery.
	if _, ok := l.ConsumeDelta("hermes-A", "req-2"); ok {
		t.Fatalf("re-delivered an already-consumed delta")
	}

	// New fact between turns → next turn sees exactly the new item.
	l.Ingest(SignalTurnCompleted, "s1/t2", "s1", "spirit", map[string]any{"claim": "second turn"}, "")
	block, ok = l.ConsumeDelta("hermes-A", "req-3")
	if !ok || !strings.Contains(block, "second turn") {
		t.Fatalf("new signal not delivered:\n%s", block)
	}
	if strings.Contains(block, "permission_prompt") {
		t.Fatalf("old delivered item re-rendered:\n%s", block)
	}
}

func TestConsumeDeltaFreshSessionSnapshot(t *testing.T) {
	l := openTest(t)

	l.Ingest(SignalWaitingInput, "s1/w1", "s1", "p", map[string]any{"waiting_kind": "permission_prompt"}, "")
	if _, ok := l.ConsumeDelta("hermes-A", "req-1"); !ok {
		t.Fatalf("expected first delta")
	}

	// /new → fresh Hermes UUID → the still-unresolved (delivered) item is
	// snapshotted for the new conversation, which has no memory of it.
	block, ok := l.ConsumeDelta("hermes-B", "req-9")
	if !ok || !strings.Contains(block, "permission_prompt") {
		t.Fatalf("fresh session did not get open-item snapshot:\n%s", block)
	}
	if !strings.Contains(block, `snapshot="true"`) {
		t.Fatalf("fresh-session block not marked as snapshot:\n%s", block)
	}

	// Resolved items never appear for a fresh session.
	l.ResolveSessionItems("s1", []Category{CategoryNeedsDecision}, "waiting ended")
	if _, ok := l.ConsumeDelta("hermes-C", "req-10"); ok {
		t.Fatalf("resolved item delivered to fresh session")
	}
}

func TestConsumeDeltaCapsAndGrouping(t *testing.T) {
	l := openTest(t)

	// 14 verify_claim items (distinct sessions so they don't coalesce) and one
	// urgent needs_decision that must render first despite arriving last.
	for i := 0; i < 14; i++ {
		sid := fmt.Sprintf("s%02d", i)
		l.Ingest(SignalTurnCompleted, sid+"/t1", sid, "p", map[string]any{"claim": "claim " + sid}, "")
	}
	l.Ingest(SignalWaitingInput, "sZ/w1", "sZ", "p", map[string]any{"waiting_kind": "permission_prompt"}, "")

	block, ok := l.ConsumeDelta("hermes-A", "req-1")
	if !ok {
		t.Fatalf("expected delta")
	}
	if got := strings.Count(block, "\n- "); got != maxDetailedItems {
		t.Fatalf("detailed lines = %d, want %d:\n%s", got, maxDetailedItems, block)
	}
	if !strings.Contains(block, "+5 more verify_claim") {
		t.Fatalf("remainder not counted:\n%s", block)
	}
	// Severity order: the urgent decision renders before any verify_claim.
	if di, vi := strings.Index(block, "needs_decision:"), strings.Index(block, "verify_claim:"); di < 0 || vi < 0 || di > vi {
		t.Fatalf("needs_decision not rendered before verify_claim:\n%s", block)
	}
	// The counted remainder still counts as delivered.
	for _, it := range l.items {
		if it.Status != StatusDelivered {
			t.Fatalf("remainder item not marked delivered: %+v", it)
		}
	}
}

func TestConsumeDeltaEmptyOwnerAndNilLedger(t *testing.T) {
	l := openTest(t)
	if _, ok := l.ConsumeDelta("", "req-1"); ok {
		t.Fatalf("empty owner must not consume")
	}
	var nilLedger *Ledger
	if _, ok := nilLedger.ConsumeDelta("x", "req-1"); ok {
		t.Fatalf("nil ledger must be a no-op")
	}
	if _, fresh := nilLedger.Ingest(SignalTurnCompleted, "a", "s", "p", nil, ""); fresh {
		t.Fatalf("nil ledger ingest must be a no-op")
	}
}
