package daemon

import (
	"encoding/json"
	"net"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

func (d *Daemon) handleSubscribe(conn net.Conn, enc *json.Encoder) {
	sub := d.addSubscriber()
	defer d.removeSubscriber(sub)

	// Send current state immediately
	sessions := d.currentSessions()
	resp := Response{
		Type:    RespSessions,
		Data:    marshalData(SessionsData{Sessions: sessions, Usage: d.currentUsage()}),
		Version: d.currentVersion(),
	}
	if err := enc.Encode(resp); err != nil {
		return
	}

	// Block and push updates
	for {
		select {
		case sessions := <-sub.ch:
			resp := Response{
				Type:    RespSessions,
				Data:    marshalData(SessionsData{Sessions: sessions, Usage: d.currentUsage()}),
				Version: d.currentVersion(),
			}
			if err := enc.Encode(resp); err != nil {
				return
			}
		case <-sub.done:
			return
		}
	}
}

func (d *Daemon) handleNudge(data json.RawMessage) *Response {
	var req NudgeData
	if err := json.Unmarshal(data, &req); err != nil || req.PaneID == "" {
		// Bare nudge without data — fall back to full poll
		d.nudge()
	} else {
		result := d.patchSession(req)
		if result == patchNotFound {
			// Pane not in sidebar yet — need full discovery
			d.nudge()
		}
		if result == patchDeduped {
			r := Response{Type: RespPong, Deduped: true}
			return &r
		}
	}
	r := Response{Type: RespPong}
	return &r
}

func (d *Daemon) handleSessions(data json.RawMessage) *Response {
	var filter SessionsFilterData
	if data != nil {
		if err := json.Unmarshal(data, &filter); err != nil {
			r := errResponse("bad data: " + err.Error())
			return &r
		}
	}

	sessions := d.currentSessions()

	// Filter out orchestrator sessions
	d.orchestratorMu.RLock()
	filtered := make([]claude.ClaudeSession, 0, len(sessions))
	for _, s := range sessions {
		if s.SessionID != "" && d.orchestratorIDs[s.SessionID] {
			continue
		}
		if filter.Status != "" {
			target := claude.ParseStatus(filter.Status)
			if s.Status != target {
				continue
			}
		}
		filtered = append(filtered, s)
	}
	d.orchestratorMu.RUnlock()

	r := resultResponse(SessionsData{Sessions: filtered})
	return &r
}

func (d *Daemon) handlePaneGeometry(data json.RawMessage) *Response {
	var req SessionNameData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	panes, err := tmux.ListPaneGeometry(req.SessionName)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(PaneGeometryData{Panes: panes})
	return &r
}
