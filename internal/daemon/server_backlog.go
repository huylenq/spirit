package daemon

import (
	"encoding/json"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (d *Daemon) handleBacklogList(data json.RawMessage) *Response {
	var req BacklogListData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	backlogs, err := claude.ReadAllBacklog(req.CWD)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	if backlogs == nil {
		backlogs = []claude.Backlog{}
	}
	r := resultResponse(BacklogListResultData{Backlogs: backlogs})
	return &r
}

func (d *Daemon) handleBacklogCreate(data json.RawMessage) *Response {
	var req BacklogCreateData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	now := time.Now()
	backlog := claude.Backlog{
		ID:        claude.GenerateBacklogID(),
		Body:      req.Body,
		CWD:       req.CWD,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := claude.WriteBacklog(req.CWD, backlog); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(BacklogItemResultData{Backlog: backlog})
	return &r
}

func (d *Daemon) handleBacklogUpdate(data json.RawMessage) *Response {
	var req BacklogUpdateData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	now := time.Now()
	backlog := claude.Backlog{
		ID:        req.ID,
		Body:      req.Body,
		CWD:       req.CWD,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := claude.WriteBacklog(req.CWD, backlog); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(BacklogItemResultData{Backlog: backlog})
	return &r
}

func (d *Daemon) handleBacklogDelete(data json.RawMessage) *Response {
	var req BacklogDeleteData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	if err := claude.RemoveBacklog(req.CWD, req.ID); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse("ok")
	return &r
}
