// Package receipt defines the ActionReceipt schema (spec Decision 5): the durable,
// structured response every Spirit side-effect operation returns. It is the tool-call
// result payload for the `spirit mcp` server (W3) and is designed to be retrofitted
// onto the agent CLI and Lua surfaces (W5/W8) without duplication.
//
// This is a leaf package on purpose — it imports nothing from the rest of Spirit so
// every surface (daemon, mcpserver, cmd/spirit, scripting) can adopt it with no risk
// of an import cycle. Observed post-action state is modeled with primitive fields
// rather than embedding agent.Session, keeping the type dependency-free and stable.
package receipt

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Outcome is the delivery result of an operation. It answers "did the instruction
// reach its target", distinct from the observed post-action state which answers
// "what did the fleet look like afterward" (reconciliation, per Decision 5).
type Outcome string

const (
	// OutcomeDelivered — a direct imperative was delivered to the target now.
	OutcomeDelivered Outcome = "delivered"
	// OutcomeQueued — the instruction was accepted for delivery when the target is idle.
	OutcomeQueued Outcome = "queued"
	// OutcomeCompleted — a non-messaging operation (tag, note, kill, later, commit) succeeded.
	OutcomeCompleted Outcome = "completed"
	// OutcomeFailed — the daemon rejected the operation; see Error.
	OutcomeFailed Outcome = "failed"
	// OutcomeSkipped — the operation was never attempted because an earlier
	// step of its batch failed under stop-on-failure semantics (W8). The step
	// is returned in the batch result's remainder for resubmission.
	OutcomeSkipped Outcome = "skipped"
)

// Resolution records how the target session id was arrived at. In W3 every tool
// takes an explicit id, so this is always ResolvedExplicit; W2's selected-session
// scope will introduce ResolvedSelected et al.
type Resolution string

const (
	ResolvedExplicit Resolution = "explicit_id" // caller passed the session id verbatim
	ResolvedSelected Resolution = "selected"    // resolved from the client's selected session (future, W2)
)

// Target identifies the session an action was aimed at and how it was resolved.
type Target struct {
	SessionID   string     `json:"session_id,omitempty"`
	ResolvedBy  Resolution `json:"resolved_by,omitempty"`
	PaneID      string     `json:"pane_id,omitempty"`
	DisplayName string     `json:"display_name,omitempty"`
}

// ObservedState is a compact snapshot of the target session captured after the
// operation, for reconciliation. A clean RPC response is not proof a coding agent
// consumed the instruction (Decision 5) — the caller compares this against intent.
type ObservedState struct {
	Status    string `json:"status,omitempty"`     // "agent-turn" | "user-turn"
	IsWaiting bool   `json:"is_waiting,omitempty"`  // waiting on a permission/input prompt
	QueueLen  int    `json:"queue_len,omitempty"`   // pending queued messages
	Alive     bool   `json:"alive"`                 // session still present in the fleet
}

// ActionReceipt is the structured result of a Spirit side-effect operation.
// Field set follows spec Decision 5 (action_id, request_id, target, operation,
// accepted_at, delivery_outcome, observed_state_after, error) plus the params that
// produced it, so a receipt is self-explanatory without its originating call.
type ActionReceipt struct {
	ActionID      string         `json:"action_id"`
	RequestID     string         `json:"request_id,omitempty"`
	Operation     string         `json:"operation"`
	Target        Target         `json:"target"`
	Params        map[string]any `json:"params,omitempty"`
	DeliveryOutcome Outcome      `json:"delivery_outcome"`
	AcceptedAt    string         `json:"accepted_at"` // RFC3339 UTC
	ObservedState *ObservedState `json:"observed_state_after,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// New starts a receipt for an operation against a target, stamping a fresh action
// id and the acceptance time. Callers set DeliveryOutcome, ObservedState, and (on
// failure) Error before returning it.
func New(operation string, target Target) *ActionReceipt {
	return &ActionReceipt{
		ActionID:   NewActionID(),
		Operation:  operation,
		Target:     target,
		AcceptedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// Fail marks the receipt failed with an error message and returns it, for one-line
// error returns from operation handlers.
func (r *ActionReceipt) Fail(err error) *ActionReceipt {
	r.DeliveryOutcome = OutcomeFailed
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

// NewActionID returns a short, collision-resistant action id like
// "act_9f3c1a2b7d4e". Exported so surfaces that assemble receipt-shaped
// results field-by-field (the Lua wrappers) mint ids from the same vocabulary.
func NewActionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fail-fast fallback: a time-based id is still unique enough to correlate.
		return "act_" + time.Now().UTC().Format("20060102150405.000000")
	}
	return "act_" + hex.EncodeToString(b[:])
}
