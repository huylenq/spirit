package ledger

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock installs a controllable clock on a test ledger.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openWatchTest(t *testing.T) (*Ledger, *fakeClock) {
	t.Helper()
	l := openTest(t)
	clock := &fakeClock{t: time.Now()}
	l.now = clock.now
	return l, clock
}

func validWatch(sessionID string) Watch {
	return Watch{
		Scope:           WatchScope{SessionID: sessionID},
		Condition:       ConditionCompletedTurn,
		Response:        ResponseInbox,
		ExpiresAt:       time.Now().Add(24 * time.Hour),
		CooldownSeconds: 60,
		MaxFirings:      5,
	}
}

// --- validation ---

func TestCreateWatchValidation(t *testing.T) {
	l, clock := openWatchTest(t)
	base := validWatch("s1")
	base.ExpiresAt = clock.now().Add(24 * time.Hour)

	cases := []struct {
		name   string
		mutate func(*Watch)
		errHas string
	}{
		{"no expiry", func(w *Watch) { w.ExpiresAt = time.Time{} }, "expires_at"},
		{"past expiry", func(w *Watch) { w.ExpiresAt = clock.now().Add(-time.Hour) }, "expires_at"},
		{"expiry beyond cap", func(w *Watch) { w.ExpiresAt = clock.now().Add(8 * 24 * time.Hour) }, "maximum"},
		{"no cooldown", func(w *Watch) { w.CooldownSeconds = 0 }, "cooldown"},
		{"no max firings", func(w *Watch) { w.MaxFirings = 0 }, "max_firings"},
		{"firings beyond cap", func(w *Watch) { w.MaxFirings = 1000 }, "maximum"},
		{"bad condition", func(w *Watch) { w.Condition = "vibes" }, "condition"},
		{"bad response", func(w *Watch) { w.Response = "act" }, "response"},
	}
	for _, tc := range cases {
		w := base
		tc.mutate(&w)
		if _, err := l.CreateWatch(w); err == nil || !strings.Contains(err.Error(), tc.errHas) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.errHas)
		}
	}

	// LLM budget cap applies to recommend watches only.
	w := base
	w.Response = ResponseRecommend
	w.LLMBudget = 99
	if _, err := l.CreateWatch(w); err == nil || !strings.Contains(err.Error(), "llm_budget") {
		t.Errorf("llm budget cap: err = %v", err)
	}

	// A valid watch creates with defaults derived.
	w = base
	w.Response = ResponseRecommend
	created, err := l.CreateWatch(w)
	if err != nil {
		t.Fatalf("CreateWatch: %v", err)
	}
	if created.State != WatchActive || created.AutonomyLevel != "recommend" || created.LLMBudget != DefaultLLMBudget {
		t.Fatalf("created = %+v", created)
	}
	if created.ID == "" {
		t.Fatalf("no watch id assigned")
	}
}

func TestCreateWatchLiveCap(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s")
	w.ExpiresAt = clock.now().Add(time.Hour)
	for i := 0; i < maxLiveWatches; i++ {
		if _, err := l.CreateWatch(w); err != nil {
			t.Fatalf("watch %d: %v", i, err)
		}
	}
	if _, err := l.CreateWatch(w); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("expected live-cap error, got %v", err)
	}
}

// --- FSM ---

func mustCreate(t *testing.T, l *Ledger, w Watch) *Watch {
	t.Helper()
	created, err := l.CreateWatch(w)
	if err != nil {
		t.Fatalf("CreateWatch: %v", err)
	}
	return created
}

func TestWatchTriggersOnMatchingFreshSignalOnly(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(time.Hour)
	created := mustCreate(t, l, w)

	// Non-matching session: no trigger.
	l.Ingest(SignalTurnCompleted, "s2/t1", "s2", "p", nil, "")
	// Non-matching kind: no trigger.
	l.Ingest(SignalWaitingInput, "s1/w1", "s1", "p", nil, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchActive {
		t.Fatalf("state after non-matching signals = %s", got.State)
	}

	// Matching fresh signal: triggered, pending carries signal + item.
	sig, _ := l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	got, _ := l.WatchByID(created.ID)
	if got.State != WatchTriggered {
		t.Fatalf("state = %s, want triggered", got.State)
	}
	if len(got.Pending.SignalIDs) != 1 || got.Pending.SignalIDs[0] != sig.ID || got.Pending.ItemID == "" {
		t.Fatalf("pending = %+v", got.Pending)
	}
	item, ok := l.ItemByID(got.Pending.ItemID)
	if !ok || len(item.Audit) == 0 || item.Audit[0].Kind != AuditWatchTriggered || item.Audit[0].WatchID != created.ID {
		t.Fatalf("item audit = %+v", item.Audit)
	}

	// A DUPLICATE of the same fact (same anchor) is not fresh — no state churn.
	before, _ := l.WatchByID(created.ID)
	for i := 0; i < 100; i++ {
		l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	}
	after, _ := l.WatchByID(created.ID)
	if len(after.Pending.SignalIDs) != len(before.Pending.SignalIDs) {
		t.Fatalf("idle re-ingest grew pending: %+v", after.Pending)
	}

	// A second fresh matching signal coalesces into the pending firing.
	l.Ingest(SignalTurnCompleted, "s1/t2", "s1", "p", nil, "")
	got, _ = l.WatchByID(created.ID)
	if got.State != WatchTriggered || len(got.Pending.SignalIDs) != 2 {
		t.Fatalf("after coalesce: state=%s pending=%+v", got.State, got.Pending)
	}
}

func TestWatchFiringLifecycle(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(time.Hour)
	w.MaxFirings = 2
	created := mustCreate(t, l, w)

	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")

	claimed := l.ClaimTriggered(0)
	if len(claimed) != 1 || claimed[0].ID != created.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	if got, _ := l.WatchByID(created.ID); got.State != WatchProcessing {
		t.Fatalf("state = %s, want processing", got.State)
	}
	// Claiming again returns nothing (single claim).
	if again := l.ClaimTriggered(0); len(again) != 0 {
		t.Fatalf("double claim: %+v", again)
	}

	l.CompleteFiring(created.ID, "inboxed")
	got, _ := l.WatchByID(created.ID)
	if got.State != WatchActive || got.Firings != 1 || got.LastOutcome != "inboxed" || len(got.Pending.SignalIDs) != 0 {
		t.Fatalf("after complete: %+v", got)
	}

	// Cooldown: an immediate matching signal does not re-trigger.
	l.Ingest(SignalTurnCompleted, "s1/t2", "s1", "p", nil, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchActive {
		t.Fatalf("cooldown violated: state = %s", got.State)
	}
	// Past the cooldown it does.
	clock.advance(61 * time.Second)
	l.Ingest(SignalTurnCompleted, "s1/t3", "s1", "p", nil, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchTriggered {
		t.Fatalf("post-cooldown trigger missing: state = %s", got.State)
	}

	// Final allowed firing exhausts max_firings → expired.
	l.ClaimTriggered(0)
	l.CompleteFiring(created.ID, "inboxed")
	if got, _ := l.WatchByID(created.ID); got.State != WatchExpired {
		t.Fatalf("after max firings: state = %s, want expired", got.State)
	}
}

func TestWatchExpirySweep(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(time.Hour)
	created := mustCreate(t, l, w)

	clock.advance(2 * time.Hour)
	if n := l.SweepWatches(); n != 1 {
		t.Fatalf("sweep = %d, want 1", n)
	}
	if got, _ := l.WatchByID(created.ID); got.State != WatchExpired {
		t.Fatalf("state = %s", got.State)
	}

	// An expired watch never triggers again.
	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchExpired {
		t.Fatalf("expired watch triggered: %s", got.State)
	}
}

func TestWatchFailureBackoffAndTerminalFailed(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(6 * time.Hour)
	w.Response = ResponseRecommend
	created := mustCreate(t, l, w)

	fireAndFail := func(anchor string) {
		l.Ingest(SignalTurnCompleted, anchor, "s1", "p", nil, "")
		claimed := l.ClaimTriggered(1)
		if len(claimed) != 1 {
			t.Fatalf("claim for %s = %+v", anchor, claimed)
		}
		l.FailFiring(created.ID, "llm timeout")
	}

	fireAndFail("s1/t1")
	got, _ := l.WatchByID(created.ID)
	if got.State != WatchActive || got.Failures != 1 || got.NextEligibleAt.IsZero() {
		t.Fatalf("after failure 1: %+v", got)
	}
	// Backoff = cooldown * 2^1 = 120s: a signal at +90s must not trigger.
	clock.advance(90 * time.Second)
	l.Ingest(SignalTurnCompleted, "s1/t2", "s1", "p", nil, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchActive {
		t.Fatalf("backoff violated: %s", got.State)
	}
	// Past backoff (total +130s) it triggers again.
	clock.advance(40 * time.Second)
	l.Ingest(SignalTurnCompleted, "s1/t3", "s1", "p", nil, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchTriggered {
		t.Fatalf("post-backoff trigger missing: %s", got.State)
	}
	l.ClaimTriggered(1)
	l.FailFiring(created.ID, "llm timeout")

	// Third consecutive failure → terminal failed.
	clock.advance(time.Hour)
	fireAndFail("s1/t4")
	if got, _ := l.WatchByID(created.ID); got.State != WatchFailed || got.Failures != maxWatchFailures {
		t.Fatalf("after failure 3: %+v", got)
	}
}

func TestWatchSuccessResetsFailureStreak(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(6 * time.Hour)
	created := mustCreate(t, l, w)

	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	l.ClaimTriggered(0)
	l.FailFiring(created.ID, "boom")

	clock.advance(3 * time.Minute)
	l.Ingest(SignalTurnCompleted, "s1/t2", "s1", "p", nil, "")
	l.ClaimTriggered(0)
	l.CompleteFiring(created.ID, "ok")

	got, _ := l.WatchByID(created.ID)
	if got.Failures != 0 || !got.NextEligibleAt.IsZero() {
		t.Fatalf("failure streak not reset: %+v", got)
	}
}

func TestRecommendSlotsAndLLMBudget(t *testing.T) {
	l, clock := openWatchTest(t)

	rec := validWatch("s1")
	rec.ExpiresAt = clock.now().Add(time.Hour)
	rec.Response = ResponseRecommend
	rec.LLMBudget = 1
	recW := mustCreate(t, l, rec)

	inbox := validWatch("s1")
	inbox.ExpiresAt = clock.now().Add(time.Hour)
	inboxW := mustCreate(t, l, inbox)

	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")

	// Zero recommend slots: only the inbox watch is claimed; the recommend
	// watch stays triggered (deferral, not failure).
	claimed := l.ClaimTriggered(0)
	if len(claimed) != 1 || claimed[0].ID != inboxW.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	if got, _ := l.WatchByID(recW.ID); got.State != WatchTriggered {
		t.Fatalf("recommend watch state = %s, want triggered", got.State)
	}

	// With a slot, it claims.
	claimed = l.ClaimTriggered(1)
	if len(claimed) != 1 || claimed[0].ID != recW.ID {
		t.Fatalf("claimed = %+v", claimed)
	}

	// Budget spends on attempt and exhausts.
	if !l.SpendLLM(recW.ID) {
		t.Fatalf("first SpendLLM refused")
	}
	if l.SpendLLM(recW.ID) {
		t.Fatalf("SpendLLM exceeded budget")
	}
}

func TestCancelWatch(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(time.Hour)
	created := mustCreate(t, l, w)

	if err := l.CancelWatch(created.ID); err != nil {
		t.Fatalf("CancelWatch: %v", err)
	}
	if got, _ := l.WatchByID(created.ID); got.State != WatchCancelled {
		t.Fatalf("state = %s", got.State)
	}
	if err := l.CancelWatch(created.ID); err == nil {
		t.Fatalf("double cancel succeeded")
	}
	if err := l.CancelWatch("nope"); err == nil {
		t.Fatalf("cancel of unknown watch succeeded")
	}
}

func TestWatchPersistenceAndProcessingRecovery(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w := validWatch("s1")
	w.ExpiresAt = time.Now().Add(time.Hour)
	created := mustCreate(t, l, w)

	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	if claimed := l.ClaimTriggered(0); len(claimed) != 1 {
		t.Fatalf("claim failed")
	}
	// Daemon "crashes" mid-processing; reopen.
	l2, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := l2.WatchByID(created.ID)
	if !ok {
		t.Fatalf("watch not persisted")
	}
	if got.State != WatchTriggered {
		t.Fatalf("state after recovery = %s, want triggered", got.State)
	}
	if len(got.Pending.SignalIDs) != 1 {
		t.Fatalf("pending lost across restart: %+v", got.Pending)
	}
}

// --- item audit / resolution / recommendation ---

func TestResolveItemAndAudit(t *testing.T) {
	l, _ := openWatchTest(t)
	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	items := l.UnresolvedItems()
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	id := items[0].ID

	l.AppendItemAudit(id, AuditEvent{Kind: AuditPolicyDecision, Detail: "inbox (observe)"})
	l.SetRecommendation(id, "verify the diff, then park")

	if err := l.ResolveItem(id, "user ack"); err != nil {
		t.Fatalf("ResolveItem: %v", err)
	}
	it, _ := l.ItemByID(id)
	if it.Status != StatusResolved || it.Resolution != "user ack" {
		t.Fatalf("item = %+v", it)
	}
	if it.Recommendation != "verify the diff, then park" {
		t.Fatalf("recommendation = %q", it.Recommendation)
	}
	kinds := make([]string, 0, len(it.Audit))
	for _, ev := range it.Audit {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{AuditPolicyDecision, AuditResolution}
	if len(kinds) != len(want) || kinds[0] != want[0] || kinds[1] != want[1] {
		t.Fatalf("audit kinds = %v", kinds)
	}

	if err := l.ResolveItem(id, "again"); err == nil {
		t.Fatalf("double resolve succeeded")
	}
	if err := l.ResolveItem("nope", ""); err == nil {
		t.Fatalf("resolve of unknown item succeeded")
	}
	if l.UnresolvedItems() != nil && len(l.UnresolvedItems()) != 0 {
		t.Fatalf("resolved item still unresolved")
	}
}

func TestItemAuditCapped(t *testing.T) {
	l, _ := openWatchTest(t)
	l.Ingest(SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	id := l.UnresolvedItems()[0].ID
	for i := 0; i < maxItemAuditEvents+25; i++ {
		l.AppendItemAudit(id, AuditEvent{Kind: AuditDelivery, Detail: "d"})
	}
	it, _ := l.ItemByID(id)
	if len(it.Audit) != maxItemAuditEvents {
		t.Fatalf("audit len = %d, want %d", len(it.Audit), maxItemAuditEvents)
	}
}

// --- concurrency ---

func TestConcurrentWatchOps(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("")
	w.ExpiresAt = clock.now().Add(time.Hour)
	w.CooldownSeconds = 1
	w.MaxFirings = MaxWatchFirings
	created := mustCreate(t, l, w)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				l.Ingest(SignalTurnCompleted, "s/t", "s", "p", nil, "") // mostly dedup
				for _, c := range l.ClaimTriggered(1) {
					l.CompleteFiring(c.ID, "ok")
				}
				l.Watches()
				l.UnresolvedItems()
			}
		}(g)
	}
	wg.Wait()
	if _, ok := l.WatchByID(created.ID); !ok {
		t.Fatalf("watch vanished")
	}
}
