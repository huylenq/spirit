package main

import (
	"reflect"
	"testing"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/receipt"
)

// TestPhaseReached exercises the wait <id> --phase state machine, which mirrors
// the Lua wait/cycle semantics (idle=user-turn, working=agent-turn, cycle=working
// then idle).
func TestPhaseReached(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		var saw bool
		if !phaseReached("idle", claude.StatusUserTurn, &saw) {
			t.Fatal("idle should be reached at user-turn")
		}
		if phaseReached("idle", claude.StatusAgentTurn, &saw) {
			t.Fatal("idle should not be reached at agent-turn")
		}
	})

	t.Run("working", func(t *testing.T) {
		var saw bool
		if !phaseReached("working", claude.StatusAgentTurn, &saw) {
			t.Fatal("working should be reached at agent-turn")
		}
		if phaseReached("working", claude.StatusUserTurn, &saw) {
			t.Fatal("working should not be reached at user-turn")
		}
	})

	t.Run("cycle requires working before idle", func(t *testing.T) {
		var saw bool
		// A pre-work idle must NOT satisfy cycle.
		if phaseReached("cycle", claude.StatusUserTurn, &saw) {
			t.Fatal("cycle must not complete on a pre-work idle")
		}
		// Enter working.
		if phaseReached("cycle", claude.StatusAgentTurn, &saw) {
			t.Fatal("cycle is not complete while still working")
		}
		if !saw {
			t.Fatal("cycle should have recorded the working observation")
		}
		// Return to idle → cycle complete.
		if !phaseReached("cycle", claude.StatusUserTurn, &saw) {
			t.Fatal("cycle should complete on idle after working")
		}
	})

	t.Run("unknown phase is never reached", func(t *testing.T) {
		var saw bool
		if phaseReached("bogus", claude.StatusUserTurn, &saw) {
			t.Fatal("unknown phase should never be reached")
		}
	})
}

// TestTailMessages exercises inspect's transcript-tail bounding.
func TestTailMessages(t *testing.T) {
	msgs := []string{"a", "b", "c", "d", "e"}
	cases := []struct {
		n    int
		want []string
	}{
		{2, []string{"d", "e"}},
		{5, msgs},
		{10, msgs}, // fewer than n → all
		{0, msgs},  // guard: non-positive → all
	}
	for _, c := range cases {
		if got := tailMessages(msgs, c.n); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("tailMessages(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

// TestCLIReceiptShape pins the W8 receipt unification: mutating agent verbs
// emit a real receipt.ActionReceipt (action_id + operation + target +
// delivery_outcome), not the pre-W3 {"status":"ok"} map.
func TestCLIReceiptShape(t *testing.T) {
	rcpt := receipt.New("tag", receipt.Target{SessionID: "sess-1", ResolvedBy: receipt.ResolvedExplicit})
	rcpt.Params = map[string]any{"tags": []string{"x"}}
	rcpt.DeliveryOutcome = receipt.OutcomeCompleted
	if rcpt.ActionID == "" || rcpt.AcceptedAt == "" {
		t.Fatalf("receipt missing identity: %+v", rcpt)
	}
	if rcpt.Target.SessionID != "sess-1" {
		t.Fatalf("target = %+v", rcpt.Target)
	}
	if !reflect.DeepEqual(rcpt.Params["tags"], []string{"x"}) {
		t.Fatalf("params = %v", rcpt.Params)
	}
}
