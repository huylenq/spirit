package daemon

import (
	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/batch"
)

// ClientOps adapts a daemon Client to the batch.Ops interface (W8), so batch
// plan/execute runs over the SAME per-operation daemon paths every other
// surface uses. It carries no logic beyond signature adaptation.
type ClientOps struct{ Client *Client }

var _ batch.Ops = ClientOps{}

func (o ClientOps) Sessions() ([]agent.Session, error) { return o.Client.Sessions("") }

func (o ClientOps) Send(sessionID, message string) error { return o.Client.Send(sessionID, message) }

func (o ClientOps) Queue(paneID, sessionID, message, actionID string) (string, error) {
	return o.Client.QueueMessage(paneID, sessionID, message, actionID)
}

func (o ClientOps) Spawn(provider agent.ProviderID, cwd, tmuxSession, message string) (string, string, error) {
	// No splitFromPane: batches have no caller pane context; open a new window
	// (matching the MCP spawn_session behavior).
	result, err := o.Client.SpawnProvider(provider, cwd, tmuxSession, message, "")
	if err != nil {
		return "", "", err
	}
	return result.SessionID, result.PaneID, nil
}

func (o ClientOps) Kill(sessionID string) error { return o.Client.Kill(sessionID) }

func (o ClientOps) SetTags(sessionID string, tags []string) error {
	return o.Client.SetTags(sessionID, tags)
}

func (o ClientOps) SetNote(sessionID, note string) error { return o.Client.SetNote(sessionID, note) }

func (o ClientOps) Later(paneID, sessionID string) error {
	return o.Client.Later(paneID, sessionID, "")
}

func (o ClientOps) LaterKill(paneID string, pid int, sessionID string) error {
	return o.Client.LaterKill(paneID, pid, sessionID, "")
}

func (o ClientOps) CommitOnly(paneID, sessionID string, pid int) error {
	return o.Client.CommitOnly(paneID, sessionID, pid)
}

func (o ClientOps) CommitAndDone(paneID, sessionID string, pid int) error {
	return o.Client.CommitAndDone(paneID, sessionID, pid)
}

func (o ClientOps) ReportActionFailure(actionID, operation, sessionID, errMsg string) error {
	return o.Client.ReportActionFailure(actionID, operation, sessionID, errMsg)
}
