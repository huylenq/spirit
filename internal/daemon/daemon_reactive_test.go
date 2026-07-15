package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/ledger"
)

// newReactiveDaemon builds an ingest-capable daemon with one subscribed client
// (TUI-active) and a fake-Hermes-backed ACP client whose fork prompts answer
// with a canned recommendation.
func newReactiveDaemon(t *testing.T, f *fakeHermes) (*Daemon, *subscriber) {
	t.Helper()
	d := newIngestDaemon(t)
	d.clientCount = 1
	sub := d.addSubscriber("client-1")
	t.Cleanup(func() { d.removeSubscriber(sub) })
	if f != nil {
		d.acpClient = newFakeClient(t, f)
		f.start()
	}
	return d, sub
}

func mustWatch(t *testing.T, d *Daemon, w ledger.Watch) *ledger.Watch {
	t.Helper()
	if w.ExpiresAt.IsZero() {
		w.ExpiresAt = time.Now().Add(time.Hour)
	}
	if w.CooldownSeconds == 0 {
		w.CooldownSeconds = 1
	}
	if w.MaxFirings == 0 {
		w.MaxFirings = 10
	}
	created, err := d.perception.CreateWatch(w)
	if err != nil {
		t.Fatalf("CreateWatch: %v", err)
	}
	return created
}

// drainAttention collects "attention" chunks currently queued for a subscriber.
func drainAttention(sub *subscriber) []CopilotStreamData {
	var out []CopilotStreamData
	for {
		select {
		case evt := <-sub.copilot:
			if evt.Type == "attention" {
				out = append(out, evt)
			}
		default:
			return out
		}
	}
}

// waitWatchState polls until the watch reaches a state (reactive runs are async).
func waitWatchState(t *testing.T, d *Daemon, id string, want ledger.WatchState) ledger.Watch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w, ok := d.perception.WatchByID(id); ok && w.State == want {
			return w
		}
		time.Sleep(5 * time.Millisecond)
	}
	w, _ := d.perception.WatchByID(id)
	t.Fatalf("watch %s state = %s, want %s", id, w.State, want)
	return w
}

func auditKinds(it ledger.AttentionItem) []string {
	kinds := make([]string, 0, len(it.Audit))
	for _, ev := range it.Audit {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func hasAudit(it ledger.AttentionItem, kind, detailSub string) bool {
	for _, ev := range it.Audit {
		if ev.Kind == kind && strings.Contains(ev.Detail, detailSub) {
			return true
		}
	}
	return false
}

func TestReactiveInboxWatchAuditsWithoutInterruption(t *testing.T) {
	d, sub := newReactiveDaemon(t, nil)
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseInbox,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", map[string]any{"claim": "did the thing"}, "")
	d.reactiveTick()

	w, _ := d.perception.WatchByID(created.ID)
	if w.State != ledger.WatchActive || w.Firings != 1 {
		t.Fatalf("watch = %+v", w)
	}
	items := d.perception.UnresolvedItems()
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if !hasAudit(items[0], ledger.AuditWatchTriggered, "matched") ||
		!hasAudit(items[0], ledger.AuditPolicyDecision, "observe") ||
		!hasAudit(items[0], ledger.AuditDelivery, "inbox row") {
		t.Fatalf("audit = %v", items[0].Audit)
	}
	// observe level: no user-facing interruption.
	if evts := drainAttention(sub); len(evts) != 0 {
		t.Fatalf("inbox watch emitted notifications: %+v", evts)
	}
	// Subsequent idle ticks do not re-fire.
	d.reactiveTick()
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.Firings != 1 {
		t.Fatalf("re-fired on idle ticks: %+v", w)
	}
}

func TestReactiveNotifyImmediateForHighSalience(t *testing.T) {
	d, sub := newReactiveDaemon(t, nil)
	mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionWaiting, Response: ledger.ResponseNotify,
	})

	// waiting_input with a permission kind → needs_decision (high salience).
	d.perception.Ingest(ledger.SignalWaitingInput, "s1/w1", "s1", "p",
		map[string]any{"waiting_kind": "permission_prompt"}, "")
	d.reactiveTick()

	evts := drainAttention(sub)
	if len(evts) != 1 || evts[0].Kind != "notify" {
		t.Fatalf("attention events = %+v", evts)
	}
	if !strings.Contains(evts[0].Content, "waiting") {
		t.Fatalf("notify content = %q", evts[0].Content)
	}
}

func TestReactiveNotifyLowSalienceBatchesIntoDigest(t *testing.T) {
	oldAge := digestFlushAge
	digestFlushAge = 0 // flush on the next tick
	defer func() { digestFlushAge = oldAge }()

	d, sub := newReactiveDaemon(t, nil)
	// Two notify watches on distinct sessions, both firing in the same tick:
	// their low-salience firings must coalesce into ONE digest notification.
	mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseNotify,
	})
	mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s2"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseNotify,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.perception.Ingest(ledger.SignalTurnCompleted, "s2/t1", "s2", "p", nil, "")
	d.reactiveTick() // both fire, both batch, batch flushes (age 0)

	evts := drainAttention(sub)
	var digest *CopilotStreamData
	for i := range evts {
		if evts[i].Kind == "digest" {
			if digest != nil {
				t.Fatalf("more than one digest: %+v", evts)
			}
			digest = &evts[i]
		}
		if evts[i].Kind == "notify" {
			t.Fatalf("low-salience firing notified immediately: %+v", evts[i])
		}
	}
	if digest == nil {
		t.Fatalf("no digest flushed: %+v", evts)
	}
	if !strings.Contains(digest.Content, "2 watched event(s)") {
		t.Fatalf("digest content = %q", digest.Content)
	}
}

func TestReactiveImmediateNotifyThrottled(t *testing.T) {
	d, sub := newReactiveDaemon(t, nil)
	mustWatch(t, d, ledger.Watch{
		Condition: ledger.ConditionWaiting, Response: ledger.ResponseNotify,
	})

	d.perception.Ingest(ledger.SignalWaitingInput, "s1/w1", "s1", "p",
		map[string]any{"waiting_kind": "permission_prompt"}, "")
	d.reactiveTick()
	time.Sleep(1100 * time.Millisecond) // cooldown, but inside the notify throttle
	d.perception.Ingest(ledger.SignalWaitingInput, "s2/w1", "s2", "p",
		map[string]any{"waiting_kind": "permission_prompt"}, "")
	d.reactiveTick()

	immediate := 0
	for _, evt := range drainAttention(sub) {
		if evt.Kind == "notify" {
			immediate++
		}
	}
	if immediate != 1 {
		t.Fatalf("immediate notifications = %d, want exactly 1 (throttled)", immediate)
	}
}

func TestReactiveRecommendHappyPath(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID == "main-1" {
			f.reply(id, map[string]any{"stopReason": "end_turn"})
			return
		}
		if !strings.Contains(text, "reactive-attention-run") || !strings.Contains(text, "NO tools") {
			t.Errorf("fork prompt missing framing: %q", text)
		}
		f.textDelta(sessionID, "Verify: the claim looks complete; run the test suite before merging.")
		f.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	d, sub := newReactiveDaemon(t, f)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusUserTurn}}
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseRecommend,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", map[string]any{"claim": "fixed the tests"}, "")
	d.reactiveTick()

	w := waitWatchState(t, d, created.ID, ledger.WatchActive)
	if w.Firings != 1 || w.LLMUsed != 1 || w.LastOutcome != "recommended" {
		t.Fatalf("watch after run = %+v", w)
	}
	items := d.perception.UnresolvedItems()
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	it := items[0]
	if !strings.Contains(it.Recommendation, "Verify") {
		t.Fatalf("recommendation = %q", it.Recommendation)
	}
	if !hasAudit(it, ledger.AuditLLMRun, "fork fork-1") || !hasAudit(it, ledger.AuditDelivery, "recommendation") {
		t.Fatalf("audit = %v (kinds %v)", it.Audit, auditKinds(it))
	}
	recEvents := 0
	for _, evt := range drainAttention(sub) {
		if evt.Kind == "recommendation" {
			recEvents++
		}
	}
	if recEvents != 1 {
		t.Fatalf("recommendation events = %d, want exactly 1", recEvents)
	}

	// Idle ticks after the run: no duplicates of anything.
	d.reactiveTick()
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.Firings != 1 || w.LLMUsed != 1 {
		t.Fatalf("idle ticks re-ran the watch: %+v", w)
	}
	if evts := drainAttention(sub); len(evts) != 0 {
		t.Fatalf("idle ticks emitted: %+v", evts)
	}
}

func TestReactiveNoProcessingWithoutClients(t *testing.T) {
	d, sub := newReactiveDaemon(t, nil)
	// No TUI subscriber attached: the §0 gate keys on the subscriber set, not
	// clientCount, so removing the subscriber closes the reactive gate even
	// though an RPC connection (clientCount) may be open.
	d.removeSubscriber(sub)
	d.clientCount = 1 // an eval-shaped RPC connection is open — must NOT enable
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseInbox,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	w, _ := d.perception.WatchByID(created.ID)
	if w.State != ledger.WatchTriggered {
		t.Fatalf("state = %s, want triggered (unprocessed while no subscriber)", w.State)
	}

	// A TUI subscriber attaches: the pending trigger is processed.
	d.addSubscriber("client-2")
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.State != ledger.WatchActive || w.Firings != 1 {
		t.Fatalf("after subscriber attach: %+v", w)
	}
}

func TestReactiveRecommendDeferredDuringUserTurn(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID != "main-1" {
			f.textDelta(sessionID, "park it")
		}
		f.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	d, _ := newReactiveDaemon(t, f)
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseRecommend,
	})

	d.setActiveTurn("req-1", "client-1") // user turn streaming
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	if w, _ := d.perception.WatchByID(created.ID); w.State != ledger.WatchTriggered {
		t.Fatalf("state during user turn = %s, want triggered (deferred)", w.State)
	}
	if d.reactiveRunning.Load() {
		t.Fatalf("reactive slot leaked while deferred")
	}

	// Turn ends → next tick runs it.
	d.copilotStateMu.Lock()
	d.copilotActive = nil
	d.copilotStateMu.Unlock()
	d.reactiveTick()
	w := waitWatchState(t, d, created.ID, ledger.WatchActive)
	if w.Firings != 1 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestReactiveForkUnavailableDowngradesToNotify(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", agentCaps: map[string]any{
		"loadSession":         true,
		"sessionCapabilities": map[string]any{"list": map[string]any{}},
	}}
	d, sub := newReactiveDaemon(t, f)
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseRecommend,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	w := waitWatchState(t, d, created.ID, ledger.WatchActive)
	if w.Firings != 1 || !strings.Contains(w.LastOutcome, "fork unavailable") {
		t.Fatalf("watch = %+v", w)
	}
	items := d.perception.UnresolvedItems()
	if len(items) != 1 || items[0].Recommendation != "" {
		t.Fatalf("items = %+v", items)
	}
	if !hasAudit(items[0], ledger.AuditLLMRun, "skipped") {
		t.Fatalf("audit = %v", items[0].Audit)
	}
	// Downgrade delivered something (digest batch for low salience).
	d.reactiveMu.Lock()
	batched := len(d.digestLines)
	d.reactiveMu.Unlock()
	if batched == 0 && len(drainAttention(sub)) == 0 {
		t.Fatalf("downgraded firing delivered nothing")
	}
}

func TestReactiveLLMBudgetExhaustionDowngrades(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID != "main-1" {
			f.textDelta(sessionID, "verify it")
		}
		f.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	d, _ := newReactiveDaemon(t, f)
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn,
		Response: ledger.ResponseRecommend, LLMBudget: 1,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	waitWatchState(t, d, created.ID, ledger.WatchActive)

	time.Sleep(1100 * time.Millisecond) // cooldown
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t2", "s1", "p", nil, "")
	d.reactiveTick()
	w := waitWatchState(t, d, created.ID, ledger.WatchActive)
	if w.LLMUsed != 1 {
		t.Fatalf("llm_used = %d, want 1 (budget respected)", w.LLMUsed)
	}
	if w.Firings != 2 || !strings.Contains(w.LastOutcome, "budget exhausted") {
		t.Fatalf("watch = %+v", w)
	}
}

func TestReactiveFailedRunBacksOffWithoutRetry(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID == "main-1" {
			f.reply(id, map[string]any{"stopReason": "end_turn"})
			return
		}
		f.replyErr(id, -32000, "model unavailable")
	}
	d, sub := newReactiveDaemon(t, f)
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseRecommend,
	})

	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	w := waitWatchState(t, d, created.ID, ledger.WatchActive)
	if w.Failures != 1 || w.NextEligibleAt.IsZero() || w.Firings != 0 {
		t.Fatalf("watch after failed run = %+v", w)
	}
	// No autonomous retry: further idle ticks change nothing.
	d.reactiveTick()
	d.reactiveTick()
	if w2, _ := d.perception.WatchByID(created.ID); w2.LLMUsed != 1 {
		t.Fatalf("retried after failure: %+v", w2)
	}
	if evts := drainAttention(sub); len(evts) != 0 {
		t.Fatalf("failed run emitted: %+v", evts)
	}
}
