package ledger

import (
	"fmt"
	"time"
)

// Watches are durable promises with a finite state machine and an audit trail
// (spec Decision 10): "watch this session" persists as a contract that outlives
// the daemon, fires on matching fresh signals, and records every autonomous
// processing step on the attention item it touched.
//
// The FSM lives entirely inside the ledger, under its lock:
//
//	active → triggered → processing → delivered → active | expired | cancelled | failed
//
// A watch can only trigger on a *fresh* signal (matching happens inside Ingest,
// after (kind, anchor) dedup), so repeated idle polls can never re-fire a watch
// — that property is inherited from W6's ingest idempotency.

// WatchCondition names the class of fleet fact a watch subscribes to.
type WatchCondition string

const (
	ConditionCompletedTurn    WatchCondition = "completed_turn"
	ConditionWaiting          WatchCondition = "waiting"
	ConditionOverlap          WatchCondition = "overlap"
	ConditionActionReconciled WatchCondition = "action_reconciled"
)

// WatchResponse is what Spirit does when the watch fires (the policy).
type WatchResponse string

const (
	ResponseInbox     WatchResponse = "inbox"
	ResponseNotify    WatchResponse = "notify"
	ResponseRecommend WatchResponse = "inspect_and_recommend"
)

// AutonomyOf maps a response onto the Decision 9 autonomy ladder. constrained-act
// deliberately has no mapping — it is outside W7's ceiling.
func AutonomyOf(r WatchResponse) string {
	switch r {
	case ResponseInbox:
		return "observe"
	case ResponseNotify:
		return "notify"
	case ResponseRecommend:
		return "recommend"
	}
	return ""
}

// WatchState is the FSM state.
type WatchState string

const (
	WatchActive     WatchState = "active"
	WatchTriggered  WatchState = "triggered"
	WatchProcessing WatchState = "processing"
	WatchDelivered  WatchState = "delivered" // transient: recorded in last_outcome; persisted state returns to active
	WatchExpired    WatchState = "expired"
	WatchCancelled  WatchState = "cancelled"
	WatchFailed     WatchState = "failed"
)

// terminal reports whether a watch state can never fire again.
func (s WatchState) terminal() bool {
	return s == WatchExpired || s == WatchCancelled || s == WatchFailed
}

// WatchScope bounds which signals a watch sees. Empty = fleet-wide.
type WatchScope struct {
	SessionID string `json:"session_id,omitempty"`
	Project   string `json:"project,omitempty"`
}

// WatchPending is the trigger context carried from triggered into processing:
// which fresh signal(s) fired the watch and which attention item they folded into.
type WatchPending struct {
	SignalIDs []string `json:"signal_ids,omitempty"`
	ItemID    string   `json:"item_id,omitempty"`
}

// Watch is one durable reactive contract (spec Decision 10 schema).
type Watch struct {
	ID                 string         `json:"watch_id"`
	Scope              WatchScope     `json:"scope"`
	Condition          WatchCondition `json:"condition"`
	Response           WatchResponse  `json:"response"`
	AutonomyLevel      string         `json:"autonomy_level"`
	State              WatchState     `json:"state"`
	ExpiresAt          time.Time      `json:"expires_at"`
	CooldownSeconds    int            `json:"cooldown_seconds"`
	MaxFirings         int            `json:"max_firings"`
	Firings            int            `json:"firings"`
	LastFiredAt        time.Time      `json:"last_fired_at,omitempty"`
	LLMBudget          int            `json:"llm_budget,omitempty"` // recommend only: max LLM invocations over the watch's life
	LLMUsed            int            `json:"llm_used,omitempty"`
	Failures           int            `json:"failures,omitempty"` // consecutive failed firings (reset on success)
	NextEligibleAt     time.Time      `json:"next_eligible_at,omitempty"`
	CreatedByRequestID string         `json:"created_by_request_id,omitempty"`
	CreatedBy          string         `json:"created_by,omitempty"` // "tui" | "lulu" | "lua"
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Pending            WatchPending   `json:"pending,omitempty"`
	LastOutcome        string         `json:"last_outcome,omitempty"`
}

func (w *Watch) cooldown() time.Duration {
	return time.Duration(w.CooldownSeconds) * time.Second
}

// Watch validation limits. A watch without an expiry or rate limit is invalid —
// enforced at creation, on every surface (spec Decision 9).
const (
	MaxWatchExpiry     = 7 * 24 * time.Hour
	MaxWatchFirings    = 100
	MaxWatchLLMBudget  = 20
	DefaultLLMBudget   = 5
	maxLiveWatches     = 100
	maxWatchFailures   = 3 // consecutive failures before a watch goes terminal-failed
	maxFailureBackoff  = time.Hour
	maxItemAuditEvents = 50
)

// CreateWatch validates and persists a new watch. Zero-valued limits are NOT
// defaulted here — surfaces choose their defaults; the ledger enforces validity
// (fail-fast: a watch without expiry or rate limit is invalid).
func (l *Ledger) CreateWatch(w Watch) (*Watch, error) {
	if l == nil {
		return nil, fmt.Errorf("ledger disabled")
	}
	switch w.Condition {
	case ConditionCompletedTurn, ConditionWaiting, ConditionOverlap, ConditionActionReconciled:
	default:
		return nil, fmt.Errorf("invalid watch condition %q", w.Condition)
	}
	switch w.Response {
	case ResponseInbox, ResponseNotify, ResponseRecommend:
	default:
		return nil, fmt.Errorf("invalid watch response %q", w.Response)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	if w.ExpiresAt.IsZero() || !w.ExpiresAt.After(now) {
		return nil, fmt.Errorf("watch requires a future expires_at (a watch without expiry is invalid)")
	}
	if w.ExpiresAt.After(now.Add(MaxWatchExpiry)) {
		return nil, fmt.Errorf("watch expiry exceeds the %s maximum", MaxWatchExpiry)
	}
	if w.CooldownSeconds <= 0 {
		return nil, fmt.Errorf("watch requires cooldown_seconds > 0 (a watch without a rate limit is invalid)")
	}
	if w.MaxFirings < 1 {
		return nil, fmt.Errorf("watch requires max_firings >= 1 (a watch without a firing budget is invalid)")
	}
	if w.MaxFirings > MaxWatchFirings {
		return nil, fmt.Errorf("max_firings exceeds the %d maximum", MaxWatchFirings)
	}
	if w.Response == ResponseRecommend {
		if w.LLMBudget == 0 {
			w.LLMBudget = DefaultLLMBudget
		}
		if w.LLMBudget < 0 || w.LLMBudget > MaxWatchLLMBudget {
			return nil, fmt.Errorf("llm_budget must be 1..%d", MaxWatchLLMBudget)
		}
	} else {
		w.LLMBudget = 0
	}

	live := 0
	for _, existing := range l.watches {
		if !existing.State.terminal() {
			live++
		}
	}
	if live >= maxLiveWatches {
		return nil, fmt.Errorf("too many live watches (%d); cancel some first", live)
	}

	w.ID = newULID(now)
	w.AutonomyLevel = AutonomyOf(w.Response)
	w.State = WatchActive
	w.Firings = 0
	w.LLMUsed = 0
	w.Failures = 0
	w.LastFiredAt = time.Time{}
	w.NextEligibleAt = time.Time{}
	w.Pending = WatchPending{}
	w.CreatedAt = now
	w.UpdatedAt = now

	stored := w
	l.watches = append(l.watches, &stored)
	l.saveWatches()
	out := stored
	return &out, nil
}

// CancelWatch moves a live watch to cancelled.
func (l *Ledger) CancelWatch(id string) error {
	if l == nil {
		return fmt.Errorf("ledger disabled")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.watches {
		if w.ID != id {
			continue
		}
		if w.State.terminal() {
			return fmt.Errorf("watch %s is already %s", id, w.State)
		}
		w.State = WatchCancelled
		w.Pending = WatchPending{}
		w.UpdatedAt = l.now()
		l.saveWatches()
		return nil
	}
	return fmt.Errorf("watch not found: %s", id)
}

// Watches returns a copy of every known watch (live and terminal within the
// retention window), newest first.
func (l *Ledger) Watches() []Watch {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Watch, 0, len(l.watches))
	for i := len(l.watches) - 1; i >= 0; i-- {
		out = append(out, *l.watches[i])
	}
	return out
}

// WatchByID returns a copy of the watch, if known.
func (l *Ledger) WatchByID(id string) (Watch, bool) {
	if l == nil {
		return Watch{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.watches {
		if w.ID == id {
			return *w, true
		}
	}
	return Watch{}, false
}

// SweepWatches expires live watches past their expiry. Returns how many expired.
func (l *Ledger) SweepWatches() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sweepWatchesLocked(l.now())
}

func (l *Ledger) sweepWatchesLocked(now time.Time) int {
	n := 0
	for _, w := range l.watches {
		if w.State.terminal() || w.State == WatchProcessing {
			continue // a processing watch finishes its run; expiry lands at CompleteFiring
		}
		if now.After(w.ExpiresAt) {
			w.State = WatchExpired
			w.Pending = WatchPending{}
			w.LastOutcome = "expired"
			w.UpdatedAt = now
			n++
		}
	}
	if n > 0 {
		l.saveWatches()
	}
	return n
}

// conditionMatches maps signal kinds onto watch conditions.
func conditionMatches(c WatchCondition, k SignalKind) bool {
	switch c {
	case ConditionCompletedTurn:
		return k == SignalTurnCompleted
	case ConditionWaiting:
		return k == SignalWaitingInput
	case ConditionOverlap:
		return k == SignalOverlapDetected
	case ConditionActionReconciled:
		return k == SignalQueueDelivered || k == SignalQueueFailed || k == SignalActionFailed
	}
	return false
}

// scopeMatches bounds a watch to a session or project. Fleet-scoped signals
// (empty session id, e.g. overlaps) match session-scoped watches only via
// project adjacency; an empty watch scope matches everything.
func scopeMatches(s WatchScope, sig *Signal) bool {
	if s.SessionID != "" && sig.SessionID != s.SessionID {
		return false
	}
	if s.Project != "" && sig.Project != s.Project {
		return false
	}
	return true
}

// matchWatchesLocked is called from Ingest for every FRESH signal (post-dedup):
// eligible active watches move to triggered with the signal + derived item as
// pending context; watches already triggered coalesce the new signal into their
// pending context instead of queuing a second firing. Audit lands on the item.
func (l *Ledger) matchWatchesLocked(sig *Signal, item *AttentionItem, now time.Time) {
	changed := false
	for _, w := range l.watches {
		if !conditionMatches(w.Condition, sig.Kind) || !scopeMatches(w.Scope, sig) {
			continue
		}
		switch w.State {
		case WatchActive:
			if now.After(w.ExpiresAt) {
				w.State = WatchExpired
				w.LastOutcome = "expired"
				w.UpdatedAt = now
				changed = true
				continue
			}
			if w.Firings >= w.MaxFirings {
				continue // CompleteFiring should have expired it; belt only
			}
			if !w.NextEligibleAt.IsZero() && now.Before(w.NextEligibleAt) {
				continue // failure backoff window
			}
			if !w.LastFiredAt.IsZero() && now.Sub(w.LastFiredAt) < w.cooldown() {
				continue // cooldown window
			}
			w.State = WatchTriggered
			w.Pending = WatchPending{SignalIDs: []string{sig.ID}}
			if item != nil {
				w.Pending.ItemID = item.ID
				l.appendItemAuditLocked(item, AuditEvent{
					At:      now,
					Kind:    AuditWatchTriggered,
					WatchID: w.ID,
					Detail:  fmt.Sprintf("signal %s (%s) matched %s watch", sig.ID, sig.Kind, w.Condition),
				})
			}
			w.UpdatedAt = now
			changed = true
		case WatchTriggered:
			w.Pending.SignalIDs = append(w.Pending.SignalIDs, sig.ID)
			if item != nil && w.Pending.ItemID == "" {
				w.Pending.ItemID = item.ID
			}
			if item != nil {
				l.appendItemAuditLocked(item, AuditEvent{
					At:      now,
					Kind:    AuditWatchTriggered,
					WatchID: w.ID,
					Detail:  fmt.Sprintf("signal %s coalesced into pending firing", sig.ID),
				})
			}
			w.UpdatedAt = now
			changed = true
		}
	}
	if changed {
		l.saveWatches()
	}
}

// ClaimTriggered moves eligible triggered watches to processing and returns
// copies for the daemon's reactive tick. recommendSlots bounds how many
// inspect_and_recommend watches may be claimed this tick (0 while a user turn
// streams or a reactive run is already in flight — those stay triggered and are
// retried later; deferral, not failure).
func (l *Ledger) ClaimTriggered(recommendSlots int) []Watch {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var claimed []Watch
	changed := false
	for _, w := range l.watches {
		if w.State != WatchTriggered {
			continue
		}
		if w.Response == ResponseRecommend {
			if recommendSlots <= 0 {
				continue
			}
			recommendSlots--
		}
		w.State = WatchProcessing
		w.UpdatedAt = now
		claimed = append(claimed, *w)
		changed = true
	}
	if changed {
		l.saveWatches()
	}
	return claimed
}

// SpendLLM consumes one unit of a watch's LLM budget. Returns false when the
// budget is exhausted (the caller downgrades the firing to notify). Spent on
// attempt, not on success, so a flapping failure cannot overspend.
func (l *Ledger) SpendLLM(watchID string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, w := range l.watches {
		if w.ID != watchID {
			continue
		}
		if w.LLMUsed >= w.LLMBudget {
			return false
		}
		w.LLMUsed++
		w.UpdatedAt = l.now()
		l.saveWatches()
		return true
	}
	return false
}

// CompleteFiring finishes a processing watch's firing: firings++, cooldown
// clock restarts, failure streak resets, and the watch returns to active — or
// expires when the firing budget or expiry is reached.
func (l *Ledger) CompleteFiring(watchID, outcome string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, w := range l.watches {
		if w.ID != watchID || w.State != WatchProcessing {
			continue
		}
		w.Firings++
		w.LastFiredAt = now
		w.Failures = 0
		w.NextEligibleAt = time.Time{}
		w.Pending = WatchPending{}
		w.LastOutcome = outcome
		if w.Firings >= w.MaxFirings || now.After(w.ExpiresAt) {
			w.State = WatchExpired
		} else {
			w.State = WatchActive
		}
		w.UpdatedAt = now
		l.saveWatches()
		return
	}
}

// FailFiring records a failed firing: exponential backoff
// (cooldown · 2^failures, capped at maxFailureBackoff), terminal failed state
// after maxWatchFailures consecutive failures. The individual firing is NOT
// retried (no autonomous retry loops, spec Decision 11) — the pending trigger
// context is dropped.
func (l *Ledger) FailFiring(watchID, reason string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, w := range l.watches {
		if w.ID != watchID || w.State != WatchProcessing {
			continue
		}
		w.Failures++
		w.Pending = WatchPending{}
		w.LastOutcome = "failed: " + reason
		if w.Failures >= maxWatchFailures {
			w.State = WatchFailed
		} else {
			backoff := w.cooldown() << uint(w.Failures)
			if backoff > maxFailureBackoff {
				backoff = maxFailureBackoff
			}
			w.NextEligibleAt = now.Add(backoff)
			w.State = WatchActive
		}
		w.UpdatedAt = now
		l.saveWatches()
		return
	}
}

// --- attention-item audit + resolution (the reactive causal chain) ---

// AuditEvent is one step of an attention item's reactive causal chain
// (spec Decision 10): raw event(s) → signal → item are implied by signal_ids;
// everything from the watch decision onward is recorded explicitly.
type AuditEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	WatchID string    `json:"watch_id,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// Audit event kinds.
const (
	AuditWatchTriggered = "watch_triggered"
	AuditPolicyDecision = "policy_decision"
	AuditLLMRun         = "llm_run"
	AuditDelivery       = "delivery"
	AuditResolution     = "resolution"
)

func (l *Ledger) appendItemAuditLocked(it *AttentionItem, ev AuditEvent) {
	it.Audit = append(it.Audit, ev)
	if len(it.Audit) > maxItemAuditEvents {
		it.Audit = it.Audit[len(it.Audit)-maxItemAuditEvents:]
	}
	it.UpdatedAt = ev.At
}

// AppendItemAudit records one audit event on an attention item and persists.
func (l *Ledger) AppendItemAudit(itemID string, ev AuditEvent) {
	if l == nil || itemID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, it := range l.items {
		if it.ID == itemID {
			if ev.At.IsZero() {
				ev.At = l.now()
			}
			l.appendItemAuditLocked(it, ev)
			l.saveAttention()
			return
		}
	}
}

// SetRecommendation attaches a reactive run's proposal to an attention item.
// The proposal is the ceiling of the recommend autonomy level — it mutates
// nothing but this record (spec Decision 9).
func (l *Ledger) SetRecommendation(itemID, text string) {
	if l == nil || itemID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, it := range l.items {
		if it.ID == itemID {
			it.Recommendation = text
			it.UpdatedAt = l.now()
			l.saveAttention()
			return
		}
	}
}

// ResolveItem explicitly resolves one attention item (user ack from the inbox,
// or Lulu via resolve_attention). Closes the W6 open question of delivered-but-
// unresolved items lingering until expiry.
func (l *Ledger) ResolveItem(itemID, resolution string) error {
	if l == nil {
		return fmt.Errorf("ledger disabled")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, it := range l.items {
		if it.ID != itemID {
			continue
		}
		if it.Status == StatusResolved || it.Status == StatusExpired {
			return fmt.Errorf("item %s is already %s", itemID, it.Status)
		}
		it.Status = StatusResolved
		if resolution == "" {
			resolution = "acknowledged"
		}
		it.Resolution = resolution
		l.appendItemAuditLocked(it, AuditEvent{At: now, Kind: AuditResolution, Detail: resolution})
		l.saveAttention()
		return nil
	}
	return fmt.Errorf("attention item not found: %s", itemID)
}

// UnresolvedItems returns open and delivered items, severity- then recency-
// ordered — the attention inbox's data source.
func (l *Ledger) UnresolvedItems() []AttentionItem {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	items := l.unresolvedItemsLocked(StatusOpen, StatusDelivered)
	out := make([]AttentionItem, 0, len(items))
	for _, it := range items {
		out = append(out, *it)
	}
	return out
}

// ItemByID returns a copy of one attention item.
func (l *Ledger) ItemByID(id string) (AttentionItem, bool) {
	if l == nil {
		return AttentionItem{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, it := range l.items {
		if it.ID == id {
			return *it, true
		}
	}
	return AttentionItem{}, false
}

// ItemSignals returns copies of the signals linked to an attention item (in
// link order), for bounded evidence assembly.
func (l *Ledger) ItemSignals(itemID string) []Signal {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, it := range l.items {
		if it.ID != itemID {
			continue
		}
		out := make([]Signal, 0, len(it.SignalIDs))
		for _, sid := range it.SignalIDs {
			if sig := l.byID[sid]; sig != nil {
				out = append(out, *sig)
			}
		}
		return out
	}
	return nil
}

// DescribeItem renders the one-line human digest for an attention item (reused
// by notifications and the inbox), e.g. "turn completed: fixed the tests".
func (l *Ledger) DescribeItem(it *AttentionItem) string {
	if l == nil || it == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var latest *Signal
	for i := len(it.SignalIDs) - 1; i >= 0 && latest == nil; i-- {
		latest = l.byID[it.SignalIDs[i]]
	}
	if latest == nil {
		return string(it.Category)
	}
	return describeSignal(latest)
}
