// Package ledger is Spirit's durable perception store (spec Decision 10,
// re-sequenced into W6): normalized signals, derived attention items, and
// per-consumer delivery cursors. It sits between fleet truth (live daemon
// state) and conversation history — the account of what *happened*,
// deduplicated and durable.
//
// Design center: a signal reports a monotonic fact, not an observation. The
// poll loop sees the same idle state 300 times; the ledger records one
// turn_completed. Every kind declares an anchor — the identity of the
// underlying fact — and ingest is idempotent on (kind, anchor).
//
// Storage (no new dependencies; matches existing ~/.spirit file patterns):
//
//	~/.spirit/ledger/signals-YYYY-MM-DD.ndjson   append-only day segments
//	~/.spirit/ledger/attention.json              small mutable set, atomic rewrite
//	~/.spirit/ledger/cursors.json                per-Hermes-session cursors
//
// The daemon loads a bounded window (default 7 days) into an in-memory index
// at start. Corrupt lines are skipped with a log line — they never wedge the
// daemon.
package ledger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SignalKind identifies the class of fact a signal reports.
type SignalKind string

const (
	SignalTurnCompleted   SignalKind = "turn_completed"
	SignalWaitingInput    SignalKind = "waiting_input"
	SignalOverlapDetected SignalKind = "overlap_detected"
	SignalQueueDelivered  SignalKind = "queue_delivered"
	SignalQueueFailed     SignalKind = "queue_failed"
	SignalSessionStarted  SignalKind = "session_started"
	SignalSessionEnded    SignalKind = "session_ended"
	SignalActionFailed    SignalKind = "action_failed"
	SignalLaterWoke       SignalKind = "later_woke"
)

// Signal is one durable, deduplicated fact about the fleet.
type Signal struct {
	ID         string         `json:"id"` // ULID, assigned at ingest (sortable = time-ordered)
	Kind       SignalKind     `json:"kind"`
	Anchor     string         `json:"anchor"` // dedup identity: the monotonic fact this signal reports
	SessionID  string         `json:"session_id,omitempty"`
	Project    string         `json:"project,omitempty"`
	ObservedAt time.Time      `json:"observed_at"`
	Evidence   map[string]any `json:"evidence,omitempty"`   // bounded, typed payload per kind
	Supersedes string         `json:"supersedes,omitempty"` // id of a signal this one resolves
}

// Category classifies an attention item per the W6 mapping table.
type Category string

const (
	CategoryNeedsDecision   Category = "needs_decision"
	CategoryVerifyClaim     Category = "verify_claim"
	CategoryBlocked         Category = "blocked"
	CategoryOverlap         Category = "overlap"
	CategoryDeliveryFailure Category = "delivery_failure"
	CategoryFYI             Category = "fyi"
)

// Severity orders attention items within the away-delta.
type Severity string

const (
	SeverityInfo   Severity = "info"
	SeverityAttend Severity = "attend"
	SeverityUrgent Severity = "urgent"
)

// ItemStatus is the attention-item lifecycle: open → delivered → resolved/expired.
type ItemStatus string

const (
	StatusOpen      ItemStatus = "open"
	StatusDelivered ItemStatus = "delivered"
	StatusResolved  ItemStatus = "resolved"
	StatusExpired   ItemStatus = "expired"
)

// Scope names what an attention item is about.
type Scope struct {
	SessionID string `json:"session_id,omitempty"`
	Project   string `json:"project,omitempty"`
	PlanSlug  string `json:"plan_slug,omitempty"`
}

// Delivery records one Lulu turn that consumed an item.
type Delivery struct {
	RequestID string    `json:"request_id"`
	At        time.Time `json:"at"`
}

// AttentionItem is a durable inbox record derived from one or more signals.
type AttentionItem struct {
	ID         string     `json:"id"`
	Category   Category   `json:"category"`
	Severity   Severity   `json:"severity"`
	Scope      Scope      `json:"scope"`
	SignalIDs  []string   `json:"signal_ids"`
	Status     ItemStatus `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Deliveries []Delivery `json:"deliveries,omitempty"`
	Resolution string     `json:"resolution,omitempty"` // what closed it: superseding signal id, user ack, expiry
}

// Cursor is a consumer's high-water mark in the signal log, keyed by Hermes
// session UUID.
type Cursor struct {
	LastSignalID string    `json:"last_signal_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DefaultWindow bounds how much signal history is loaded into memory at open.
const DefaultWindow = 7 * 24 * time.Hour

// itemExpiry is how long an unresolved item stays live before expiring.
const itemExpiry = 7 * 24 * time.Hour

// maxCursors bounds cursors.json; the oldest cursors are pruned past this.
const maxCursors = 20

// Dir returns the default ledger directory (~/.spirit/ledger).
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".spirit", "ledger")
}

// Ledger is the in-memory index over the on-disk store. All methods are
// safe for concurrent use.
type Ledger struct {
	mu      sync.Mutex
	dir     string
	window  time.Duration
	now     func() time.Time  // test seam
	signals []Signal          // window-bounded, ascending by ID (= time)
	byKey   map[string]string // kind+"\x00"+anchor → signal id
	byID    map[string]*Signal
	items   []*AttentionItem
	cursors map[string]Cursor
}

// Open loads the ledger from dir, reading day segments within window (0 →
// DefaultWindow) plus attention.json and cursors.json. Corrupt lines and
// unreadable files are skipped with a log; Open only fails when the directory
// itself cannot be created.
func Open(dir string, window time.Duration) (*Ledger, error) {
	if window <= 0 {
		window = DefaultWindow
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("ledger: create dir: %w", err)
	}
	l := &Ledger{
		dir:     dir,
		window:  window,
		now:     time.Now,
		byKey:   make(map[string]string),
		byID:    make(map[string]*Signal),
		cursors: make(map[string]Cursor),
	}
	l.loadSegments()
	l.loadAttention()
	l.loadCursors()
	l.expireStaleLocked(l.now())
	return l, nil
}

func dedupKey(kind SignalKind, anchor string) string {
	return string(kind) + "\x00" + anchor
}

// Ingest records a signal if its (kind, anchor) has not been seen inside the
// loaded window. It returns the stored signal and whether it was fresh. A
// fresh signal is appended to today's segment and folded into the attention
// set (coalescing with an open item of the same category and session scope).
// If supersedes names a known signal, items linked to that signal are resolved.
func (l *Ledger) Ingest(kind SignalKind, anchor, sessionID, project string, evidence map[string]any, supersedes string) (*Signal, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if id, seen := l.byKey[dedupKey(kind, anchor)]; seen {
		return l.byID[id], false
	}

	now := l.now()
	sig := Signal{
		ID:         newULID(now),
		Kind:       kind,
		Anchor:     anchor,
		SessionID:  sessionID,
		Project:    project,
		ObservedAt: now,
		Evidence:   boundEvidence(evidence),
		Supersedes: supersedes,
	}
	if err := l.appendSignal(sig); err != nil {
		log.Printf("ledger: append %s: %v", kind, err)
		// Keep the in-memory record anyway: perception continuity beats
		// disk hiccups; the signal is lost only across a restart.
	}
	l.signals = append(l.signals, sig)
	stored := &l.signals[len(l.signals)-1]
	l.byKey[dedupKey(kind, anchor)] = sig.ID
	l.byID[sig.ID] = stored

	if supersedes != "" {
		l.resolveItemsBySignalLocked(supersedes, "superseded by "+sig.ID, now)
	}
	l.deriveAttentionLocked(stored, now)
	l.expireStaleLocked(now)
	l.saveAttention()
	return stored, true
}

// SignalID returns the id of the signal previously ingested for (kind, anchor),
// or "" if none is in the loaded window. Used by ingest points that want to
// supersede a prior signal (e.g. queue delivery after queue failure).
func (l *Ledger) SignalID(kind SignalKind, anchor string) string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byKey[dedupKey(kind, anchor)]
}

// ResolveSessionItems resolves all unresolved items for a session in the given
// categories (edge-driven resolution: a waiting falling edge closes blocked/
// needs_decision items; a cleared overlap closes overlap items). Returns how
// many items were resolved.
func (l *Ledger) ResolveSessionItems(sessionID string, categories []Category, resolution string) int {
	if l == nil || sessionID == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	n := 0
	for _, it := range l.items {
		if it.Scope.SessionID != sessionID || it.Status == StatusResolved || it.Status == StatusExpired {
			continue
		}
		for _, c := range categories {
			if it.Category == c {
				it.Status = StatusResolved
				it.Resolution = resolution
				it.UpdatedAt = now
				n++
				break
			}
		}
	}
	if n > 0 {
		l.saveAttention()
	}
	return n
}

// Signals returns a copy of the in-window signal log, ascending by time.
func (l *Ledger) Signals() []Signal {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Signal(nil), l.signals...)
}

// Items returns a copy of the attention set.
func (l *Ledger) Items() []AttentionItem {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AttentionItem, 0, len(l.items))
	for _, it := range l.items {
		out = append(out, *it)
	}
	return out
}

// ResolveCategoryItems resolves every unresolved item of a category (e.g. all
// overlap items once the fleet is overlap-free). Returns how many resolved.
func (l *Ledger) ResolveCategoryItems(cat Category, resolution string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	n := 0
	for _, it := range l.items {
		if it.Category != cat || it.Status == StatusResolved || it.Status == StatusExpired {
			continue
		}
		it.Status = StatusResolved
		it.Resolution = resolution
		it.UpdatedAt = now
		n++
	}
	if n > 0 {
		l.saveAttention()
	}
	return n
}

// SignalsToday counts signals observed since local midnight (a cheap health
// stat for copilot status; replaces the journal's event count).
func (l *Ledger) SignalsToday() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	midnight := l.now().Truncate(24 * time.Hour)
	n := 0
	for i := len(l.signals) - 1; i >= 0; i-- {
		if l.signals[i].ObservedAt.Before(midnight) {
			break
		}
		n++
	}
	return n
}

// resolveItemsBySignalLocked resolves unresolved items linked to signalID.
func (l *Ledger) resolveItemsBySignalLocked(signalID, resolution string, now time.Time) {
	for _, it := range l.items {
		if it.Status == StatusResolved || it.Status == StatusExpired {
			continue
		}
		for _, sid := range it.SignalIDs {
			if sid == signalID {
				it.Status = StatusResolved
				it.Resolution = resolution
				it.UpdatedAt = now
				break
			}
		}
	}
}

// categoryOf maps a signal to its attention category (W6 mapping table).
// needs_decision is reserved for permission/question waits; Spirit's hooks
// only set the waiting flag for permission_prompt / elicitation_dialog
// notifications, carried in evidence["waiting_kind"].
func categoryOf(sig *Signal) Category {
	switch sig.Kind {
	case SignalTurnCompleted:
		return CategoryVerifyClaim
	case SignalWaitingInput:
		if k, _ := sig.Evidence["waiting_kind"].(string); k == "permission_prompt" || k == "elicitation_dialog" {
			return CategoryNeedsDecision
		}
		return CategoryBlocked
	case SignalOverlapDetected:
		return CategoryOverlap
	case SignalQueueFailed, SignalActionFailed:
		return CategoryDeliveryFailure
	default: // session_started/session_ended/later_woke/queue_delivered
		return CategoryFYI
	}
}

func severityOf(c Category) Severity {
	switch c {
	case CategoryNeedsDecision:
		return SeverityUrgent
	case CategoryFYI:
		return SeverityInfo
	default:
		return SeverityAttend
	}
}

// deriveAttentionLocked folds a fresh signal into the attention set. A signal
// joins an existing *open* (undelivered) item of the same category and session
// scope — that is the coalescing that keeps five completed turns from becoming
// five inbox rows — otherwise it creates a new open item.
//
// session_started deliberately produces no item: a spawn is context, not a
// call on the user's attention; it still reaches Lulu as a fleet-snapshot
// change and remains in the signal log.
func (l *Ledger) deriveAttentionLocked(sig *Signal, now time.Time) {
	if sig.Kind == SignalSessionStarted {
		return
	}
	cat := categoryOf(sig)
	for _, it := range l.items {
		if it.Status == StatusOpen && it.Category == cat && it.Scope.SessionID == sig.SessionID {
			it.SignalIDs = append(it.SignalIDs, sig.ID)
			it.UpdatedAt = now
			return
		}
	}
	l.items = append(l.items, &AttentionItem{
		ID:        newULID(now),
		Category:  cat,
		Severity:  severityOf(cat),
		Scope:     Scope{SessionID: sig.SessionID, Project: sig.Project},
		SignalIDs: []string{sig.ID},
		Status:    StatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// expireStaleLocked expires unresolved items older than itemExpiry.
func (l *Ledger) expireStaleLocked(now time.Time) {
	cutoff := now.Add(-itemExpiry)
	for _, it := range l.items {
		if (it.Status == StatusOpen || it.Status == StatusDelivered) && it.UpdatedAt.Before(cutoff) {
			it.Status = StatusExpired
			it.Resolution = "expired"
			it.UpdatedAt = now
		}
	}
}

// boundEvidence caps evidence payloads so a runaway caller cannot bloat
// segments: string values are truncated, and the map is shallow-copied.
const maxEvidenceString = 400

func boundEvidence(ev map[string]any) map[string]any {
	if len(ev) == 0 {
		return nil
	}
	out := make(map[string]any, len(ev))
	for k, v := range ev {
		if s, ok := v.(string); ok && len(s) > maxEvidenceString {
			v = s[:maxEvidenceString] + "…"
		}
		out[k] = v
	}
	return out
}

// unresolvedItemsLocked returns items still calling for attention, severity-
// then-recency ordered.
func (l *Ledger) unresolvedItemsLocked(statuses ...ItemStatus) []*AttentionItem {
	match := func(s ItemStatus) bool {
		for _, want := range statuses {
			if s == want {
				return true
			}
		}
		return false
	}
	var out []*AttentionItem
	for _, it := range l.items {
		if match(it.Status) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := severityRank(out[i].Severity), severityRank(out[j].Severity); a != b {
			return a > b
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func severityRank(s Severity) int {
	switch s {
	case SeverityUrgent:
		return 2
	case SeverityAttend:
		return 1
	default:
		return 0
	}
}
