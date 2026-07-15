package daemon

import (
	"testing"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/ledger"
)

// findSignal returns the newest signal of the given kind.
func findSignal(d *Daemon, kind ledger.SignalKind) *ledger.Signal {
	sigs := d.perception.Signals()
	for i := len(sigs) - 1; i >= 0; i-- {
		if sigs[i].Kind == kind {
			return &sigs[i]
		}
	}
	return nil
}

func TestSignalQueueItemOutcomeAnchorsOnItemID(t *testing.T) {
	d := newIngestDaemon(t)
	item := agent.QueueItem{ID: "qi_test1", Message: "run the linter", ActionID: "act_abc"}

	// A retried failure for the same item dedups on the item-id anchor.
	d.signalQueueItemOutcome(false, "s1", "p", item, "pane busy")
	d.signalQueueItemOutcome(false, "s1", "p", item, "pane busy")
	if got := signalCount(d, ledger.SignalQueueFailed); got != 1 {
		t.Fatalf("queue_failed signals = %d, want 1 (retry must dedup on item id)", got)
	}

	// Delivery supersedes the failure and carries item + action evidence.
	d.signalQueueItemOutcome(true, "s1", "p", item, "")
	sig := findSignal(d, ledger.SignalQueueDelivered)
	if sig == nil {
		t.Fatal("no queue_delivered signal")
	}
	if sig.Anchor != "s1/qi_test1" {
		t.Fatalf("anchor = %q, want s1/qi_test1", sig.Anchor)
	}
	if sig.Evidence["queue_item_id"] != "qi_test1" || sig.Evidence["action_id"] != "act_abc" {
		t.Fatalf("evidence = %#v", sig.Evidence)
	}
	if sig.Supersedes == "" {
		t.Fatal("delivery must supersede the prior failure signal")
	}

	// Two identical messages with distinct items are two distinct facts.
	other := agent.QueueItem{ID: "qi_test2", Message: "run the linter"}
	d.signalQueueItemOutcome(true, "s1", "p", other, "")
	if got := signalCount(d, ledger.SignalQueueDelivered); got != 2 {
		t.Fatalf("queue_delivered signals = %d, want 2 (distinct items are distinct facts)", got)
	}
}

func TestTurnAttributionLinksDeliveryToNextCompletedTurn(t *testing.T) {
	d := newIngestDaemon(t)
	d.sessions = []agent.Session{{SessionID: "s1", PaneID: "%1", Project: "p", Status: agent.StatusAgentTurn}}

	item := agent.QueueItem{ID: "qi_link", Message: "go", ActionID: "act_link"}
	d.recordTurnAttribution("s1", item)

	claude.WriteStatus("s1", agent.StatusUserTurn) //nolint:errcheck
	if res := d.patchSession(NudgeData{PaneID: "%1", SessionID: "s1", Status: "user-turn"}); res != patchApplied {
		t.Fatalf("patch result = %v", res)
	}
	sig := findSignal(d, ledger.SignalTurnCompleted)
	if sig == nil {
		t.Fatal("no turn_completed signal")
	}
	if sig.Evidence["caused_by_queue_item"] != "qi_link" || sig.Evidence["caused_by_action"] != "act_link" {
		t.Fatalf("turn evidence missing attribution: %#v", sig.Evidence)
	}

	// The attribution is consumed exactly once.
	if _, ok := d.takeTurnAttribution("s1"); ok {
		t.Fatal("attribution should have been consumed by the completed turn")
	}
}
