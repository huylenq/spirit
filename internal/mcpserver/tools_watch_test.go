package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/ledger"
	"github.com/huylenq/spirit/internal/receipt"
)

func decodeReceipt(t *testing.T, raw json.RawMessage) receipt.ActionReceipt {
	t.Helper()
	var r receipt.ActionReceipt
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	return r
}

func TestCreateWatchStampsActionIDAsRequestID(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions()}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, body := pc.callTool(t, "create_watch", map[string]any{
		"session_id": "sess-1", "condition": "completed_turn", "response": "inspect_and_recommend",
	})
	if res.IsError {
		t.Fatalf("create_watch errored: %s", body)
	}
	rcpt := decodeReceipt(t, body)
	if rcpt.Operation != "create_watch" || rcpt.DeliveryOutcome != receipt.OutcomeCompleted {
		t.Fatalf("receipt = %+v", rcpt)
	}
	if len(fd.watches) != 1 {
		t.Fatalf("watches created = %d", len(fd.watches))
	}
	w := fd.watches[0]
	if w.CreatedBy != "lulu" {
		t.Fatalf("created_by = %q", w.CreatedBy)
	}
	// The receipt's action id IS the watch's created_by_request_id: a Lulu-
	// created watch is traceable to the exact tool call that made it.
	if w.CreatedByRequestID == "" || w.CreatedByRequestID != rcpt.ActionID {
		t.Fatalf("created_by_request_id = %q, action_id = %q", w.CreatedByRequestID, rcpt.ActionID)
	}
}

func TestCreateWatchValidationError(t *testing.T) {
	pc := newPipeClient(t, &fakeDaemon{})
	defer pc.close()

	res, body := pc.callTool(t, "create_watch", map[string]any{"session_id": "sess-1"})
	if !res.IsError {
		t.Fatalf("expected isError for missing condition/response")
	}
	rcpt := decodeReceipt(t, body)
	if rcpt.DeliveryOutcome != receipt.OutcomeFailed || !strings.Contains(rcpt.Error, "required") {
		t.Fatalf("receipt = %+v", rcpt)
	}
}

func TestListAndCancelWatch(t *testing.T) {
	fd := &fakeDaemon{watches: []ledger.Watch{{ID: "w1", State: ledger.WatchActive, Condition: ledger.ConditionWaiting}}}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, body := pc.callTool(t, "list_watches", map[string]any{})
	if res.IsError {
		t.Fatalf("list_watches errored: %s", body)
	}
	var watches []ledger.Watch
	if err := json.Unmarshal(body, &watches); err != nil || len(watches) != 1 || watches[0].ID != "w1" {
		t.Fatalf("watches = %s (err %v)", body, err)
	}

	res, body = pc.callTool(t, "cancel_watch", map[string]any{"watch_id": "w1"})
	if res.IsError {
		t.Fatalf("cancel_watch errored: %s", body)
	}
	rcpt := decodeReceipt(t, body)
	if rcpt.DeliveryOutcome != receipt.OutcomeCompleted {
		t.Fatalf("receipt = %+v", rcpt)
	}
	if fd.watches[0].State != ledger.WatchCancelled {
		t.Fatalf("watch state = %s", fd.watches[0].State)
	}

	res, body = pc.callTool(t, "cancel_watch", map[string]any{"watch_id": "nope"})
	if !res.IsError {
		t.Fatalf("cancel of unknown watch did not error: %s", body)
	}
}

func TestListAndResolveAttention(t *testing.T) {
	fd := &fakeDaemon{attentionItems: []ledger.AttentionItem{{
		ID: "item-1", Category: ledger.CategoryVerifyClaim, Severity: ledger.SeverityAttend,
		Status: ledger.StatusOpen, Recommendation: "verify then park",
	}}}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, body := pc.callTool(t, "list_attention", map[string]any{})
	if res.IsError {
		t.Fatalf("list_attention errored: %s", body)
	}
	var payload struct {
		Items []ledger.AttentionItem `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Items) != 1 {
		t.Fatalf("payload = %s (err %v)", body, err)
	}
	if payload.Items[0].Recommendation != "verify then park" {
		t.Fatalf("recommendation lost: %+v", payload.Items[0])
	}

	res, body = pc.callTool(t, "resolve_attention", map[string]any{
		"item_id": "item-1", "resolution": "verified: tests pass",
	})
	if res.IsError {
		t.Fatalf("resolve_attention errored: %s", body)
	}
	if len(fd.resolved) != 1 || fd.resolved[0] != "item-1|verified: tests pass" {
		t.Fatalf("resolved = %v", fd.resolved)
	}
}

func TestWatchToolsListed(t *testing.T) {
	pc := newPipeClient(t, &fakeDaemon{})
	defer pc.close()
	resp := pc.call(t, "tools/list", map[string]any{})
	raw, _ := json.Marshal(resp.Result)
	for _, name := range []string{"create_watch", "list_watches", "cancel_watch", "list_attention", "resolve_attention"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("tools/list missing %s", name)
		}
	}
}

func TestReactiveStatusToolRoundTrip(t *testing.T) {
	fd := &fakeDaemon{reactiveStatus: daemon.ReactiveStatusData{
		Enabled: true, Leased: true, DurableReactive: true, GateReason: "durable",
		LLMBudgetTotal: 20, LLMBudgetRemaining: 19,
	}}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, body := pc.callTool(t, "reactive_status", map[string]any{})
	if res.IsError {
		t.Fatalf("reactive_status errored: %s", body)
	}
	var st daemon.ReactiveStatusData
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if !st.Enabled || !st.Leased || st.GateReason != "durable" || st.LLMBudgetRemaining != 19 {
		t.Fatalf("reactive_status payload = %+v", st)
	}

	// There is deliberately NO enable/disable tool — the autonomy switch is human-only.
	resp := pc.call(t, "tools/list", map[string]any{})
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), `"reactive_status"`) {
		t.Error("tools/list missing reactive_status")
	}
	for _, forbidden := range []string{"reactive_enable", "reactive_disable", "reactive_pause"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Errorf("tools/list exposes a durable-reactivity mutation tool: %s", forbidden)
		}
	}
}
