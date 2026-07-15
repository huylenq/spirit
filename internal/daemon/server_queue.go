package daemon

import (
	"encoding/json"
	"log"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
)

func (d *Daemon) handleQueue(data json.RawMessage) *Response {
	var req QueueData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	session, ok := d.sessionByPaneID(req.PaneID)
	if !ok {
		r := errResponse("session not found for pane: " + req.PaneID)
		return &r
	}
	if err := d.require(session, agent.CapabilityQueue); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	item := agent.QueueItem{
		ID:         agent.NewQueueItemID(),
		Message:    req.Message,
		ActionID:   req.ActionID,
		EnqueuedAt: time.Now().UTC(),
	}
	d.queueMu.Lock()
	d.queuePanes[req.SessionID] = append(d.queuePanes[req.SessionID], item)
	items := d.queuePanes[req.SessionID]
	err := claude.WriteQueueItems(req.SessionID, items)
	d.queueMu.Unlock()
	if err != nil {
		r := errResponse("write queue: " + err.Error())
		return &r
	}
	d.nudge()
	log.Printf("queue: appended %s to session %s (%d total)", item.ID, req.SessionID, len(items))
	r := resultResponse(QueueResultData{ItemID: item.ID})
	return &r
}

func (d *Daemon) handleCancelQueueItem(data json.RawMessage) *Response {
	var req CancelQueueItemData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.queueMu.Lock()
	items := d.queuePanes[req.SessionID]
	if req.Index < 0 || req.Index >= len(items) {
		d.queueMu.Unlock()
		r := errResponse("index out of range")
		return &r
	}
	items = append(items[:req.Index], items[req.Index+1:]...)
	if len(items) == 0 {
		delete(d.queuePanes, req.SessionID)
		claude.RemoveQueueMessage(req.SessionID)
	} else {
		d.queuePanes[req.SessionID] = items
		claude.WriteQueueItems(req.SessionID, items) //nolint:errcheck
	}
	d.queueMu.Unlock()
	d.nudge()
	log.Printf("queue: removed item %d from session %s (%d remaining)", req.Index, req.SessionID, len(items))
	r := resultResponse("ok")
	return &r
}
