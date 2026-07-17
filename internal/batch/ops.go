package batch

import (
	"github.com/huylenq/spirit/internal/agent"
)

// Ops is the narrow slice of daemon operations a batch needs — the SAME
// per-operation daemon paths every other surface uses (no forked logic).
// daemon.Client satisfies it through the ClientOps adapter (client_batch.go
// in internal/daemon); tests inject a fake.
type Ops interface {
	Sessions() ([]agent.Session, error)
	Send(sessionID, message string) error
	// Queue enqueues a message and returns the durable queue item id, stamping
	// the step's action id for ledger linkage.
	Queue(paneID, sessionID, message, actionID string) (itemID string, err error)
	// Spawn starts a new session and returns its id and pane.
	Spawn(provider agent.ProviderID, cwd, tmuxSession, message string, remoteControl bool) (sessionID, paneID string, err error)
	Kill(sessionID string) error
	SetTags(sessionID string, tags []string) error
	SetNote(sessionID, note string) error
	Later(paneID, sessionID string) error
	LaterKill(paneID string, pid int, sessionID string) error
	CommitOnly(paneID, sessionID string, pid int) error
	CommitAndDone(paneID, sessionID string, pid int) error
	// ReportActionFailure feeds a failed step into the daemon's perception
	// ledger as an action_failed signal (best-effort).
	ReportActionFailure(actionID, operation, sessionID, errMsg string) error
}
