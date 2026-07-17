package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/runbook"
	"github.com/huylenq/spirit/internal/scripting"
)

// Batch + runbook tools (W8, spec Decisions 3/4/5). run_actions is ONE tool
// call, so Hermes raises at most ONE permission round-trip for the whole
// batch — one human decision covers every step, and the daemon renders the
// batch payload legibly in the approval overlay. plan_actions/plan_runbook
// are read-only dry-runs that execute nothing.

// apiOps adapts the MCP daemonAPI to batch.Ops so batches run over the same
// per-operation daemon paths the individual tools use.
type apiOps struct{ api daemonAPI }

var _ batch.Ops = apiOps{}

func (o apiOps) Sessions() ([]agent.Session, error) { return o.api.Sessions("") }
func (o apiOps) Send(sessionID, message string) error {
	return o.api.Send(sessionID, message)
}
func (o apiOps) Queue(paneID, sessionID, message, actionID string) (string, error) {
	return o.api.QueueMessage(paneID, sessionID, message, actionID)
}
func (o apiOps) Spawn(provider agent.ProviderID, cwd, tmuxSession, message string, remoteControl bool) (string, string, error) {
	result, err := o.api.SpawnProvider(provider, cwd, tmuxSession, message, "", remoteControl)
	if err != nil {
		return "", "", err
	}
	return result.SessionID, result.PaneID, nil
}
func (o apiOps) Kill(sessionID string) error { return o.api.Kill(sessionID) }
func (o apiOps) SetTags(sessionID string, tags []string) error {
	return o.api.SetTags(sessionID, tags)
}
func (o apiOps) SetNote(sessionID, note string) error { return o.api.SetNote(sessionID, note) }
func (o apiOps) Later(paneID, sessionID string) error { return o.api.Later(paneID, sessionID, "") }
func (o apiOps) LaterKill(paneID string, pid int, sessionID string) error {
	return o.api.LaterKill(paneID, pid, sessionID, "")
}
func (o apiOps) CommitOnly(paneID, sessionID string, pid int) error {
	return o.api.CommitOnly(paneID, sessionID, pid)
}
func (o apiOps) CommitAndDone(paneID, sessionID string, pid int) error {
	return o.api.CommitAndDone(paneID, sessionID, pid)
}
func (o apiOps) ReportActionFailure(actionID, operation, sessionID, errMsg string) error {
	return o.api.ReportActionFailure(actionID, operation, sessionID, errMsg)
}

// batchArgs is the shared argument shape of plan_actions / run_actions.
type batchArgs struct {
	Actions  []batch.Step `json:"actions"`
	OnError  string       `json:"on_error"`
	ResumeOf string       `json:"resume_of"`
}

func (a batchArgs) toBatch() (batch.Batch, error) {
	b := batch.Batch{Actions: a.Actions, OnError: batch.OnError(a.OnError), ResumeOf: a.ResumeOf}
	if b.OnError == "" {
		b.OnError = batch.StopOnError
	}
	if b.OnError != batch.StopOnError && b.OnError != batch.ContinueOnError {
		return b, fmt.Errorf("invalid on_error %q", a.OnError)
	}
	if len(b.Actions) == 0 {
		return b, fmt.Errorf("actions is required and must be non-empty")
	}
	return b, nil
}

func handlePlanActions(api daemonAPI, args json.RawMessage) (any, bool) {
	var a batchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errPayload("plan_actions", fmt.Errorf("invalid arguments: %w", err)), true
	}
	b, err := a.toBatch()
	if err != nil {
		return errPayload("plan_actions", err), true
	}
	plan, err := batch.BuildPlan(apiOps{api}, b)
	if err != nil {
		return errPayload("plan_actions", err), true
	}
	return plan, false
}

func handleRunActions(api daemonAPI, args json.RawMessage) (any, bool) {
	var a batchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errPayload("run_actions", fmt.Errorf("invalid arguments: %w", err)), true
	}
	b, err := a.toBatch()
	if err != nil {
		return errPayload("run_actions", err), true
	}
	result, err := batch.Execute(apiOps{api}, b)
	if err != nil {
		return errPayload("run_actions", err), true
	}
	// Partial/failed outcomes are still structured results the model must
	// read (receipts + remainder), not tool errors — isError stays false so
	// the payload is the record of what actually happened.
	return result, false
}

// --- runbooks ---

type runbookArgs struct {
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
}

func handleListRunbooks(api daemonAPI, args json.RawMessage) (any, bool) {
	return runbook.List(), false
}

func handleExplainRunbook(api daemonAPI, args json.RawMessage) (any, bool) {
	var a runbookArgs
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return errPayload("explain_runbook", fmt.Errorf("name is required")), true
	}
	rb, err := runbook.Load(a.Name)
	if err != nil {
		return errPayload("explain_runbook", err), true
	}
	return rb, false
}

func handlePlanRunbook(api daemonAPI, args json.RawMessage) (any, bool) {
	var a runbookArgs
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return errPayload("plan_runbook", fmt.Errorf("name is required")), true
	}
	_, plan, err := scripting.RunbookPlan(apiOps{api}, a.Name, a.Params)
	if err != nil {
		return errPayload("plan_runbook", err), true
	}
	return plan, false
}

func handleRunRunbook(api daemonAPI, args json.RawMessage) (any, bool) {
	var a runbookArgs
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return errPayload("run_runbook", fmt.Errorf("name is required")), true
	}
	_, result, err := scripting.RunbookRun(apiOps{api}, a.Name, a.Params)
	if err != nil {
		return errPayload("run_runbook", err), true
	}
	return result, false
}
