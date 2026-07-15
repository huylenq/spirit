package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Durable reactivity (W9, spec Decision 11 milestone 2). The reactive *brain*
// stays in the daemon (daemon_reactive.go); this file is only its
// permission-to-run-headless: a held-open lease that a separately-supervised
// `spirit reactive run` worker holds, plus one-shot pause/resume/status RPCs.
//
// The lease is the structural encoding of "merely owning a watch keeps nothing
// awake; explicitly, visibly, revocably enabling durable reactivity does":
// d.durableReactive is true only while a lease connection is held, and it both
// satisfies the reactive gate (§0) and suppresses the idle timeout (§1). Drop
// the supervisor → the lease drops → autonomy stops and the daemon reverts to
// normal 10-minute idle behavior.

// reactiveEligible reports whether the reactive engine may process triggered
// watches this tick (spec Decision 11, §0 gate correction): a real TUI
// subscriber is attached, OR durable reactivity holds a lease. It intentionally
// does NOT key on clientCount — an RPC-only `spirit eval` bumps clientCount but
// is not a subscriber, and must not drive reactive processing.
func (d *Daemon) reactiveEligible() bool {
	d.subMu.Lock()
	subs := len(d.subscribers)
	d.subMu.Unlock()
	if subs > 0 {
		return true
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.durableReactive
}

// reactivePausedNow reports the runtime pause state.
func (d *Daemon) reactivePausedNow() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.reactivePaused
}

// acquireReactiveLease marks durable reactivity active for one held-open lease
// connection. Refcounted so a stray second lease re-affirms rather than races.
func (d *Daemon) acquireReactiveLease() {
	d.mu.Lock()
	d.reactiveLeases++
	d.durableReactive = true
	// A fresh lease adopts the persisted pause intent so a worker that resumes
	// after `reactive pause` starts paused rather than silently active.
	d.reactivePaused = d.readPref("reactive") == "paused"
	d.mu.Unlock()
}

// releaseReactiveLease clears durable reactivity when the last lease drops.
func (d *Daemon) releaseReactiveLease() {
	d.mu.Lock()
	d.reactiveLeases--
	if d.reactiveLeases <= 0 {
		d.reactiveLeases = 0
		d.durableReactive = false
		d.reactivePaused = false
	}
	d.mu.Unlock()
}

// handleReactiveLease holds the connection open for its whole lifetime, keeping
// d.durableReactive set. It mirrors handleSubscribe's held-open shape: dispatch
// returns nil so handleConn does not write a response, and this blocks until the
// worker exits / crashes / the daemon stops — at which point the lease drops.
func (d *Daemon) handleReactiveLease(conn net.Conn, enc *json.Encoder) {
	d.acquireReactiveLease()
	defer d.releaseReactiveLease()

	// Ack with the current status so the worker knows the lease is held.
	if err := enc.Encode(resultResponse(d.reactiveStatus())); err != nil {
		return
	}

	// Block until the connection drops. The worker never writes on the lease
	// connection (control/status ride separate one-shot RPCs), so a Read that
	// returns is always EOF/error = the lease is gone.
	buf := make([]byte, 1)
	for {
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

// handleReactiveControl applies a one-shot pause/resume to the runtime mirror.
// Enablement/disablement are deliberately NOT actions here — turning autonomy on
// is a human act (launchd + pref), never an RPC verb.
func (d *Daemon) handleReactiveControl(data json.RawMessage) *Response {
	var req ReactiveControlData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	switch req.Action {
	case "pause":
		d.mu.Lock()
		d.reactivePaused = true
		d.mu.Unlock()
	case "resume":
		d.mu.Lock()
		d.reactivePaused = false
		d.mu.Unlock()
	default:
		r := errResponse("unknown reactive control action: " + req.Action)
		return &r
	}
	r := resultResponse(d.reactiveStatus())
	return &r
}

// handleReactiveStatus returns the read-only durable-reactivity report.
func (d *Daemon) handleReactiveStatus() *Response {
	r := resultResponse(d.reactiveStatus())
	return &r
}

// reactiveStatus assembles the durable-reactivity report from runtime flags,
// the persisted pref, quiet-hours config, and the global provider budget.
func (d *Daemon) reactiveStatus() ReactiveStatusData {
	d.subMu.Lock()
	subs := len(d.subscribers)
	d.subMu.Unlock()

	d.mu.RLock()
	durable := d.durableReactive
	paused := d.reactivePaused
	d.mu.RUnlock()

	pref := d.readPref("reactive")
	reason := "none"
	if subs > 0 {
		reason = "subscriber"
	} else if durable {
		reason = "durable"
	}

	quiet := d.readPref("reactive.quiet_hours")
	total, remaining := d.reactiveBudgetSnapshot()

	return ReactiveStatusData{
		Enabled:            pref == "on" || pref == "paused",
		Paused:             paused || pref == "paused",
		Leased:             durable,
		DurableReactive:    durable,
		Subscribers:        subs,
		GateReason:         reason,
		QuietHoursActive:   d.inQuietHours(d.reactiveNow()),
		QuietHours:         quiet,
		LLMBudgetRemaining: remaining,
		LLMBudgetTotal:     total,
	}
}

// reactiveNow is the reactive path's clock (a test seam; overridable so
// quiet-hours and budget-rollover tests can pin the time).
func (d *Daemon) reactiveNow() time.Time {
	if d.reactiveClock != nil {
		return d.reactiveClock()
	}
	return time.Now()
}

// handleReactiveDigest composes and delivers a daily attention digest, driven by
// the Spirit-owned scheduler (W9 §5). It is a pure time-of-day summary of what
// is already in the ledger — it triggers no LLM run and mutates nothing.
func (d *Daemon) handleReactiveDigest() *Response {
	n := d.composeDailyDigest()
	r := resultResponse(ReactiveDigestResultData{Items: n})
	return &r
}

// composeDailyDigest summarizes the current unresolved attention items into one
// coalesced digest event + (outside quiet hours) one OS notification. Returns
// the item count summarized; 0 items is a no-op (no empty digest). This is a
// read of durable state, on the SHARED delivery path — no fleet/repo mutation.
func (d *Daemon) composeDailyDigest() int {
	if d.perception == nil {
		return 0
	}
	items := d.perception.UnresolvedItems()
	if len(items) == 0 {
		return 0
	}
	content := fmt.Sprintf("Daily digest: %d item(s) need attention", len(items))
	d.pushCopilotStream(CopilotStreamData{Type: "attention", Kind: "digest", Content: content})
	if !d.inQuietHours(d.reactiveNow()) {
		d.deliverOSNotification("Spirit — daily digest", content)
	}
	return len(items)
}
