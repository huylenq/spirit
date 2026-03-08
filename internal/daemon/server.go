package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return // listener closed
		}
		d.clientConnected()
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	defer d.clientDisconnected()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			enc.Encode(Response{Type: RespError, Error: "invalid JSON: " + err.Error()})
			continue
		}

		resp := d.dispatch(req, conn, enc)
		if resp == nil {
			// subscribe handler manages its own writes
			return
		}
		enc.Encode(resp)
	}
}

func (d *Daemon) dispatch(req Request, conn net.Conn, enc *json.Encoder) *Response {
	switch req.Type {
	case ReqPing:
		r := Response{Type: RespPong}
		return &r

	case ReqNudge:
		return d.handleNudge(req.Data)

	case ReqSubscribe:
		d.handleSubscribe(conn, enc)
		return nil // subscribe manages its own lifecycle

	case ReqTranscript:
		return d.handleTranscript(req.Data)

	case ReqDiffStats:
		return d.handleDiffStats(req.Data)

	case ReqSummary:
		return d.handleSummary(req.Data)

	case ReqSummarize:
		return d.handleSummarize(req.Data)

	case ReqSummarizeAll:
		return d.handleSummarizeAll(req.Data)

	case ReqHookEvents:
		return d.handleHookEvents(req.Data)

	case ReqPaneGeometry:
		return d.handlePaneGeometry(req.Data)

	case ReqDefer:
		return d.handleDefer(req.Data)

	case ReqUndefer:
		return d.handleUndefer(req.Data)

	case ReqRenameWindow:
		return d.handleRenameWindow(req.Data)

	case ReqCommitDone:
		return d.handleCommitDone(req.Data)

	case ReqCancelCommitDone:
		return d.handleCancelCommitDone(req.Data)

	default:
		r := Response{Type: RespError, Error: "unknown request type: " + req.Type}
		return &r
	}
}

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
		status := claude.ParseStatus(req.Status)
		if !d.patchSession(req.PaneID, status, req.LastUserMessage) {
			// Pane not in session list yet — need full discovery
			d.nudge()
		}
	}
	r := Response{Type: RespPong}
	return &r
}

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

func (d *Daemon) handleSummarize(data json.RawMessage) *Response {
	var req PaneSessionData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.summarizingMu.Lock()
	d.summarizingPanes[req.PaneID] = true
	d.summarizingMu.Unlock()

	summary, fromCache, err := claude.Summarize(req.SessionID)

	d.summarizingMu.Lock()
	delete(d.summarizingPanes, req.PaneID)
	d.summarizingMu.Unlock()
	d.nudge()

	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	// Send /rename to pane when fresh summarization produces a headline
	if !fromCache && summary != nil && summary.Headline != "" {
		tmux.SendKeys(req.PaneID, "/rename "+summary.Headline, "Enter")
	}
	r := resultResponse(SummarizeResultData{
		PaneID:    req.PaneID,
		Summary:   summary,
		FromCache: fromCache,
	})
	return &r
}

func (d *Daemon) handleSummarizeAll(data json.RawMessage) *Response {
	var req SkipPaneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}

	sessions := d.currentSessions()

	// Find the most recently changed session to skip
	skipPaneID := req.SkipPaneID
	if skipPaneID == "" {
		var latestTime time.Time
		for _, s := range sessions {
			if s.LastChanged.After(latestTime) {
				latestTime = s.LastChanged
				skipPaneID = s.PaneID
			}
		}
	}

	// Mark all target panes as summarizing
	d.summarizingMu.Lock()
	for _, s := range sessions {
		if s.PaneID != skipPaneID && s.SessionID != "" {
			d.summarizingPanes[s.PaneID] = true
		}
	}
	d.summarizingMu.Unlock()

	var results []SummarizeResultData
	var done []string
	for _, s := range sessions {
		if s.PaneID == skipPaneID || s.SessionID == "" {
			continue
		}
		summary, fromCache, err := claude.Summarize(s.SessionID)
		done = append(done, s.PaneID)
		if err != nil {
			log.Printf("summarize %s: %v", s.SessionID, err)
			continue
		}
		if !fromCache && summary != nil && summary.Headline != "" {
			tmux.SendKeys(s.PaneID, "/rename "+summary.Headline, "Enter")
		}
		results = append(results, SummarizeResultData{
			PaneID:    s.PaneID,
			Summary:   summary,
			FromCache: fromCache,
		})
	}
	d.summarizingMu.Lock()
	for _, paneID := range done {
		delete(d.summarizingPanes, paneID)
	}
	d.summarizingMu.Unlock()
	d.nudge()

	r := resultResponse(SummarizeAllResultData{Results: results})
	return &r
}

func (d *Daemon) handleHookEvents(data json.RawMessage) *Response {
	var req PaneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	events, _ := claude.ReadHookEvents(req.PaneID)
	r := resultResponse(HookEventsData{Events: events})
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

func (d *Daemon) handleDefer(data json.RawMessage) *Response {
	var req DeferData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	until := time.Now().Add(time.Duration(req.Minutes) * time.Minute)
	if err := claude.WriteDeferUntil(req.PaneID, until); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := Response{Type: RespResult, Data: marshalData("ok")}
	return &r
}

func (d *Daemon) handleUndefer(data json.RawMessage) *Response {
	var req PaneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	claude.Undefer(req.PaneID)
	r := Response{Type: RespResult, Data: marshalData("ok")}
	return &r
}

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

func (d *Daemon) handleCommitDone(data json.RawMessage) *Response {
	var req CommitDoneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	// Send the commit command to the pane
	if err := tmux.SendKeysLiteral(req.PaneID, "/commit-commands:commit"); err != nil {
		r := errResponse("send failed: " + err.Error())
		return &r
	}
	// Register the pending commit-done and nudge so subscribers see CommitDonePending immediately
	d.commitDoneMu.Lock()
	d.commitDonePanes[req.PaneID] = commitDoneEntry{PaneID: req.PaneID, PID: req.PID}
	d.commitDoneMu.Unlock()
	d.nudge()
	log.Printf("commit-done: registered pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleCancelCommitDone(data json.RawMessage) *Response {
	var req PaneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.commitDoneMu.Lock()
	delete(d.commitDonePanes, req.PaneID)
	d.commitDoneMu.Unlock()
	log.Printf("commit-done: cancelled pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}
