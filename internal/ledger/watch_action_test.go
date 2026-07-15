package ledger

import (
	"strings"
	"testing"
	"time"
)

// W8: action_reconciled watches anchored to one specific action_id.

func TestCreateWatchActionIDRequiresActionReconciled(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("s1")
	w.ExpiresAt = clock.now().Add(24 * time.Hour)
	w.Scope.ActionID = "act_1"
	if _, err := l.CreateWatch(w); err == nil || !strings.Contains(err.Error(), "action_reconciled") {
		t.Fatalf("action_id with completed_turn condition must be rejected; got %v", err)
	}
}

func TestActionScopedWatchFiresOnlyForItsAction(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("")
	w.ExpiresAt = clock.now().Add(24 * time.Hour)
	w.Condition = ConditionActionReconciled
	w.Scope = WatchScope{ActionID: "act_target"}
	created, err := l.CreateWatch(w)
	if err != nil {
		t.Fatal(err)
	}

	// A queue delivery for a DIFFERENT action does not trigger it.
	l.Ingest(SignalQueueDelivered, "s1/qi_other", "s1", "p",
		map[string]any{"action_id": "act_other", "queue_item_id": "qi_other"}, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchActive {
		t.Fatalf("watch triggered by unrelated action: state = %s", got.State)
	}

	// The delivery signal carrying the watched action_id in evidence triggers it.
	l.Ingest(SignalQueueDelivered, "s1/qi_mine", "s1", "p",
		map[string]any{"action_id": "act_target", "queue_item_id": "qi_mine"}, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchTriggered {
		t.Fatalf("watch not triggered by its action's delivery: state = %s", got.State)
	}
}

func TestActionScopedWatchMatchesActionFailedAnchor(t *testing.T) {
	l, clock := openWatchTest(t)
	w := validWatch("")
	w.ExpiresAt = clock.now().Add(24 * time.Hour)
	w.Condition = ConditionActionReconciled
	w.Scope = WatchScope{ActionID: "act_fail"}
	created, err := l.CreateWatch(w)
	if err != nil {
		t.Fatal(err)
	}

	// action_failed signals anchor ON the action id itself.
	l.Ingest(SignalActionFailed, "act_fail", "s1", "p",
		map[string]any{"operation": "send_message", "error": "gone"}, "")
	if got, _ := l.WatchByID(created.ID); got.State != WatchTriggered {
		t.Fatalf("watch not triggered by action_failed anchor: state = %s", got.State)
	}
}
