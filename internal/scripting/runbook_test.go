package scripting

import (
	"strings"
	"testing"

	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/runbook"
)

func buildFleet() []claude.ClaudeSession {
	return []claude.ClaudeSession{
		{SessionID: "a", PaneID: "%1", CWD: "/repo/x", Status: claude.StatusUserTurn},
		{SessionID: "b", PaneID: "%2", CWD: "/repo/y", Status: claude.StatusAgentTurn},
	}
}

func TestBuildRunbookStepsComputesSteps(t *testing.T) {
	script := `
local steps = {}
for _, s in ipairs(sessions()) do
  if s.status == "idle" then
    steps[#steps+1] = { op = "queue", session_id = s.id, message = params.message }
  end
end
return steps`
	steps, err := BuildRunbookSteps(script, buildFleet(), map[string]string{"message": "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Op != batch.OpQueue || steps[0].SessionID != "a" || steps[0].Message != "go" {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestBuildPhaseHasNoSideEffectVerbs(t *testing.T) {
	// The build VM is structurally side-effect-free: mutation verbs simply do
	// not exist. Calling one is a hard error, not a silent no-op.
	for _, verb := range []string{"send", "queue", "kill", "spawn", "set_tags", "set_note", "later", "commit", "run_actions", "runbook_run"} {
		script := `return { { op = "note", session_id = "a", note = tostring(` + verb + `) } }`
		_, err := BuildRunbookSteps(script, buildFleet(), nil)
		// tostring(nil-global) succeeds — assert the global is nil instead.
		if err != nil {
			continue // some verbs may error at parse; fine either way
		}
		script2 := `if ` + verb + ` ~= nil then error("side-effect verb available") end return { { op = "wait", session_id = "a", phase = "idle" } }`
		if _, err := BuildRunbookSteps(script2, buildFleet(), nil); err != nil {
			t.Fatalf("%s is available in the build VM: %v", verb, err)
		}
	}
}

func TestBuildPhaseErrorsAreEager(t *testing.T) {
	if _, err := BuildRunbookSteps(`return nil`, buildFleet(), nil); err == nil {
		t.Fatal("nil result must be an error")
	}
	if _, err := BuildRunbookSteps(`return {}`, buildFleet(), nil); err == nil {
		t.Fatal("empty step set must be an error (nothing to do is a bug, not a plan)")
	}
	if _, err := BuildRunbookSteps(`error("boom")`, buildFleet(), nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("script error not surfaced: %v", err)
	}
}

func TestCheckDeclaredActions(t *testing.T) {
	rb := runbook.Runbook{Name: "x", Actions: []string{"queue"}}
	ok := []batch.Step{{Op: batch.OpQueue, SessionID: "a", Message: "m"}, {Op: batch.OpWait, SessionID: "a", Phase: "idle"}}
	if err := checkDeclaredActions(rb, ok); err != nil {
		t.Fatalf("declared + wait must pass: %v", err)
	}
	bad := []batch.Step{{Op: batch.OpKill, SessionID: "a"}}
	if err := checkDeclaredActions(rb, bad); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("undeclared kill must fail: %v", err)
	}
}
