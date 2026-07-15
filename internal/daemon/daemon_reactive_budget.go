package daemon

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"sync"
	"time"
)

// Global daily provider budget (W9 §2/§6). A durable, date-keyed counter in
// ~/.spirit/ledger/budget.json that bounds worst-case reactive LLM spend across
// ALL watches per day, on top of each watch's own llm_budget. It is decremented
// on every reactive LLM *attempt* (mirrors the per-watch spend-on-attempt), so a
// crash-loop cannot overspend the day; the counter rolls over on date change.
//
// This is the only NEW durable state W9 adds; everything else is W7/W8
// infrastructure the worker reuses unchanged.

// defaultDailyLLMCalls is the fallback daily cap when the pref is unset.
const defaultDailyLLMCalls = 20

type reactiveBudgetFile struct {
	Date    string `json:"date"`           // YYYY-MM-DD (local)
	LLMUsed int    `json:"llm_calls_used"` // attempts spent today
}

type reactiveBudgetStore struct {
	mu   sync.Mutex
	path string
	date string
	used int
}

// openReactiveBudget loads (or initializes) the durable budget counter.
func openReactiveBudget(path string, now time.Time) *reactiveBudgetStore {
	s := &reactiveBudgetStore{path: path, date: now.Format("2006-01-02")}
	data, err := os.ReadFile(path)
	if err != nil {
		return s // fresh
	}
	var f reactiveBudgetFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("reactive budget: %s corrupt, starting fresh: %v", path, err)
		return s
	}
	if f.Date == s.date {
		s.used = f.LLMUsed // same day → resume spend
	}
	// A stale date rolls over to a fresh counter (used stays 0).
	return s
}

// rolloverLocked resets the counter when the local date changes.
func (s *reactiveBudgetStore) rolloverLocked(now time.Time) {
	day := now.Format("2006-01-02")
	if day != s.date {
		s.date = day
		s.used = 0
	}
}

func (s *reactiveBudgetStore) persistLocked() {
	data, err := json.Marshal(reactiveBudgetFile{Date: s.date, LLMUsed: s.used})
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("reactive budget: write: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("reactive budget: rename: %v", err)
	}
}

// trySpend consumes one unit against the daily cap, returning false when the day
// is exhausted (the caller degrades recommend to inbox). Spent on attempt and
// persisted immediately so a crash-loop cannot overspend.
func (s *reactiveBudgetStore) trySpend(cap int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked(now)
	if cap <= 0 {
		return false // a zero cap disables reactive LLM runs entirely
	}
	if s.used >= cap {
		return false
	}
	s.used++
	s.persistLocked()
	return true
}

// usedToday returns the attempts spent so far today (after rollover).
func (s *reactiveBudgetStore) usedToday(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolloverLocked(now)
	return s.used
}

// reactiveDailyBudget reads the configured daily cap (pref
// reactive.daily_llm_calls), falling back to defaultDailyLLMCalls.
func (d *Daemon) reactiveDailyBudget() int {
	v := d.readPref("reactive.daily_llm_calls")
	if v == "" {
		return defaultDailyLLMCalls
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultDailyLLMCalls
	}
	return n
}

// reactiveBudgetSnapshot reports (total, remaining) daily provider budget.
func (d *Daemon) reactiveBudgetSnapshot() (total, remaining int) {
	total = d.reactiveDailyBudget()
	if d.reactiveBudget == nil {
		return total, total
	}
	used := d.reactiveBudget.usedToday(d.reactiveNow())
	remaining = total - used
	if remaining < 0 {
		remaining = 0
	}
	return total, remaining
}

// reactiveSpendDailyBudget attempts to consume one global daily LLM unit,
// returning false when the day's budget is exhausted.
func (d *Daemon) reactiveSpendDailyBudget() bool {
	if d.reactiveBudget == nil {
		return true // budget store unavailable → do not block (fail-open on infra)
	}
	return d.reactiveBudget.trySpend(d.reactiveDailyBudget(), d.reactiveNow())
}
