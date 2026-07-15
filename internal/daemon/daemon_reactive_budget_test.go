package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ledger"
)

func claudeWritePref(t *testing.T, k, v string) {
	t.Helper()
	if err := claude.WritePref(k, v); err != nil {
		t.Fatalf("WritePref %q=%q: %v", k, v, err)
	}
}

func TestReactiveBudgetSpendAndRollover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.json")
	day1 := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	s := openReactiveBudget(path, day1)

	// Spend up to a cap of 3, then exhaust.
	for i := 0; i < 3; i++ {
		if !s.trySpend(3, day1) {
			t.Fatalf("spend %d refused within cap", i)
		}
	}
	if s.trySpend(3, day1) {
		t.Fatal("spent beyond the daily cap")
	}
	if got := s.usedToday(day1); got != 3 {
		t.Fatalf("used = %d, want 3", got)
	}

	// Rollover to the next day resets the counter.
	day2 := day1.Add(24 * time.Hour)
	if !s.trySpend(3, day2) {
		t.Fatal("spend refused after date rollover")
	}
	if got := s.usedToday(day2); got != 1 {
		t.Fatalf("used after rollover = %d, want 1", got)
	}
}

func TestReactiveBudgetCrashCannotReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.json")
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)

	s := openReactiveBudget(path, now)
	s.trySpend(20, now)
	s.trySpend(20, now) // 2 spent, persisted

	// Simulate a crash-restart mid-day: reopen from disk, same date.
	s2 := openReactiveBudget(path, now)
	if got := s2.usedToday(now); got != 2 {
		t.Fatalf("reloaded used = %d, want 2 (crash must not reset spend)", got)
	}
	// A zero cap disables reactive LLM runs entirely.
	if s2.trySpend(0, now) {
		t.Fatal("spent against a zero cap")
	}
}

// TestReactiveRecommendExhaustedGlobalBudgetDegradesToInbox asserts a
// recommend firing degrades to inbox (no LLM) when the global daily budget is
// exhausted, with an audit line, and leaves the per-watch budget untouched.
func TestReactiveRecommendExhaustedGlobalBudgetDegradesToInbox(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	forkCalls := 0
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID != "main-1" {
			forkCalls++
			f.textDelta(sessionID, "verify it")
		}
		f.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	d, _ := newReactiveDaemon(t, f)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusUserTurn}}

	// Global budget with cap 0 → exhausted from the start.
	fixed := time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local)
	d.reactiveClock = func() time.Time { return fixed }
	d.reactiveBudget = openReactiveBudget(filepath.Join(t.TempDir(), "budget.json"), fixed)
	claudeWritePref(t, "reactive.daily_llm_calls", "0")

	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseRecommend,
	})
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", nil, "")
	d.reactiveTick()
	w := waitWatchState(t, d, created.ID, ledger.WatchActive)

	if forkCalls != 0 {
		t.Fatalf("fork LLM ran despite exhausted global budget (%d)", forkCalls)
	}
	if w.LLMUsed != 0 {
		t.Fatalf("per-watch budget spent on a globally-exhausted day: llm_used=%d", w.LLMUsed)
	}
	if !strings.Contains(w.LastOutcome, "inboxed") || !strings.Contains(w.LastOutcome, "budget exhausted") {
		t.Fatalf("outcome = %q, want inboxed (budget exhausted)", w.LastOutcome)
	}
	items := d.perception.UnresolvedItems()
	if len(items) != 1 || items[0].Recommendation != "" {
		t.Fatalf("items = %+v", items)
	}
	if !hasAudit(items[0], ledger.AuditPolicyDecision, "degraded to inbox") {
		t.Fatalf("missing degrade audit: %v", items[0].Audit)
	}
}

// TestReactiveRecommendWithinBudgetDecrementsGlobal asserts a successful
// recommend spends exactly one global unit.
func TestReactiveRecommendWithinBudgetDecrementsGlobal(t *testing.T) {
	f := &fakeHermes{sessionID: "main-1", forkSessionID: "fork-1"}
	f.onPromptSession = func(f *fakeHermes, id int64, sessionID, text string) {
		if sessionID != "main-1" {
			f.textDelta(sessionID, "Verify: looks complete.")
		}
		f.reply(id, map[string]any{"stopReason": "end_turn"})
	}
	d, _ := newReactiveDaemon(t, f)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusUserTurn}}
	fixed := time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local)
	d.reactiveClock = func() time.Time { return fixed }
	d.reactiveBudget = openReactiveBudget(filepath.Join(t.TempDir(), "budget.json"), fixed)

	total, remaining := d.reactiveBudgetSnapshot()
	if remaining != total {
		t.Fatalf("fresh remaining %d != total %d", remaining, total)
	}
	created := mustWatch(t, d, ledger.Watch{
		Scope: ledger.WatchScope{SessionID: "s1"}, Condition: ledger.ConditionCompletedTurn, Response: ledger.ResponseRecommend,
	})
	d.perception.Ingest(ledger.SignalTurnCompleted, "s1/t1", "s1", "p", map[string]any{"claim": "did it"}, "")
	d.reactiveTick()
	waitWatchState(t, d, created.ID, ledger.WatchActive)

	_, remaining2 := d.reactiveBudgetSnapshot()
	if remaining2 != remaining-1 {
		t.Fatalf("global budget decremented by %d, want exactly 1", remaining-remaining2)
	}
}
