package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/runbook"
	"github.com/huylenq/spirit/internal/scripting"
	"github.com/huylenq/spirit/internal/ui"
)

// Runbooks in the TUI palette (W8): selecting a runbook first runs its
// DRY-RUN plan and shows the preview overlay (targets + operations + risk);
// `y` then executes the exact previewed steps through the shared batch
// pipeline — preview-then-execute-what-you-previewed, the same
// plan→approve→action contract every other surface follows.

// runbookPlanMsg delivers a dry-run plan (or its error) for the confirm overlay.
type runbookPlanMsg struct {
	Name        string
	Description string
	Plan        *batch.Plan
	Err         error
}

// runbookRunMsg delivers the execution result of a confirmed runbook plan.
type runbookRunMsg struct {
	Name   string
	Result *batch.Result
	Err    error
}

// runbookConfirmState is the pending preview shown in StateRunbookConfirm.
type runbookConfirmState struct {
	Name        string
	Description string
	Plan        *batch.Plan
}

// execRunbookPlan starts the dry-run for a palette-selected runbook. Runbooks
// with required params are prefilled into the palette's Lua mode instead —
// the user supplies values and drives runbook_plan/runbook_run explicitly.
func (m Model) execRunbookPlan(rb runbook.Runbook) (Model, tea.Cmd) {
	var required []string
	for _, p := range rb.Params {
		if p.Required {
			required = append(required, p.Name)
		}
	}
	if len(required) > 0 {
		m.state = StatePalette
		m.palette.Activate(nil)
		m.palette.EnterLuaMode()
		args := ""
		for i, name := range required {
			if i > 0 {
				args += ", "
			}
			args += name + ` = ""`
		}
		m.palette.PrefillLua(fmt.Sprintf(`return runbook_run(%q, { %s })`, rb.Name, args))
		return m, nil
	}

	client := m.client
	name := rb.Name
	return m, func() tea.Msg {
		loaded, err := runbook.Load(name)
		if err != nil {
			return runbookPlanMsg{Name: name, Err: err}
		}
		_, plan, err := scripting.RunbookPlan(daemon.ClientOps{Client: client}, name, map[string]string{})
		return runbookPlanMsg{Name: name, Description: loaded.Description, Plan: plan, Err: err}
	}
}

// handleRunbookPlanMsg opens the confirm overlay for a successful dry-run.
func (m Model) handleRunbookPlanMsg(msg runbookPlanMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.setFlash("runbook "+msg.Name+": "+msg.Err.Error(), true, 4*time.Second)
	}
	m.runbookConfirm = &runbookConfirmState{Name: msg.Name, Description: msg.Description, Plan: msg.Plan}
	m.state = StateRunbookConfirm
	return m, nil
}

// handleKeyRunbookConfirm approves (y) or cancels (esc) the previewed plan.
func (m Model) handleKeyRunbookConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pending := m.runbookConfirm
	switch msg.String() {
	case "y":
		m.runbookConfirm = nil
		m.state = StateNormal
		if pending == nil || pending.Plan == nil || len(pending.Plan.Steps) == 0 {
			return m, m.setFlash("runbook "+nameOf(pending)+": nothing to run", true, 3*time.Second)
		}
		client := m.client
		name := pending.Name
		steps := make([]batch.Step, 0, len(pending.Plan.Steps))
		for _, ps := range pending.Plan.Steps {
			steps = append(steps, ps.Step)
		}
		return m, func() tea.Msg {
			// Execute the EXACT previewed steps (revalidated against the live
			// fleet by Execute — the world may have moved since the preview).
			result, err := batch.Execute(daemon.ClientOps{Client: client}, batch.Batch{Actions: steps})
			return runbookRunMsg{Name: name, Result: result, Err: err}
		}
	case "esc", "ctrl+c", "n":
		m.runbookConfirm = nil
		m.state = StateNormal
		return m, nil
	}
	return m, nil
}

// handleRunbookRunMsg reports the structured outcome as a flash.
func (m Model) handleRunbookRunMsg(msg runbookRunMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.setFlash("runbook "+msg.Name+": "+msg.Err.Error(), true, 5*time.Second)
	}
	r := msg.Result
	text := fmt.Sprintf("runbook %s: %s — %d ok", msg.Name, r.Outcome, r.Executed)
	isErr := r.Outcome != batch.OutcomeCompleted
	if isErr {
		text += fmt.Sprintf(", %d failed, %d skipped (remainder resubmittable)", r.Failed, r.Skipped)
	}
	return m, m.setFlash(text, isErr, 5*time.Second)
}

// renderRunbookConfirm renders the preview overlay.
func (m Model) renderRunbookConfirm(width int) string {
	pending := m.runbookConfirm
	if pending == nil {
		return ""
	}
	var steps []ui.CopilotPermissionBatchStep
	if pending.Plan != nil {
		for _, ps := range pending.Plan.Steps {
			steps = append(steps, ui.CopilotPermissionBatchStep{
				Index:  ps.Index,
				Op:     string(ps.Step.Op),
				Target: targetLabel(ps),
				Detail: ps.Detail,
				Risk:   string(ps.Risk),
			})
		}
	}
	return ui.RunbookConfirmView(pending.Name, pending.Description, steps, width)
}

func targetLabel(ps batch.PlannedStep) string {
	if ps.Target.DisplayName != "" {
		short := ps.Target.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		return ps.Target.DisplayName + " (" + short + ")"
	}
	if ps.Target.SessionID != "" {
		return ps.Target.SessionID
	}
	return ps.Step.CWD
}

func nameOf(pending *runbookConfirmState) string {
	if pending == nil {
		return "?"
	}
	return pending.Name
}
