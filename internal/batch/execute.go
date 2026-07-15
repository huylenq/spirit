package batch

import (
	"fmt"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/receipt"
)

// Outcome summarizes a batch execution.
type Outcome string

const (
	// OutcomeCompleted — every step succeeded.
	OutcomeCompleted Outcome = "completed"
	// OutcomePartial — some steps succeeded, some failed or were skipped.
	OutcomePartial Outcome = "partial"
	// OutcomeFailed — no step succeeded.
	OutcomeFailed Outcome = "failed"
)

// Result is a batch execution's structured record: one ActionReceipt per step
// (in submission order, including skipped steps) plus the resubmittable
// remainder. Resume = submit Remainder as a new batch with ResumeOf set to
// this BatchID — the receipt bundle IS the durable resume token; there is no
// server-side resume state.
type Result struct {
	BatchID   string                   `json:"batch_id"`
	ResumeOf  string                   `json:"resume_of,omitempty"`
	OnError   OnError                  `json:"on_error"`
	Outcome   Outcome                  `json:"outcome"`
	Executed  int                      `json:"executed"`
	Failed    int                      `json:"failed"`
	Skipped   int                      `json:"skipped"`
	Receipts  []*receipt.ActionReceipt `json:"receipts"`
	Remainder []Step                   `json:"remainder,omitempty"`
}

// waitPollInterval is the fleet polling cadence for wait steps; package var so
// tests can shrink it.
var waitPollInterval = 500 * time.Millisecond

const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 600 * time.Second
)

// Execute validates the batch exactly like BuildPlan (an invalid batch is
// rejected whole — never half-executed) and then runs the steps sequentially
// through the per-operation daemon paths. Execution-time failures (the fleet
// changed between plan and action) produce a failed receipt; under
// stop-on-failure the remaining steps get skipped receipts and are returned
// verbatim as the remainder. Every failed step is reported to the daemon's
// perception ledger (best-effort).
func Execute(ops Ops, b Batch) (*Result, error) {
	plan, err := BuildPlan(ops, b)
	if err != nil {
		return nil, err
	}
	res := &Result{
		BatchID:  plan.BatchID,
		ResumeOf: b.ResumeOf,
		OnError:  plan.OnError,
	}
	failedStep := 0 // 1-based index of the first failure; 0 = none
	for _, ps := range plan.Steps {
		if failedStep > 0 && plan.OnError == StopOnError {
			rcpt := receipt.New(ps.Operation, ps.Target)
			rcpt.RequestID = plan.BatchID
			rcpt.DeliveryOutcome = receipt.OutcomeSkipped
			rcpt.Error = fmt.Sprintf("skipped: step %d failed", failedStep)
			res.Receipts = append(res.Receipts, rcpt)
			res.Remainder = append(res.Remainder, ps.Step)
			res.Skipped++
			continue
		}
		rcpt := executeStep(ops, ps)
		rcpt.RequestID = plan.BatchID
		res.Receipts = append(res.Receipts, rcpt)
		if rcpt.DeliveryOutcome == receipt.OutcomeFailed {
			res.Failed++
			if failedStep == 0 {
				failedStep = ps.Index
			}
			// Feed the perception ledger so action_reconciled watches and the
			// away-delta see the failure (best-effort, never masks the receipt).
			ops.ReportActionFailure(rcpt.ActionID, rcpt.Operation, rcpt.Target.SessionID, rcpt.Error) //nolint:errcheck
			continue
		}
		res.Executed++
	}
	switch {
	case res.Failed == 0 && res.Skipped == 0:
		res.Outcome = OutcomeCompleted
	case res.Executed == 0:
		res.Outcome = OutcomeFailed
	default:
		res.Outcome = OutcomePartial
	}
	return res, nil
}

// executeStep runs one planned step through the per-op daemon path and stamps
// the receipt with the delivery outcome and observed post-action state
// (Decision 5 reconciliation).
func executeStep(ops Ops, ps PlannedStep) *receipt.ActionReceipt {
	step := ps.Step
	rcpt := receipt.New(ps.Operation, ps.Target)

	// Re-resolve the target at execution time: validation ran against the
	// plan-time fleet, and the world may have moved (that gap is exactly where
	// partial failure comes from).
	var target *agent.Session
	if step.Op != OpSpawn {
		s, ok := resolveSession(ops, step.SessionID)
		if !ok {
			return rcpt.Fail(fmt.Errorf("session not found: %s", step.SessionID))
		}
		target = &s
		rcpt.Target.PaneID = s.PaneID
		rcpt.Target.DisplayName = s.DisplayName()
	}

	switch step.Op {
	case OpSend:
		rcpt.Params = map[string]any{"message": step.Message}
		if err := ops.Send(step.SessionID, step.Message); err != nil {
			return rcpt.Fail(err)
		}
		rcpt.DeliveryOutcome = receipt.OutcomeDelivered

	case OpQueue:
		rcpt.Params = map[string]any{"message": step.Message}
		itemID, err := ops.Queue(target.PaneID, step.SessionID, step.Message, rcpt.ActionID)
		if err != nil {
			return rcpt.Fail(err)
		}
		if itemID != "" {
			rcpt.Params["queue_item_id"] = itemID
		}
		rcpt.DeliveryOutcome = receipt.OutcomeQueued

	case OpTag:
		rcpt.Params = map[string]any{"tags": step.Tags}
		if err := ops.SetTags(step.SessionID, step.Tags); err != nil {
			return rcpt.Fail(err)
		}
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted

	case OpNote:
		rcpt.Params = map[string]any{"note": step.Note}
		if err := ops.SetNote(step.SessionID, step.Note); err != nil {
			return rcpt.Fail(err)
		}
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted

	case OpLater:
		rcpt.Params = map[string]any{"kill": step.Kill}
		var err error
		if step.Kill {
			err = ops.LaterKill(target.PaneID, target.PID, step.SessionID)
		} else {
			err = ops.Later(target.PaneID, step.SessionID)
		}
		if err != nil {
			return rcpt.Fail(err)
		}
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted

	case OpKill:
		if err := ops.Kill(step.SessionID); err != nil {
			return rcpt.Fail(err)
		}
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted

	case OpCommit:
		rcpt.Params = map[string]any{"done": step.Done}
		var err error
		if step.Done {
			err = ops.CommitAndDone(target.PaneID, step.SessionID, target.PID)
		} else {
			err = ops.CommitOnly(target.PaneID, step.SessionID, target.PID)
		}
		if err != nil {
			return rcpt.Fail(err)
		}
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted

	case OpSpawn:
		provider := agent.ProviderID(step.Provider)
		if step.Provider == "" {
			provider = agent.ProviderClaude
		}
		rcpt.Params = map[string]any{"cwd": step.CWD, "provider": string(provider)}
		if step.Message != "" {
			rcpt.Params["message"] = step.Message
		}
		sessionID, paneID, err := ops.Spawn(provider, step.CWD, step.TmuxSession, step.Message)
		if err != nil {
			return rcpt.Fail(err)
		}
		rcpt.Target.SessionID = sessionID
		rcpt.Target.PaneID = paneID
		rcpt.DeliveryOutcome = receipt.OutcomeCompleted

	case OpWait:
		return executeWait(ops, ps, rcpt)

	default:
		return rcpt.Fail(fmt.Errorf("unknown op %q", step.Op))
	}

	rcpt.ObservedState = observe(ops, rcpt.Target.SessionID)
	return rcpt
}

// executeWait blocks until the target reaches the requested phase, mirroring
// the CLI/Lua/MCP wait semantics (idle=user-turn, working=agent-turn, cycle=
// working then idle). Timeout is a step failure; a session vanishing counts as
// failure too unless a vanish is what reconciliation expects (the caller can
// interpret the observed state).
func executeWait(ops Ops, ps PlannedStep, rcpt *receipt.ActionReceipt) *receipt.ActionReceipt {
	step := ps.Step
	timeout := defaultWaitTimeout
	if step.TimeoutSeconds > 0 {
		timeout = time.Duration(step.TimeoutSeconds) * time.Second
	}
	if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}
	rcpt.Params = map[string]any{"phase": step.Phase, "timeout_seconds": int(timeout / time.Second)}

	start := time.Now()
	deadline := start.Add(timeout)
	sawWorking := false
	everSeen := false
	for {
		s, ok := resolveSession(ops, step.SessionID)
		switch {
		case ok:
			everSeen = true
			if phaseReached(step.Phase, s.Status, &sawWorking) {
				rcpt.DeliveryOutcome = receipt.OutcomeCompleted
				rcpt.Params["waited_ms"] = time.Since(start).Milliseconds()
				rcpt.ObservedState = &receipt.ObservedState{
					Status:    s.Status.String(),
					IsWaiting: s.IsWaiting,
					QueueLen:  len(s.QueuePending),
					Alive:     true,
				}
				return rcpt
			}
		case everSeen:
			rcpt.ObservedState = &receipt.ObservedState{Alive: false}
			return rcpt.Fail(fmt.Errorf("session %s vanished while waiting for phase %s", step.SessionID, step.Phase))
		}
		if !time.Now().Before(deadline) {
			return rcpt.Fail(fmt.Errorf("session %s did not reach phase %s within %s", step.SessionID, step.Phase, timeout))
		}
		time.Sleep(waitPollInterval)
	}
}

// phaseReached mirrors the shared wait vocabulary. For "cycle" it records the
// first working observation so a later idle counts as a completed round trip.
func phaseReached(phase string, status agent.Status, sawWorking *bool) bool {
	switch phase {
	case "idle":
		return status == agent.StatusUserTurn
	case "working":
		return status == agent.StatusAgentTurn
	case "cycle":
		if status == agent.StatusAgentTurn {
			*sawWorking = true
		}
		return status == agent.StatusUserTurn && *sawWorking
	}
	return false
}

func resolveSession(ops Ops, id string) (agent.Session, bool) {
	sessions, err := ops.Sessions()
	if err != nil {
		return agent.Session{}, false
	}
	for _, s := range sessions {
		if s.SessionID == id {
			return s, true
		}
	}
	return agent.Session{}, false
}

// observe captures the target's post-action state for reconciliation
// (Alive:false after a kill is the expected observation).
func observe(ops Ops, id string) *receipt.ObservedState {
	if id == "" {
		return nil
	}
	s, ok := resolveSession(ops, id)
	if !ok {
		return &receipt.ObservedState{Alive: false}
	}
	return &receipt.ObservedState{
		Status:    s.Status.String(),
		IsWaiting: s.IsWaiting,
		QueueLen:  len(s.QueuePending),
		Alive:     true,
	}
}
