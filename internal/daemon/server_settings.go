package daemon

import (
	"encoding/json"
	"log"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

func (d *Daemon) handleRenameWindow(data json.RawMessage) *Response {
	var req RenameWindowData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}

	sessions := d.currentSessions()
	panes, err := claude.GatherWindowPanes(req.SessionName, req.WindowIndex, sessions)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	name, err := claude.GenerateWindowName(panes)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	if err := tmux.RenameWindow(req.SessionName, req.WindowIndex, name); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(RenameResultData{Name: name})
	return &r
}

func (d *Daemon) handleSetTags(data json.RawMessage) *Response {
	var req SetTagsData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	if req.SessionID == "" {
		r := errResponse("sessionID required")
		return &r
	}
	if err := claude.WriteTags(req.SessionID, req.Tags); err != nil {
		r := errResponse("write tags: " + err.Error())
		return &r
	}
	d.mu.Lock()
	for i := range d.sessions {
		if d.sessions[i].SessionID == req.SessionID {
			d.sessions[i].Tags = req.Tags
			break
		}
	}
	sessions := d.sessions
	d.version++
	d.mu.Unlock()
	d.notifySubscribers(sessions)
	r := resultResponse(nil)
	return &r
}

func (d *Daemon) handleDigest() *Response {
	digest := claude.ReadCachedDigest()
	r := resultResponse(DigestData{Digest: digest})
	return &r
}

func (d *Daemon) handleRegisterOrchestrator(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.orchestratorMu.Lock()
	d.orchestratorIDs[req.SessionID] = true
	d.orchestratorMu.Unlock()
	log.Printf("orchestrator: registered %s", req.SessionID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleUnregisterOrchestrator(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.orchestratorMu.Lock()
	delete(d.orchestratorIDs, req.SessionID)
	d.orchestratorMu.Unlock()
	log.Printf("orchestrator: unregistered %s", req.SessionID)
	r := resultResponse("ok")
	return &r
}
