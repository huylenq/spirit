package daemon

import (
	"encoding/json"
	"testing"

	"github.com/huylenq/spirit/internal/agent"
)

// W8: a run_actions permission request renders as typed batch steps, never an
// opaque JSON blob.

func batchPermissionParams(t *testing.T, rawInput any) json.RawMessage {
	t.Helper()
	params := map[string]any{
		"sessionId": "lulu-main",
		"toolCall": map[string]any{
			"toolCallId": "tc-1",
			"title":      "spirit - run_actions",
			"kind":       "execute",
			"rawInput":   rawInput,
		},
		"options": []map[string]any{
			{"optionId": "allow_once", "kind": "allow_once", "name": "Allow once"},
			{"optionId": "deny", "kind": "reject_once", "name": "Deny"},
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParsePermissionRequestDecodesBatch(t *testing.T) {
	raw := batchPermissionParams(t, map[string]any{
		"actions": []map[string]any{
			{"op": "queue", "session_id": "s1", "message": "run the tests"},
			{"op": "kill", "session_id": "s2"},
		},
		"on_error": "stop",
	})
	parsed, err := parsePermissionRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.BatchSteps) != 2 {
		t.Fatalf("batch steps = %+v", parsed.BatchSteps)
	}
	if parsed.BatchSteps[0].Op != "queue" || parsed.BatchSteps[0].Target != "s1" || parsed.BatchSteps[0].Risk != "reversible" {
		t.Fatalf("step 1 = %+v", parsed.BatchSteps[0])
	}
	if parsed.BatchSteps[1].Op != "kill" || parsed.BatchSteps[1].Risk != "destructive" {
		t.Fatalf("step 2 = %+v", parsed.BatchSteps[1])
	}
	if parsed.BatchSteps[0].Detail == "" {
		t.Fatal("step detail missing")
	}
}

func TestParsePermissionRequestNonBatchRawInputUnaffected(t *testing.T) {
	raw := batchPermissionParams(t, map[string]any{
		"command":     "rm -rf build",
		"description": "clean",
	})
	parsed, err := parsePermissionRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.BatchSteps) != 0 {
		t.Fatalf("non-batch rawInput produced batch steps: %+v", parsed.BatchSteps)
	}
	if parsed.Command != "rm -rf build" {
		t.Fatalf("command = %q", parsed.Command)
	}
}

func TestParsePermissionRequestMalformedBatchIsNotFatal(t *testing.T) {
	raw := batchPermissionParams(t, map[string]any{
		"actions": "not-an-array",
	})
	parsed, err := parsePermissionRequest(raw)
	if err != nil {
		t.Fatalf("malformed batch must not fail the permission parse: %v", err)
	}
	if len(parsed.BatchSteps) != 0 {
		t.Fatalf("batch steps = %+v", parsed.BatchSteps)
	}
}

func TestEnrichBatchTargetsResolvesDisplayNames(t *testing.T) {
	d := &Daemon{
		sessions: []agent.Session{
			{SessionID: "aaaabbbb-cccc-dddd", PaneID: "%1", FirstMessage: "fix the tests"},
		},
	}
	steps := []CopilotPermissionBatchStep{
		{Index: 1, Op: "queue", Target: "aaaabbbb-cccc-dddd"},
		{Index: 2, Op: "send", Target: "unknown-id"},
	}
	d.enrichBatchTargets(steps)
	if steps[0].Target != "fix the tests (aaaabbbb)" {
		t.Fatalf("enriched target = %q", steps[0].Target)
	}
	if steps[1].Target != "unknown-id" {
		t.Fatalf("unknown target must stay verbatim: %q", steps[1].Target)
	}
}
