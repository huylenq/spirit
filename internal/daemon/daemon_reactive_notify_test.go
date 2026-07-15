package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/ledger"
)

// osCapture records desktop-notification calls without spawning processes.
type osCapture struct {
	mu    sync.Mutex
	calls []string
}

func (c *osCapture) fn() func(title, body string) {
	return func(title, body string) {
		c.mu.Lock()
		c.calls = append(c.calls, title+": "+body)
		c.mu.Unlock()
	}
}
func (c *osCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func TestReactiveHighSalienceOSNotify(t *testing.T) {
	cap := &osCapture{}
	d, sub := newReactiveDaemon(t, nil)
	d.notifyOS = cap.fn()
	// Noon, no quiet hours configured.
	d.reactiveClock = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local) }

	mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionWaiting, Response: ledger.ResponseNotify,
	})
	d.perception.Ingest(ledger.SignalWaitingInput, "s1/w1", "s1", "p",
		map[string]any{"waiting_kind": "permission_prompt"}, "")
	d.reactiveTick()

	if cap.count() != 1 {
		t.Fatalf("OS notifications = %d, want exactly 1 (high salience)", cap.count())
	}
	// In-app stream still delivered.
	if got := drainAttention(sub); len(got) != 1 || got[0].Kind != "notify" {
		t.Fatalf("in-app stream = %+v", got)
	}
}

func TestReactiveQuietHoursSuppressesOSNotifyNotDurableItem(t *testing.T) {
	cap := &osCapture{}
	d, sub := newReactiveDaemon(t, nil)
	d.notifyOS = cap.fn()
	// 03:00, inside a 22:00-07:00 quiet window.
	d.reactiveClock = func() time.Time { return time.Date(2026, 7, 15, 3, 0, 0, 0, time.Local) }
	claudeWritePref(t, "reactive.quiet_hours", "22:00-07:00")

	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionWaiting, Response: ledger.ResponseNotify,
	})
	d.perception.Ingest(ledger.SignalWaitingInput, "s1/w1", "s1", "p",
		map[string]any{"waiting_kind": "permission_prompt"}, "")
	d.reactiveTick()

	if cap.count() != 0 {
		t.Fatalf("OS notification fired during quiet hours (%d)", cap.count())
	}
	// The durable item + audit are still recorded; the firing completed.
	if w, _ := d.perception.WatchByID(created.ID); w.Firings != 1 {
		t.Fatalf("firing not recorded during quiet hours: %+v", w)
	}
	items := d.perception.UnresolvedItems()
	if len(items) != 1 {
		t.Fatalf("durable item missing during quiet hours: %d", len(items))
	}
	if !hasAudit(items[0], ledger.AuditDelivery, "suppressed (quiet hours)") {
		t.Fatalf("missing quiet-hours suppression audit: %v", items[0].Audit)
	}
	// The in-app stream event is NOT an OS notification and is still delivered.
	if got := drainAttention(sub); len(got) != 1 {
		t.Fatalf("in-app stream during quiet hours = %+v", got)
	}
}

func TestReactiveDigestNeverOSPushed(t *testing.T) {
	oldAge := digestFlushAge
	digestFlushAge = 0
	defer func() { digestFlushAge = oldAge }()

	cap := &osCapture{}
	d, _ := newReactiveDaemon(t, nil)
	d.notifyOS = cap.fn()
	d.reactiveClock = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local) }

	// Two low-salience (completed_turn) notify firings → one digest, no OS push.
	mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseNotify,
	})
	mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s2"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseNotify,
	})
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.perception.Ingest(ledger.SignalTurnCompleted, "s2/t1", "s2", "p", nil, "")
	d.reactiveTick()

	if cap.count() != 0 {
		t.Fatalf("digest-class firing OS-pushed (%d)", cap.count())
	}
}
