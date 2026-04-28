package daemon

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/tmux"
)

func (d *Daemon) handleRenameAllWindows() *Response {
	sessions := d.currentSessions()
	windows, err := claude.GatherAllClaudeWindowPanes(sessions)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	if len(windows) == 0 {
		r := resultResponse(RenameAllResultData{})
		return &r
	}

	names, err := claude.GenerateAllWindowNames(windows)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}

	renamed := make(map[string]string, len(names))
	var rerrs []string
	for k, name := range names {
		if err := tmux.RenameWindow(k.Session, k.WindowIndex, name); err != nil {
			rerrs = append(rerrs, fmt.Sprintf("%s: %v", k.String(), err))
			continue
		}
		renamed[k.String()] = name
	}
	r := resultResponse(RenameAllResultData{Renamed: renamed, Errors: rerrs})
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

func (d *Daemon) handleSetNote(data json.RawMessage) *Response {
	var req SetNoteData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	if req.SessionID == "" {
		r := errResponse("sessionID required")
		return &r
	}
	if err := claude.WriteNote(req.SessionID, req.Note); err != nil {
		r := errResponse("write note: " + err.Error())
		return &r
	}
	d.mu.Lock()
	for i := range d.sessions {
		if d.sessions[i].SessionID == req.SessionID {
			d.sessions[i].Note = req.Note
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
