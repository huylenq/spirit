// Package batch is the single action schema every Spirit surface shares (W8,
// spec Decisions 3/4/5): a validated batch of operations submitted as ONE unit.
// `Plan` is the dry-run — targets resolved against the live fleet, steps
// ordered, risk class per Decision 5's approval table, approval points marked,
// NOTHING executed. `Execute` runs the same validated steps through the
// existing per-operation daemon paths and returns one ActionReceipt per step
// with partial-failure semantics (stop-on-failure by default; the unexecuted
// remainder is returned verbatim so resume = resubmit).
//
// This package must stay importable by the daemon (permission-payload
// rendering), so it never imports internal/daemon — operations arrive through
// the narrow Ops interface, satisfied by a thin adapter over daemon.Client.
package batch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/huylenq/spirit/internal/agent"
)

// Op names one batchable operation. The set mirrors the per-op daemon paths;
// nothing here has logic of its own.
type Op string

const (
	OpSend   Op = "send"
	OpQueue  Op = "queue"
	OpTag    Op = "tag"
	OpNote   Op = "note"
	OpLater  Op = "later"
	OpKill   Op = "kill"
	OpCommit Op = "commit"
	OpSpawn  Op = "spawn"
	OpWait   Op = "wait"
)

// Step is one operation inside a batch — the single schema shared by MCP
// tools, the agent CLI, Lua, runbook emission, plan preview, and execution.
type Step struct {
	Op        Op     `json:"op"`
	SessionID string `json:"session_id,omitempty"`
	// send / queue
	Message string `json:"message,omitempty"`
	// tag
	Tags []string `json:"tags,omitempty"`
	// note
	Note string `json:"note,omitempty"`
	// later: also kill the pane
	Kill bool `json:"kill,omitempty"`
	// commit: auto-kill after the commit completes
	Done bool `json:"done,omitempty"`
	// spawn
	CWD         string `json:"cwd,omitempty"`
	Provider    string `json:"provider,omitempty"`
	TmuxSession string `json:"tmux_session,omitempty"`
	// wait
	Phase          string `json:"phase,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// OnError selects the partial-failure policy.
type OnError string

const (
	// StopOnError (default): steps after a failed one are NOT executed; they
	// get "skipped" receipts and are returned as the resubmittable remainder.
	StopOnError OnError = "stop"
	// ContinueOnError: independent fan-out — failed steps get failed receipts,
	// later steps still run, the remainder stays empty.
	ContinueOnError OnError = "continue"
)

// Batch is the submitted unit: ordered steps plus the failure policy.
// ResumeOf links a remainder resubmission back to its original batch.
type Batch struct {
	Actions  []Step  `json:"actions"`
	OnError  OnError `json:"on_error,omitempty"`
	ResumeOf string  `json:"resume_of,omitempty"`
}

// RiskClass follows spec Decision 5's approval-class table.
type RiskClass string

const (
	RiskReadOnly    RiskClass = "read_only"
	RiskReversible  RiskClass = "reversible"
	RiskDestructive RiskClass = "destructive"
)

// Risk classifies one step per Decision 5: wait observes; kill, later+kill,
// and commit+done are destructive; everything else is reversible coordination.
func (s Step) Risk() RiskClass {
	switch s.Op {
	case OpWait:
		return RiskReadOnly
	case OpKill:
		return RiskDestructive
	case OpLater:
		if s.Kill {
			return RiskDestructive
		}
		return RiskReversible
	case OpCommit:
		if s.Done {
			return RiskDestructive
		}
		return RiskReversible
	default:
		return RiskReversible
	}
}

// operationName is the receipt/plan operation label for a step, matching the
// MCP tool vocabulary where one exists.
func (s Step) operationName() string {
	switch s.Op {
	case OpSend:
		return "send_message"
	case OpQueue:
		return "queue_message"
	case OpTag:
		return "set_tags"
	case OpNote:
		return "set_note"
	case OpLater:
		return "later_session"
	case OpKill:
		return "kill_session"
	case OpCommit:
		return "commit_session"
	case OpSpawn:
		return "spawn_session"
	case OpWait:
		return "wait_session"
	}
	return string(s.Op)
}

// Detail renders the one-line human summary of a step (shared by plan preview,
// the permission overlay, and the TUI runbook confirm).
func (s Step) Detail() string {
	switch s.Op {
	case OpSend:
		return fmt.Sprintf("send %q", truncate(s.Message, 60))
	case OpQueue:
		return fmt.Sprintf("queue %q", truncate(s.Message, 60))
	case OpTag:
		return "set tags [" + strings.Join(s.Tags, ", ") + "]"
	case OpNote:
		return fmt.Sprintf("set note %q", truncate(s.Note, 60))
	case OpLater:
		if s.Kill {
			return "mark later + kill pane"
		}
		return "mark later"
	case OpKill:
		return "kill session"
	case OpCommit:
		if s.Done {
			return "commit + auto-kill when done"
		}
		return "commit"
	case OpSpawn:
		provider := s.Provider
		if provider == "" {
			provider = string(agent.ProviderClaude)
		}
		d := fmt.Sprintf("spawn %s session in %s", provider, s.CWD)
		if s.Message != "" {
			d += fmt.Sprintf(" with prompt %q", truncate(s.Message, 40))
		}
		return d
	case OpWait:
		return fmt.Sprintf("wait for phase %s", s.Phase)
	}
	return string(s.Op)
}

// ParseBatch decodes a batch from JSON, accepting either the full Batch object
// or a bare step array. Fail-fast: unknown fields in steps are tolerated (the
// schema may grow) but a structurally invalid document is a precise error.
func ParseBatch(raw []byte) (Batch, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return Batch{}, fmt.Errorf("empty batch")
	}
	var b Batch
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(raw, &b.Actions); err != nil {
			return Batch{}, fmt.Errorf("invalid batch step array: %w", err)
		}
	} else {
		if err := json.Unmarshal(raw, &b); err != nil {
			return Batch{}, fmt.Errorf("invalid batch: %w", err)
		}
	}
	if b.OnError == "" {
		b.OnError = StopOnError
	}
	if b.OnError != StopOnError && b.OnError != ContinueOnError {
		return Batch{}, fmt.Errorf("invalid on_error %q (want %q or %q)", b.OnError, StopOnError, ContinueOnError)
	}
	if len(b.Actions) == 0 {
		return Batch{}, fmt.Errorf("batch has no actions")
	}
	return b, nil
}

// NewBatchID mints a batch id like "bat_9f3c1a2b7d4e". Receipts carry it in
// their RequestID field so every per-step receipt is traceable to its batch.
func NewBatchID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "bat_" + time.Now().UTC().Format("20060102150405.000000")
	}
	return "bat_" + hex.EncodeToString(b[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
