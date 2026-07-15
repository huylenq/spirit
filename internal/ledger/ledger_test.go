package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func TestIngestIdempotent(t *testing.T) {
	l := openTest(t)

	sig1, fresh := l.Ingest(SignalTurnCompleted, "sess-a/uuid-1", "sess-a", "spirit", map[string]any{"claim": "done"}, "")
	if !fresh || sig1 == nil {
		t.Fatalf("first ingest not fresh")
	}
	// The same fact reported again (poll + nudge racing) must dedup.
	for i := 0; i < 300; i++ {
		sig2, fresh2 := l.Ingest(SignalTurnCompleted, "sess-a/uuid-1", "sess-a", "spirit", nil, "")
		if fresh2 {
			t.Fatalf("repeat ingest %d was fresh", i)
		}
		if sig2.ID != sig1.ID {
			t.Fatalf("repeat ingest returned different signal")
		}
	}
	if got := len(l.signals); got != 1 {
		t.Fatalf("signals = %d, want 1", got)
	}
	if got := len(l.items); got != 1 {
		t.Fatalf("items = %d, want 1", got)
	}
}

func TestCoalescingIntoOpenItem(t *testing.T) {
	l := openTest(t)

	l.Ingest(SignalTurnCompleted, "s1/turn-1", "s1", "p", nil, "")
	l.Ingest(SignalTurnCompleted, "s1/turn-2", "s1", "p", nil, "")
	l.Ingest(SignalTurnCompleted, "s1/turn-3", "s1", "p", nil, "")

	if got := len(l.items); got != 1 {
		t.Fatalf("items = %d, want 1 (coalesced)", got)
	}
	if got := len(l.items[0].SignalIDs); got != 3 {
		t.Fatalf("signal links = %d, want 3", got)
	}
	// A different session must not coalesce into the same item.
	l.Ingest(SignalTurnCompleted, "s2/turn-1", "s2", "p", nil, "")
	if got := len(l.items); got != 2 {
		t.Fatalf("items = %d, want 2", got)
	}
}

func TestSupersedeResolvesItems(t *testing.T) {
	l := openTest(t)

	failed, _ := l.Ingest(SignalQueueFailed, "s1/msg-hash", "s1", "p", map[string]any{"error": "pane vanished"}, "")
	if cat := l.items[0].Category; cat != CategoryDeliveryFailure {
		t.Fatalf("category = %s, want delivery_failure", cat)
	}
	l.Ingest(SignalQueueDelivered, "s1/msg-hash", "s1", "p", nil, failed.ID)

	var failItem *AttentionItem
	for _, it := range l.items {
		if it.Category == CategoryDeliveryFailure {
			failItem = it
		}
	}
	if failItem == nil || failItem.Status != StatusResolved {
		t.Fatalf("delivery_failure item not resolved by superseding delivery: %+v", failItem)
	}
	if !strings.Contains(failItem.Resolution, "superseded by") {
		t.Fatalf("resolution = %q", failItem.Resolution)
	}
}

func TestWaitingCategoryAndResolution(t *testing.T) {
	l := openTest(t)

	// Permission wait → needs_decision (urgent).
	l.Ingest(SignalWaitingInput, "s1/wait-1", "s1", "p", map[string]any{"waiting_kind": "permission_prompt"}, "")
	if cat := l.items[0].Category; cat != CategoryNeedsDecision {
		t.Fatalf("category = %s, want needs_decision", cat)
	}
	if sev := l.items[0].Severity; sev != SeverityUrgent {
		t.Fatalf("severity = %s, want urgent", sev)
	}

	// Falling edge resolves it.
	if n := l.ResolveSessionItems("s1", []Category{CategoryNeedsDecision, CategoryBlocked}, "waiting ended"); n != 1 {
		t.Fatalf("resolved %d items, want 1", n)
	}
	if l.items[0].Status != StatusResolved {
		t.Fatalf("item not resolved")
	}

	// Unknown waiting kind → blocked.
	l.Ingest(SignalWaitingInput, "s2/wait-1", "s2", "p", map[string]any{"waiting_kind": "mystery"}, "")
	last := l.items[len(l.items)-1]
	if last.Category != CategoryBlocked {
		t.Fatalf("category = %s, want blocked", last.Category)
	}
}

func TestSessionStartedCreatesNoItem(t *testing.T) {
	l := openTest(t)
	l.Ingest(SignalSessionStarted, "s1", "s1", "p", nil, "")
	if len(l.items) != 0 {
		t.Fatalf("session_started should not create an attention item")
	}
	if len(l.signals) != 1 {
		t.Fatalf("session_started must still be recorded as a signal")
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Ingest(SignalTurnCompleted, "s1/turn-1", "s1", "p", map[string]any{"claim": "built the thing"}, "")
	l.Ingest(SignalWaitingInput, "s2/wait-9", "s2", "p", map[string]any{"waiting_kind": "permission_prompt"}, "")

	l2, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Dedup must survive the restart: the same facts are not fresh.
	if _, fresh := l2.Ingest(SignalTurnCompleted, "s1/turn-1", "s1", "p", nil, ""); fresh {
		t.Fatalf("turn_completed re-signaled after reopen")
	}
	if _, fresh := l2.Ingest(SignalWaitingInput, "s2/wait-9", "s2", "p", nil, ""); fresh {
		t.Fatalf("waiting_input re-signaled after reopen")
	}
	if got := len(l2.items); got != 2 {
		t.Fatalf("items after reopen = %d, want 2", got)
	}
}

func TestCorruptLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Ingest(SignalTurnCompleted, "s1/turn-1", "s1", "p", nil, "")

	// Corrupt the segment: garbage line + truncated JSON + a valid line after.
	seg := l.segmentPath(l.now())
	valid, _ := json.Marshal(Signal{ID: newULID(time.Now()), Kind: SignalSessionEnded, Anchor: "s9", SessionID: "s9", ObservedAt: time.Now()})
	f, err := os.OpenFile(seg, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "not json at all\n{\"id\":\"trunc\n%s\n{}\n", valid)
	f.Close()

	l2, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open must not fail on corrupt lines: %v", err)
	}
	if got := len(l2.signals); got != 2 {
		t.Fatalf("signals = %d, want 2 (valid lines only)", got)
	}
}

func TestExpiry(t *testing.T) {
	l := openTest(t)
	base := time.Now()
	l.now = func() time.Time { return base }
	l.Ingest(SignalTurnCompleted, "s1/turn-1", "s1", "p", nil, "")

	// Fast-forward past the expiry window; any ingest sweeps stale items.
	l.now = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	l.Ingest(SignalTurnCompleted, "s2/turn-1", "s2", "p", nil, "")

	var old *AttentionItem
	for _, it := range l.items {
		if it.Scope.SessionID == "s1" {
			old = it
		}
	}
	if old == nil || old.Status != StatusExpired {
		t.Fatalf("stale item not expired: %+v", old)
	}
}

func TestULIDOrdering(t *testing.T) {
	prev := ""
	base := time.Now()
	for i := 0; i < 1000; i++ {
		id := newULID(base.Add(time.Duration(i) * time.Microsecond))
		if len(id) != 26 {
			t.Fatalf("ulid length = %d", len(id))
		}
		if id <= prev {
			t.Fatalf("ulid not monotonic: %s after %s", id, prev)
		}
		prev = id
	}
}

func TestConcurrentIngest(t *testing.T) {
	l := openTest(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				// Half the anchors collide across goroutines, half are unique.
				anchor := fmt.Sprintf("shared/%d", i)
				if g%2 == 0 {
					anchor = fmt.Sprintf("g%d/%d", g, i)
				}
				l.Ingest(SignalTurnCompleted, anchor, fmt.Sprintf("s%d", g), "p", nil, "")
				l.SignalsToday()
				l.ResolveSessionItems("nobody", []Category{CategoryBlocked}, "noop")
			}
		}(g)
	}
	wg.Wait()

	// 4 goroutines × 50 unique + 50 shared = 250 distinct anchors.
	if got := len(l.signals); got != 250 {
		t.Fatalf("signals = %d, want 250", got)
	}
}

func TestAtomicFilesWritten(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Ingest(SignalWaitingInput, "s1/w1", "s1", "p", nil, "")
	if _, err := os.Stat(filepath.Join(dir, "attention.json")); err != nil {
		t.Fatalf("attention.json missing: %v", err)
	}
	if _, ok := l.ConsumeDelta("hermes-1", "req-1"); !ok {
		t.Fatalf("expected a delta")
	}
	if _, err := os.Stat(filepath.Join(dir, "cursors.json")); err != nil {
		t.Fatalf("cursors.json missing: %v", err)
	}
}
