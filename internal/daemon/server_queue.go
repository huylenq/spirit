package daemon

import (
	"encoding/json"
	"log"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (d *Daemon) handleQueue(data json.RawMessage) *Response {
	var req QueueData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.queueMu.Lock()
	d.queuePanes[req.SessionID] = append(d.queuePanes[req.SessionID], req.Message)
	msgs := d.queuePanes[req.SessionID]
	err := claude.WriteQueueMessages(req.SessionID, msgs)
	d.queueMu.Unlock()
	if err != nil {
		r := errResponse("write queue: " + err.Error())
		return &r
	}
	d.nudge()
	log.Printf("queue: appended to session %s (%d total)", req.SessionID, len(msgs))
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleCancelQueueItem(data json.RawMessage) *Response {
	var req CancelQueueItemData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.queueMu.Lock()
	msgs := d.queuePanes[req.SessionID]
	if req.Index < 0 || req.Index >= len(msgs) {
		d.queueMu.Unlock()
		r := errResponse("index out of range")
		return &r
	}
	msgs = append(msgs[:req.Index], msgs[req.Index+1:]...)
	if len(msgs) == 0 {
		delete(d.queuePanes, req.SessionID)
		claude.RemoveQueueMessage(req.SessionID)
	} else {
		d.queuePanes[req.SessionID] = msgs
		claude.WriteQueueMessages(req.SessionID, msgs) //nolint:errcheck
	}
	d.queueMu.Unlock()
	d.nudge()
	log.Printf("queue: removed item %d from session %s (%d remaining)", req.Index, req.SessionID, len(msgs))
	r := resultResponse("ok")
	return &r
}
