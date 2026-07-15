package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/batch"
)

func TestPlanActionsPreviewsWithoutExecuting(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions()}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, raw := pc.callTool(t, "plan_actions", map[string]any{
		"actions": []map[string]any{
			{"op": "queue", "session_id": "sess-1", "message": "run tests"},
			{"op": "kill", "session_id": "sess-2"},
		},
	})
	if res.IsError {
		t.Fatalf("plan_actions errored: %s", raw)
	}
	var plan batch.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Preview || len(plan.Steps) != 2 || plan.DestructiveCount != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Steps[0].Target.PaneID != "%1" {
		t.Fatalf("target not resolved: %+v", plan.Steps[0])
	}
	if len(fd.queued) != 0 || len(fd.killed) != 0 {
		t.Fatalf("plan executed something: queued=%v killed=%v", fd.queued, fd.killed)
	}
}

func TestPlanActionsFailFastOnUnknownSession(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions()}
	pc := newPipeClient(t, fd)
	defer pc.close()

	res, raw := pc.callTool(t, "plan_actions", map[string]any{
		"actions": []map[string]any{
			{"op": "note", "session_id": "sess-1", "note": "ok"},
			{"op": "send", "session_id": "ghost", "message": "x"},
		},
	})
	if !res.IsError {
		t.Fatalf("expected fail-fast error, got %s", raw)
	}
}

func TestRunActionsPartialFailureAndResume(t *testing.T) {
	fd := &fakeDaemon{sessions: fixtureSessions()}
	pc := newPipeClient(t, fd)
	defer pc.close()

	// kill sess-2 succeeds; send sess-2 then fails (gone); tag sess-1 skipped.
	res, raw := pc.callTool(t, "run_actions", map[string]any{
		"actions": []map[string]any{
			{"op": "kill", "session_id": "sess-2"},
			{"op": "send", "session_id": "sess-2", "message": "hi"},
			{"op": "tag", "session_id": "sess-1", "tags": []string{"t"}},
		},
	})
	if res.IsError {
		t.Fatalf("run_actions must return the structured result, not a tool error: %s", raw)
	}
	var result batch.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != batch.OutcomePartial || result.Executed != 1 || result.Failed != 1 || result.Skipped != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Remainder) != 1 || result.Remainder[0].Op != batch.OpTag {
		t.Fatalf("remainder = %+v", result.Remainder)
	}
	if len(fd.tagsSet) != 0 {
		t.Fatal("skipped step was executed")
	}
	if len(fd.actionReports) != 1 {
		t.Fatalf("failed step not reported to the ledger: %v", fd.actionReports)
	}

	// Resume the remainder.
	res2, raw2 := pc.callTool(t, "run_actions", map[string]any{
		"actions":   result.Remainder,
		"resume_of": result.BatchID,
	})
	if res2.IsError {
		t.Fatalf("resume errored: %s", raw2)
	}
	var resumed batch.Result
	if err := json.Unmarshal(raw2, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Outcome != batch.OutcomeCompleted || resumed.ResumeOf != result.BatchID {
		t.Fatalf("resume = %+v", resumed)
	}
	if got := fd.tagsSet["sess-1"]; len(got) != 1 || got[0] != "t" {
		t.Fatalf("resumed step did not execute: %v", fd.tagsSet)
	}
}

func TestRunbookToolsEndToEnd(t *testing.T) {
	fleet := []agent.Session{
		{SessionID: "a", PaneID: "%1", CWD: "/repo/x", Status: agent.StatusUserTurn, FirstMessage: "x"},
		{SessionID: "b", PaneID: "%2", CWD: "/repo/y", Status: agent.StatusUserTurn, FirstMessage: "y"},
		{SessionID: "c", PaneID: "%3", CWD: "/repo/x", Status: agent.StatusAgentTurn, FirstMessage: "busy"},
	}
	fd := &fakeDaemon{sessions: fleet}
	pc := newPipeClient(t, fd)
	defer pc.close()

	// list_runbooks includes the builtin broadcast.
	_, rawList := pc.callTool(t, "list_runbooks", nil)
	if string(rawList) == "" || !contains(rawList, `"broadcast"`) {
		t.Fatalf("list_runbooks = %s", rawList)
	}

	// explain executes nothing (no Lua at all).
	res, rawExplain := pc.callTool(t, "explain_runbook", map[string]any{"name": "broadcast"})
	if res.IsError || !contains(rawExplain, `"message"`) || !contains(rawExplain, `"queue"`) {
		t.Fatalf("explain_runbook = %s", rawExplain)
	}

	// Required param missing → precise error, nothing executed.
	resMissing, rawMissing := pc.callTool(t, "plan_runbook", map[string]any{"name": "broadcast"})
	if !resMissing.IsError || !contains(rawMissing, "required param") {
		t.Fatalf("missing required param must fail: %s", rawMissing)
	}

	// plan resolves the two idle /repo/x-or-any sessions; nothing queued.
	resPlan, rawPlan := pc.callTool(t, "plan_runbook", map[string]any{
		"name":   "broadcast",
		"params": map[string]string{"message": "wrap up", "project": "/repo/x"},
	})
	if resPlan.IsError {
		t.Fatalf("plan_runbook errored: %s", rawPlan)
	}
	var plan batch.Plan
	if err := json.Unmarshal(rawPlan, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Target.SessionID != "a" {
		t.Fatalf("plan steps = %+v", plan.Steps)
	}
	if len(fd.queued) != 0 {
		t.Fatalf("dry-run queued messages: %v", fd.queued)
	}

	// run executes the emitted batch with receipts.
	resRun, rawRun := pc.callTool(t, "run_runbook", map[string]any{
		"name":   "broadcast",
		"params": map[string]string{"message": "wrap up"},
	})
	if resRun.IsError {
		t.Fatalf("run_runbook errored: %s", rawRun)
	}
	var result batch.Result
	if err := json.Unmarshal(rawRun, &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != batch.OutcomeCompleted || result.Executed != 2 {
		t.Fatalf("result = %+v", result)
	}
	if len(fd.queued) != 2 {
		t.Fatalf("queued = %v", fd.queued)
	}
	// Queue items were stamped with the receipts' action ids for ledger linkage.
	for _, aid := range fd.queuedActionIDs {
		if aid == "" {
			t.Fatal("queue item missing action id stamp")
		}
	}
}

func contains(raw json.RawMessage, sub string) bool {
	return strings.Contains(string(raw), sub)
}
