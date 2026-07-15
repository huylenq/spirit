package daemon

import (
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/ledger"
)

func TestComposeDailyDigest(t *testing.T) {
	cap := &osCapture{}
	d, sub := newReactiveDaemon(t, nil)
	d.notifyOS = cap.fn()
	d.reactiveClock = func() time.Time { return time.Date(2026, 7, 15, 9, 0, 0, 0, time.Local) }

	// No items → no-op, no notification.
	if n := d.composeDailyDigest(); n != 0 {
		t.Fatalf("empty digest returned %d", n)
	}
	if cap.count() != 0 || len(drainAttention(sub)) != 0 {
		t.Fatal("empty digest delivered something")
	}

	// Two unresolved items → one digest event + one OS notify.
	d.perception.Ingest(ledger.SignalWaitingInput, "s1/w1", "s1", "p", map[string]any{"waiting_kind": "permission_prompt"}, "")
	d.perception.Ingest(ledger.SignalTurnCompleted, "s2/t1", "s2", "p", nil, "")
	n := d.composeDailyDigest()
	if n != 2 {
		t.Fatalf("digest summarized %d items, want 2", n)
	}
	if cap.count() != 1 {
		t.Fatalf("digest OS notifications = %d, want 1", cap.count())
	}
	evts := drainAttention(sub)
	if len(evts) != 1 || evts[0].Kind != "digest" {
		t.Fatalf("digest stream = %+v", evts)
	}
}

func TestComposeDailyDigestQuietHoursNoOSNotify(t *testing.T) {
	cap := &osCapture{}
	d, _ := newReactiveDaemon(t, nil)
	d.notifyOS = cap.fn()
	d.reactiveClock = func() time.Time { return time.Date(2026, 7, 15, 3, 0, 0, 0, time.Local) }
	claudeWritePref(t, "reactive.quiet_hours", "22:00-07:00")

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	if n := d.composeDailyDigest(); n != 1 {
		t.Fatalf("digest items = %d", n)
	}
	if cap.count() != 0 {
		t.Fatalf("digest OS-notified during quiet hours (%d)", cap.count())
	}
}

// TestLaterWakeFiresHeadlessUnderLease proves a delayed reminder (Later wake)
// ingests with NO subscriber attached — durable reactivity only needs to keep
// the daemon awake (the lease); the signal itself is a deterministic daemon
// mechanic, independent of any client.
func TestLaterWakeFiresHeadlessUnderLease(t *testing.T) {
	d := newIngestDaemon(t)
	d.acquireReactiveLease() // headless: leased, zero subscribers

	past := time.Now().Add(-time.Minute)
	old := []agent.Session{{
		SessionID: "s1", PaneID: "%1", Project: "p",
		LaterID: "later-1", LaterWakeAt: &past, IsPhantom: true,
	}}
	// The parked session's pane is gone at wake time (Later removal = a wake).
	d.observeFleet(old, nil)

	if got := signalCount(d, ledger.SignalLaterWoke); got != 1 {
		t.Fatalf("later_woke signals = %d, want 1 (headless under lease)", got)
	}
}
