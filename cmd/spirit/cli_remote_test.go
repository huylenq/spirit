package main

import (
	"reflect"
	"testing"

	"github.com/huylenq/spirit/internal/claude"
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

// TestMutationReceipt pins the structured shape of the receipt seam so W3's
// ActionReceipt adoption is a deliberate, visible change.
func TestMutationReceipt(t *testing.T) {
	r := mutationReceipt("tag", "sess-1", map[string]any{"tags": []string{"x"}})
	if r["status"] != "ok" {
		t.Fatalf("status = %v, want ok", r["status"])
	}
	if r["operation"] != "tag" {
		t.Fatalf("operation = %v, want tag", r["operation"])
	}
	if r["target"] != "sess-1" {
		t.Fatalf("target = %v, want sess-1", r["target"])
	}
	if !reflect.DeepEqual(r["tags"], []string{"x"}) {
		t.Fatalf("tags = %v, want [x]", r["tags"])
	}

	// Empty target is omitted.
	r2 := mutationReceipt("synthesize", "", nil)
	if _, ok := r2["target"]; ok {
		t.Fatal("empty target should be omitted from the receipt")
	}
}
