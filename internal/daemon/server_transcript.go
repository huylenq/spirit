package daemon

import (
	"encoding/json"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (d *Daemon) handleTranscript(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	msgs, _ := claude.ReadUserMessages(req.SessionID)
	r := resultResponse(TranscriptData{Messages: msgs})
	return &r
}

func (d *Daemon) handleRawTranscript(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	entries, err := claude.ReadTranscriptEntries(req.SessionID)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(TranscriptEntriesData{Entries: entries})
	return &r
}

func (d *Daemon) handleDiffStats(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	stats := claude.ReadDiffStats(req.SessionID)
	r := resultResponse(DiffStatsData{Stats: stats})
	return &r
}

func (d *Daemon) handleDiffHunks(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	hunks := claude.ReadDiffHunks(req.SessionID)
	r := resultResponse(DiffHunksData{Hunks: hunks})
	return &r
}

func (d *Daemon) handleSummary(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	summary := claude.ReadCachedSummary(req.SessionID)
	r := resultResponse(SummaryData{Summary: summary, FromCache: true})
	return &r
}
