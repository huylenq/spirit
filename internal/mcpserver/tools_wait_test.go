package mcpserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/huylenq/spirit/internal/agent"
)

// seqDaemon serves a scripted sequence of fleet snapshots: each Sessions call
// consumes the next step (the last step repeats forever). It lets wait_session
// observe lifecycle transitions without a live daemon.
type seqDaemon struct {
	fakeDaemon
	seq  [][]agent.Session
	step int
}

func (s *seqDaemon) Sessions(string) ([]agent.Session, error) {
	i := s.step
	if i >= len(s.seq) {
		i = len(s.seq) - 1
	}
	s.step++
	return s.seq[i], nil
}

// fastPoll shrinks the wait polling cadence for the duration of a test.
func fastPoll(t *testing.T) {
	t.Helper()
	orig := waitPollInterval
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = orig })
}

func decodeWait(t *testing.T, raw json.RawMessage) waitResult {
	t.Helper()
	var w waitResult
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("decode wait result: %v (%s)", err, raw)
	}
	return w
}

func TestWaitSessionListedWithSchema(t *testing.T) {
	c := newPipeClient(t, &fakeDaemon{})
	defer c.close()
	resp := c.call(t, "tools/list", nil)
	raw, _ := json.Marshal(resp.Result)
	var list struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, d := range list.Tools {
		if d.Name == "wait_session" {
			if len(d.InputSchema) == 0 {
				t.Fatal("wait_session listed without an input schema")
			}
			return
		}
	}
	t.Fatal("wait_session not present in tools/list")
}

func TestWaitSessionReachesWorking(t *testing.T) {
	fastPoll(t)
	api := &seqDaemon{seq: [][]agent.Session{
		{{SessionID: "sess-1", PaneID: "%1", Status: agent.StatusUserTurn, FirstMessage: "fix it"}},
		{{SessionID: "sess-1", PaneID: "%1", Status: agent.StatusUserTurn}},
		{{SessionID: "sess-1", PaneID: "%1", Status: agent.StatusAgentTurn, QueuePending: []string{"next"}}},
	}}
	c := newPipeClient(t, api)
	defer c.close()

	res, raw := c.callTool(t, "wait_session", map[string]any{"session_id": "sess-1", "phase": "working", "timeout_seconds": 5})
	if res.IsError {
		t.Fatalf("wait_session errored: %s", raw)
	}
	w := decodeWait(t, raw)
	if w.Operation != "wait_session" || w.Phase != "working" || w.Outcome != "reached" {
		t.Fatalf("unexpected result: %+v", w)
	}
	if w.Target.SessionID != "sess-1" || w.Target.PaneID != "%1" {
		t.Fatalf("target not populated: %+v", w.Target)
	}
	if w.ObservedState == nil || !w.ObservedState.Alive || w.ObservedState.Status != "agent-turn" || w.ObservedState.QueueLen != 1 {
		t.Fatalf("observed state not captured: %+v", w.ObservedState)
	}
}

// cycle requires working to be observed before idle counts: an initial idle
// snapshot must not satisfy it.
func TestWaitSessionCycleSemantics(t *testing.T) {
	fastPoll(t)
	api := &seqDaemon{seq: [][]agent.Session{
		{{SessionID: "sess-1", Status: agent.StatusUserTurn}},  // pre-work idle: not a cycle
		{{SessionID: "sess-1", Status: agent.StatusAgentTurn}}, // working...
		{{SessionID: "sess-1", Status: agent.StatusAgentTurn}},
		{{SessionID: "sess-1", Status: agent.StatusUserTurn}}, // ...back to idle: cycle complete
	}}
	c := newPipeClient(t, api)
	defer c.close()

	res, raw := c.callTool(t, "wait_session", map[string]any{"session_id": "sess-1", "phase": "cycle", "timeout_seconds": 5})
	if res.IsError {
		t.Fatalf("wait_session errored: %s", raw)
	}
	w := decodeWait(t, raw)
	if w.Outcome != "reached" {
		t.Fatalf("cycle not reached: %+v", w)
	}
	// The sequence has exactly one idle→working→idle arc; reaching it must have
	// consumed at least the four scripted polls (i.e. the first idle didn't win).
	if api.step < 4 {
		t.Fatalf("cycle satisfied too early after %d polls (pre-work idle must not count)", api.step)
	}
}

// A session that disappears mid-wait is a legitimate observation (e.g. kill
// reconciliation): outcome vanished, alive=false, not a tool error.
func TestWaitSessionVanished(t *testing.T) {
	fastPoll(t)
	api := &seqDaemon{seq: [][]agent.Session{
		{{SessionID: "sess-1", Status: agent.StatusAgentTurn}},
		{}, // gone
	}}
	c := newPipeClient(t, api)
	defer c.close()

	res, raw := c.callTool(t, "wait_session", map[string]any{"session_id": "sess-1", "phase": "idle", "timeout_seconds": 5})
	if res.IsError {
		t.Fatalf("vanished should not be a tool error: %s", raw)
	}
	w := decodeWait(t, raw)
	if w.Outcome != "vanished" {
		t.Fatalf("outcome = %q, want vanished", w.Outcome)
	}
	if w.ObservedState == nil || w.ObservedState.Alive {
		t.Fatalf("vanished must observe alive=false: %+v", w.ObservedState)
	}
}

// Timing out is a tool error (reconciliation failed) carrying the last
// observed state so the caller can see what the session was doing instead.
func TestWaitSessionTimeout(t *testing.T) {
	fastPoll(t)
	api := &seqDaemon{seq: [][]agent.Session{
		{{SessionID: "sess-1", Status: agent.StatusAgentTurn}}, // stuck working forever
	}}
	c := newPipeClient(t, api)
	defer c.close()

	res, raw := c.callTool(t, "wait_session", map[string]any{"session_id": "sess-1", "phase": "idle", "timeout_seconds": 1})
	if !res.IsError {
		t.Fatalf("timeout should be a tool error: %s", raw)
	}
	w := decodeWait(t, raw)
	if w.Outcome != "timeout" || w.Error == "" {
		t.Fatalf("unexpected timeout result: %+v", w)
	}
	if w.ObservedState == nil || !w.ObservedState.Alive || w.ObservedState.Status != "agent-turn" {
		t.Fatalf("timeout should carry the last observed state: %+v", w.ObservedState)
	}
	if w.WaitedMs < 900 {
		t.Fatalf("waited_ms = %d, expected ≈1000", w.WaitedMs)
	}
}

func TestWaitSessionNeverSeenTimesOut(t *testing.T) {
	fastPoll(t)
	api := &seqDaemon{seq: [][]agent.Session{{}}}
	c := newPipeClient(t, api)
	defer c.close()

	res, raw := c.callTool(t, "wait_session", map[string]any{"session_id": "ghost", "phase": "idle", "timeout_seconds": 1})
	if !res.IsError {
		t.Fatalf("never-seen session should time out as a tool error: %s", raw)
	}
	w := decodeWait(t, raw)
	if w.Outcome != "timeout" || w.ObservedState == nil || w.ObservedState.Alive {
		t.Fatalf("unexpected result for never-seen session: %+v", w)
	}
}

func TestWaitSessionRejectsBadPhase(t *testing.T) {
	c := newPipeClient(t, &fakeDaemon{})
	defer c.close()
	res, raw := c.callTool(t, "wait_session", map[string]any{"session_id": "sess-1", "phase": "napping"})
	if !res.IsError {
		t.Fatalf("invalid phase accepted: %s", raw)
	}
}
