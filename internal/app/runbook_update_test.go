package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/receipt"
)

func samplePlan() *batch.Plan {
	return &batch.Plan{
		BatchID: "bat_test",
		OnError: batch.StopOnError,
		Preview: true,
		Steps: []batch.PlannedStep{
			{
				Index:     1,
				Operation: "queue_message",
				Target:    receipt.Target{SessionID: "aaaabbbbcccc", DisplayName: "fix tests"},
				Detail:    `queue "wrap up"`,
				Risk:      batch.RiskReversible,
				Step:      batch.Step{Op: batch.OpQueue, SessionID: "aaaabbbbcccc", Message: "wrap up"},
			},
			{
				Index:         2,
				Operation:     "kill_session",
				Target:        receipt.Target{SessionID: "ddddeeeeffff", DisplayName: "old spike"},
				Detail:        "kill session",
				Risk:          batch.RiskDestructive,
				ApprovalPoint: true,
				Step:          batch.Step{Op: batch.OpKill, SessionID: "ddddeeeeffff"},
			},
		},
		DestructiveCount: 1,
	}
}

func TestRunbookPlanMsgOpensConfirm(t *testing.T) {
	m := Model{state: StateNormal}
	updated, _ := m.handleRunbookPlanMsg(runbookPlanMsg{Name: "cleanup", Description: "park things", Plan: samplePlan()})
	got := updated.(Model)
	if got.state != StateRunbookConfirm || got.runbookConfirm == nil {
		t.Fatalf("state = %v, confirm = %v", got.state, got.runbookConfirm)
	}
}

func TestRunbookPlanErrorFlashes(t *testing.T) {
	m := Model{state: StateNormal}
	updated, cmd := m.handleRunbookPlanMsg(runbookPlanMsg{Name: "broadcast", Err: fmt.Errorf("required param \"message\" is missing")})
	got := updated.(Model)
	if got.state != StateNormal || got.runbookConfirm != nil {
		t.Fatalf("error must not open the confirm overlay: %v", got.state)
	}
	if cmd == nil {
		t.Fatal("error must flash")
	}
}

func TestRunbookConfirmEscCancels(t *testing.T) {
	m := Model{state: StateRunbookConfirm, runbookConfirm: &runbookConfirmState{Name: "cleanup", Plan: samplePlan()}}
	updated, _ := m.handleKeyRunbookConfirm(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.state != StateNormal || got.runbookConfirm != nil {
		t.Fatalf("esc must cancel: state=%v confirm=%v", got.state, got.runbookConfirm)
	}
}

func TestRunbookConfirmYesWithEmptyPlanFlashes(t *testing.T) {
	m := Model{state: StateRunbookConfirm, runbookConfirm: &runbookConfirmState{Name: "noop", Plan: &batch.Plan{Preview: true}}}
	updated, cmd := m.handleKeyRunbookConfirm(keyRunes("y"))
	got := updated.(Model)
	if got.state != StateNormal || got.runbookConfirm != nil {
		t.Fatalf("state = %v", got.state)
	}
	if cmd == nil {
		t.Fatal("empty plan must flash, not silently do nothing")
	}
}

func TestRenderRunbookConfirmShowsStepsAndRisk(t *testing.T) {
	m := Model{runbookConfirm: &runbookConfirmState{Name: "cleanup", Description: "park things", Plan: samplePlan()}}
	out := ansi.Strip(m.renderRunbookConfirm(120))
	for _, want := range []string{
		"runbook: cleanup",
		"batch: 2 step(s)",
		"1 destructive",
		"queue → fix tests (aaaabbbb)",
		"kill → old spike (ddddeeee)",
		"y run 2 step(s)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("confirm overlay missing %q:\n%s", want, out)
		}
	}
}

func TestRunbookRunMsgFlashesOutcome(t *testing.T) {
	m := Model{}
	res := &batch.Result{BatchID: "bat_1", Outcome: batch.OutcomePartial, Executed: 1, Failed: 1, Skipped: 2}
	_, cmd := m.handleRunbookRunMsg(runbookRunMsg{Name: "cleanup", Result: res})
	if cmd == nil {
		t.Fatal("result must flash")
	}
}
