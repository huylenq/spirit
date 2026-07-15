package batch

import (
	"fmt"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/receipt"
)

// PlannedStep is one step of a dry-run plan: the resolved target, its risk
// class, and whether it is an approval point (destructive per Decision 5).
type PlannedStep struct {
	Index         int            `json:"index"` // 1-based, matches receipt order
	Operation     string         `json:"operation"`
	Target        receipt.Target `json:"target"`
	Detail        string         `json:"detail"`
	Risk          RiskClass      `json:"risk"`
	ApprovalPoint bool           `json:"approval_point,omitempty"`
	Step          Step           `json:"step"`
}

// Plan is the dry-run result: ordered, target-resolved, risk-classified steps.
// Producing a Plan executes NOTHING.
type Plan struct {
	BatchID          string        `json:"batch_id"`
	ResumeOf         string        `json:"resume_of,omitempty"`
	OnError          OnError       `json:"on_error"`
	Steps            []PlannedStep `json:"steps"`
	DestructiveCount int           `json:"destructive_count"`
	Preview          bool          `json:"preview"` // true: nothing was executed
}

// waitPhases are the lifecycle phases a wait step accepts (identical to the
// CLI/Lua/MCP wait vocabulary).
var waitPhases = map[string]bool{"idle": true, "working": true, "cycle": true}

// capabilityFor maps a step onto the daemon's capability gate for its target's
// provider. Ops without a daemon-side gate return "".
func capabilityFor(op Op) agent.Capability {
	switch op {
	case OpQueue:
		return agent.CapabilityQueue
	case OpLater:
		return agent.CapabilityLater
	case OpKill:
		return agent.CapabilityKill
	case OpCommit:
		return agent.CapabilityCommit
	case OpSpawn:
		return agent.CapabilitySpawn
	}
	return ""
}

// BuildPlan validates a batch against the live fleet and returns its dry-run
// plan. Fail-fast (spec/W8 hard boundary): ANY invalid step — unknown op,
// missing fields, unknown session, capability-gated op for that session's
// provider — rejects the whole batch with a precise error; a batch is never
// half-planned, and Plan never executes anything.
func BuildPlan(ops Ops, b Batch) (*Plan, error) {
	if len(b.Actions) == 0 {
		return nil, fmt.Errorf("batch has no actions")
	}
	if b.OnError == "" {
		b.OnError = StopOnError
	}
	sessions, err := ops.Sessions()
	if err != nil {
		return nil, fmt.Errorf("resolve fleet: %w", err)
	}
	byID := make(map[string]*agent.Session, len(sessions))
	for i := range sessions {
		if sessions[i].SessionID != "" {
			byID[sessions[i].SessionID] = &sessions[i]
		}
	}
	registry := agent.NewDefaultRegistry()

	plan := &Plan{
		BatchID:  NewBatchID(),
		ResumeOf: b.ResumeOf,
		OnError:  b.OnError,
		Preview:  true,
	}
	for i, step := range b.Actions {
		ps, err := planStep(registry, byID, i, step)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", i+1, step.Op, err)
		}
		if ps.Risk == RiskDestructive {
			plan.DestructiveCount++
		}
		plan.Steps = append(plan.Steps, ps)
	}
	return plan, nil
}

func planStep(registry *agent.Registry, byID map[string]*agent.Session, index int, step Step) (PlannedStep, error) {
	ps := PlannedStep{
		Index:     index + 1,
		Operation: step.operationName(),
		Detail:    step.Detail(),
		Risk:      step.Risk(),
		Step:      step,
	}
	ps.ApprovalPoint = ps.Risk == RiskDestructive

	switch step.Op {
	case OpSend, OpQueue:
		if step.Message == "" {
			return ps, fmt.Errorf("message is required")
		}
	case OpTag, OpNote, OpLater, OpKill, OpCommit:
		// target-only ops; field validation below
	case OpWait:
		if !waitPhases[step.Phase] {
			return ps, fmt.Errorf("phase must be idle, working, or cycle; got %q", step.Phase)
		}
		if step.TimeoutSeconds < 0 {
			return ps, fmt.Errorf("timeout_seconds must be >= 0")
		}
	case OpSpawn:
		if step.CWD == "" {
			return ps, fmt.Errorf("cwd is required")
		}
		provider := agent.ProviderID(step.Provider)
		if step.Provider == "" {
			provider = agent.ProviderClaude
		}
		seed := agent.Session{Provider: provider}
		if err := registry.Require(seed, agent.CapabilitySpawn); err != nil {
			return ps, fmt.Errorf("provider %q: %v", step.Provider, err)
		}
		ps.Target = receipt.Target{ResolvedBy: receipt.ResolvedExplicit}
		return ps, nil
	default:
		return ps, fmt.Errorf("unknown op %q", step.Op)
	}

	// Everything but spawn targets an existing session.
	if step.SessionID == "" {
		return ps, fmt.Errorf("session_id is required")
	}
	s, ok := byID[step.SessionID]
	if !ok {
		return ps, fmt.Errorf("unknown session %s", step.SessionID)
	}
	ps.Target = receipt.Target{
		SessionID:   s.SessionID,
		ResolvedBy:  receipt.ResolvedExplicit,
		PaneID:      s.PaneID,
		DisplayName: s.DisplayName(),
	}
	if cap := capabilityFor(step.Op); cap != "" {
		gated := *s
		// Normalize like the daemon's discovery does — a missing provider is Claude.
		gated.Provider = agent.ParseProviderID(string(s.Provider))
		if err := registry.Require(gated, cap); err != nil {
			return ps, fmt.Errorf("%v", err)
		}
	}
	return ps, nil
}
