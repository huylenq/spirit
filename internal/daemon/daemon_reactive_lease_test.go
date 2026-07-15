package daemon

import (
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/ledger"
)

// TestReactiveEvalConnectionDoesNotEnable asserts the §0 gate correction: an
// RPC-only (eval-shaped) connection bumps clientCount but is not a subscriber,
// so it must NOT enable reactive processing.
func TestReactiveEvalConnectionDoesNotEnable(t *testing.T) {
	d := newIngestDaemon(t)
	d.clientCount = 1 // an eval holds one RPC connection for its whole script
	if d.reactiveEligible() {
		t.Fatal("eval-shaped RPC connection enabled reactive processing")
	}
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseInbox,
	})
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.State != ledger.WatchTriggered {
		t.Fatalf("state = %s, want triggered (eval connection must not process)", w.State)
	}
}

// TestReactiveLeaseEnablesProcessing asserts a durable lease enables processing
// with zero subscribers, and that dropping it restores idle-exit eligibility.
func TestReactiveLeaseEnablesProcessing(t *testing.T) {
	d := newIngestDaemon(t)
	if d.reactiveEligible() {
		t.Fatal("eligible with no subscriber and no lease")
	}
	d.acquireReactiveLease()
	if !d.reactiveEligible() {
		t.Fatal("lease did not enable reactive processing")
	}
	if !d.durableReactive {
		t.Fatal("durableReactive not set under lease")
	}

	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseInbox,
	})
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.State != ledger.WatchActive || w.Firings != 1 {
		t.Fatalf("lease did not process the pending watch: %+v", w)
	}

	// Lease drop clears the flag → idle-exit becomes possible again.
	d.releaseReactiveLease()
	if d.durableReactive {
		t.Fatal("durableReactive still set after lease drop")
	}
	if d.reactiveEligible() {
		t.Fatal("still eligible after lease drop with no subscriber")
	}
}

// TestReactiveLeaseRefcount asserts two leases must both drop before autonomy ends.
func TestReactiveLeaseRefcount(t *testing.T) {
	d := newIngestDaemon(t)
	d.acquireReactiveLease()
	d.acquireReactiveLease()
	d.releaseReactiveLease()
	if !d.durableReactive {
		t.Fatal("durableReactive cleared while a second lease is still held")
	}
	d.releaseReactiveLease()
	if d.durableReactive {
		t.Fatal("durableReactive still set after both leases dropped")
	}
}

// TestReactivePausedDispatchesNothing asserts a paused tick sweeps housekeeping
// but claims/dispatches nothing, and that resume processes the pending trigger.
func TestReactivePausedDispatchesNothing(t *testing.T) {
	d := newIngestDaemon(t)
	d.acquireReactiveLease()
	d.mu.Lock()
	d.reactivePaused = true
	d.mu.Unlock()

	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseInbox,
	})
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick() // paused: must not claim
	if w, _ := d.perception.WatchByID(created.ID); w.State != ledger.WatchTriggered {
		t.Fatalf("paused tick processed a watch: %+v", w)
	}

	// Resume → next tick processes it (deferred, not dropped).
	d.mu.Lock()
	d.reactivePaused = false
	d.mu.Unlock()
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.State != ledger.WatchActive || w.Firings != 1 {
		t.Fatalf("resume did not process the deferred watch: %+v", w)
	}
}

// TestReactiveControlTogglesPause exercises the control handler + status report.
func TestReactiveControlTogglesPause(t *testing.T) {
	d := newIngestDaemon(t)
	d.acquireReactiveLease()

	st := d.reactiveStatus()
	if !st.Leased || !st.DurableReactive || st.GateReason != "durable" {
		t.Fatalf("status under lease = %+v", st)
	}
	if st.Paused {
		t.Fatal("fresh lease reports paused")
	}

	if resp := d.handleReactiveControl(marshalData(ReactiveControlData{Action: "pause"})); resp.Error != "" {
		t.Fatalf("pause: %s", resp.Error)
	}
	if !d.reactiveStatus().Paused {
		t.Fatal("status not paused after control pause")
	}
	if resp := d.handleReactiveControl(marshalData(ReactiveControlData{Action: "resume"})); resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}
	if d.reactiveStatus().Paused {
		t.Fatal("status still paused after control resume")
	}
	if resp := d.handleReactiveControl(marshalData(ReactiveControlData{Action: "bogus"})); resp.Error == "" {
		t.Fatal("unknown control action was not rejected")
	}
}

// TestReactiveStatusGateReasonSubscriber asserts a real subscriber is reported
// as the gate reason (not the lease) when both would apply.
func TestReactiveStatusGateReasonSubscriber(t *testing.T) {
	d := newIngestDaemon(t)
	sub := d.addSubscriber("client-1")
	defer d.removeSubscriber(sub)
	st := d.reactiveStatus()
	if st.Subscribers != 1 || st.GateReason != "subscriber" {
		t.Fatalf("status with subscriber = %+v", st)
	}
}

// TestQuietHoursParser exercises the window parser incl. midnight wrap.
func TestQuietHoursParser(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 7, 15, h, m, 0, 0, time.Local) }
	cases := []struct {
		spec   string
		when   time.Time
		active bool
	}{
		{"", at(3, 0), false},
		{"garbage", at(3, 0), false},
		{"22:00-07:00", at(23, 30), true}, // wrap, before midnight
		{"22:00-07:00", at(3, 0), true},   // wrap, after midnight
		{"22:00-07:00", at(12, 0), false}, // outside wrap
		{"09:00-17:00", at(12, 0), true},  // same-day window
		{"09:00-17:00", at(8, 59), false}, // before start
		{"09:00-17:00", at(17, 0), false}, // end is exclusive
		{"09:00-09:00", at(9, 0), false},  // empty window
	}
	for _, c := range cases {
		if got := parseQuietHours(c.spec).active(c.when); got != c.active {
			t.Errorf("parseQuietHours(%q).active(%v) = %v, want %v", c.spec, c.when.Format("15:04"), got, c.active)
		}
	}
}
