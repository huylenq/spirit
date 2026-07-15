package ledger

import (
	"fmt"
	"strings"
	"time"
)

// maxDetailedItems caps how many attention items the away-delta renders in
// full; the remainder is counted per category ("+4 more fyi").
const maxDetailedItems = 10

// ConsumeDelta assembles the away-delta block for one Lulu turn and commits
// its delivery: the returned block covers open (undelivered) attention items
// plus context from signals since the owner's cursor; rendered items are
// marked delivered stamped with requestID, and the owner's cursor advances to
// the current high-water mark.
//
// owner is the Hermes session UUID. An owner without a cursor (a fresh
// conversation after /new, or the very first turn ever) gets the snapshot of
// still-unresolved items — open and previously-delivered alike, because the
// new conversation has no memory of prior deliveries — and no signal history.
//
// Returns ("", false) when there is nothing to say, in which case nothing is
// consumed and no cursor is created or advanced (an empty fleet stays a
// zero-write no-op).
func (l *Ledger) ConsumeDelta(owner, requestID string) (string, bool) {
	if l == nil || owner == "" {
		return "", false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cursor, known := l.cursors[owner]

	var items []*AttentionItem
	if known {
		items = l.unresolvedItemsLocked(StatusOpen)
	} else {
		// Fresh conversation: snapshot of everything still unresolved.
		items = l.unresolvedItemsLocked(StatusOpen, StatusDelivered)
	}

	if len(items) == 0 {
		// Still advance a known cursor so signal history doesn't pile up
		// behind a quiet conversation; never *create* a cursor for nothing.
		if known && len(l.signals) > 0 {
			last := l.signals[len(l.signals)-1].ID
			if last != cursor.LastSignalID {
				l.cursors[owner] = Cursor{LastSignalID: last, UpdatedAt: now}
				l.saveCursors()
			}
		}
		return "", false
	}

	var b strings.Builder
	if known {
		fmt.Fprintf(&b, "<away-delta time=%q>\n", now.Format(time.RFC3339))
		b.WriteString("Since your last turn:\n")
	} else {
		fmt.Fprintf(&b, "<away-delta time=%q snapshot=\"true\">\n", now.Format(time.RFC3339))
		b.WriteString("Open attention items (fresh conversation — snapshot, not history):\n")
	}

	detailed := 0
	remainder := map[Category]int{}
	for _, cat := range categoryOrder {
		var group []*AttentionItem
		for _, it := range items {
			if it.Category == cat {
				group = append(group, it)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", cat)
		for _, it := range group {
			if detailed >= maxDetailedItems {
				remainder[cat]++
				continue
			}
			b.WriteString(l.renderItemLocked(it, now))
			b.WriteString("\n")
			detailed++
		}
	}
	for _, cat := range categoryOrder {
		if n := remainder[cat]; n > 0 {
			fmt.Fprintf(&b, "+%d more %s\n", n, cat)
		}
	}
	b.WriteString("</away-delta>")

	// Commit: every unresolved item covered by this block counts as delivered
	// (including the counted remainder — the count itself was communicated).
	for _, it := range items {
		it.Status = StatusDelivered
		it.Deliveries = append(it.Deliveries, Delivery{RequestID: requestID, At: now})
		it.UpdatedAt = now
	}
	l.saveAttention()

	last := ""
	if len(l.signals) > 0 {
		last = l.signals[len(l.signals)-1].ID
	}
	l.cursors[owner] = Cursor{LastSignalID: last, UpdatedAt: now}
	l.saveCursors()

	return b.String(), true
}

// categoryOrder is the away-delta's rendering order: decisions first, noise last.
var categoryOrder = []Category{
	CategoryNeedsDecision,
	CategoryBlocked,
	CategoryDeliveryFailure,
	CategoryOverlap,
	CategoryVerifyClaim,
	CategoryFYI,
}

// renderItemLocked renders one attention item as a single line, using the
// latest linked signal's evidence for the human-readable digest.
func (l *Ledger) renderItemLocked(it *AttentionItem, now time.Time) string {
	var b strings.Builder
	b.WriteString("- ")
	if it.Scope.SessionID != "" {
		fmt.Fprintf(&b, "[session %s", shortID(it.Scope.SessionID))
		if it.Scope.Project != "" {
			fmt.Fprintf(&b, " %s", it.Scope.Project)
		}
		b.WriteString("] ")
	} else if it.Scope.Project != "" {
		fmt.Fprintf(&b, "[%s] ", it.Scope.Project)
	}

	var latest *Signal
	for i := len(it.SignalIDs) - 1; i >= 0 && latest == nil; i-- {
		latest = l.byID[it.SignalIDs[i]]
	}
	if latest != nil {
		b.WriteString(describeSignal(latest))
	} else {
		b.WriteString(string(it.Category))
	}
	if n := len(it.SignalIDs); n > 1 {
		fmt.Fprintf(&b, " (×%d)", n)
	}
	fmt.Fprintf(&b, " (%s)", relativeAge(now.Sub(it.UpdatedAt)))
	return b.String()
}

// describeSignal renders a one-line human digest of a signal from its kind and
// bounded evidence.
func describeSignal(sig *Signal) string {
	ev := func(key string) string {
		s, _ := sig.Evidence[key].(string)
		return s
	}
	switch sig.Kind {
	case SignalTurnCompleted:
		if claim := ev("claim"); claim != "" {
			return "turn completed: " + firstLine(claim)
		}
		return "turn completed"
	case SignalWaitingInput:
		msg := "waiting for input"
		if k := ev("waiting_kind"); k != "" {
			msg = "waiting: " + k
		}
		if intent := ev("last_user_intent"); intent != "" {
			msg += " — was asked: " + firstLine(intent)
		}
		return msg
	case SignalOverlapDetected:
		if f := ev("file"); f != "" {
			return "file overlap on " + f
		}
		return "file overlap"
	case SignalQueueDelivered:
		if p := ev("prompt"); p != "" {
			return "queued message delivered: " + firstLine(p)
		}
		return "queued message delivered"
	case SignalQueueFailed:
		msg := "queued message failed"
		if r := ev("error"); r != "" {
			msg += ": " + firstLine(r)
		}
		return msg
	case SignalSessionEnded:
		msg := "session ended"
		if t := ev("title"); t != "" {
			msg += ": " + t
		}
		return msg
	case SignalActionFailed:
		msg := "action failed"
		if op := ev("operation"); op != "" {
			msg = op + " failed"
		}
		if r := ev("error"); r != "" {
			msg += ": " + firstLine(r)
		}
		return msg
	case SignalLaterWoke:
		msg := "later timer woke"
		if t := ev("title"); t != "" {
			msg += ": " + t
		}
		return msg
	default:
		return string(sig.Kind)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 140
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func relativeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
