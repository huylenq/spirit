package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"syscall"
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

	case ReqSynthesize:
		return d.handleSynthesize(req.Data)

	case ReqSynthesizeAll:
		return d.handleSynthesizeAll(req.Data)

	case ReqHookEvents:
		return d.handleHookEvents(req.Data)

	case ReqRawTranscript:
		return d.handleRawTranscript(req.Data)

	case ReqPaneGeometry:
		return d.handlePaneGeometry(req.Data)

	case ReqLater:
		return d.handleLater(req.Data)

	case ReqLaterKill:
		return d.handleLaterKill(req.Data)

	case ReqUnlater:
		return d.handleUnlater(req.Data)

	case ReqOpenLater:
		return d.handleOpenLater(req.Data)

	case ReqRenameWindow:
		return d.handleRenameWindow(req.Data)

	case ReqCommitOnly:
		return d.handleCommit(req.Data, false)

	case ReqCommitDone:
		return d.handleCommit(req.Data, true)

	case ReqCancelCommitDone:
		return d.handleCancelCommitDone(req.Data)

	case ReqQueue:
		return d.handleQueue(req.Data)

	case ReqCancelQueue:
		return d.handleCancelQueue(req.Data)

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
		if !d.patchSession(req) {
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

func (d *Daemon) handleSynthesize(data json.RawMessage) *Response {
	var req PaneSessionData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.synthesizingMu.Lock()
	d.synthesizingPanes[req.PaneID] = true
	d.synthesizingMu.Unlock()

	summary, fromCache, err := claude.Summarize(req.SessionID)

	d.synthesizingMu.Lock()
	delete(d.synthesizingPanes, req.PaneID)
	d.synthesizingMu.Unlock()
	d.nudge()

	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	// Send /rename to pane when fresh synthesis produces a headline
	if !fromCache && summary != nil && summary.Headline != "" {
		tmux.SendKeys(req.PaneID, "/rename "+summary.Headline, "Enter")
	}
	r := resultResponse(SynthesizeResultData{
		PaneID:    req.PaneID,
		Summary:   summary,
		FromCache: fromCache,
	})
	return &r
}

func (d *Daemon) handleSynthesizeAll(data json.RawMessage) *Response {
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

	// Mark all target panes as synthesizing
	d.synthesizingMu.Lock()
	for _, s := range sessions {
		if s.PaneID != skipPaneID && s.SessionID != "" {
			d.synthesizingPanes[s.PaneID] = true
		}
	}
	d.synthesizingMu.Unlock()

	var results []SynthesizeResultData
	var done []string
	for _, s := range sessions {
		if s.PaneID == skipPaneID || s.SessionID == "" {
			continue
		}
		summary, fromCache, err := claude.Summarize(s.SessionID)
		done = append(done, s.PaneID)
		if err != nil {
			log.Printf("synthesize %s: %v", s.SessionID, err)
			continue
		}
		if !fromCache && summary != nil && summary.Headline != "" {
			tmux.SendKeys(s.PaneID, "/rename "+summary.Headline, "Enter")
		}
		results = append(results, SynthesizeResultData{
			PaneID:    s.PaneID,
			Summary:   summary,
			FromCache: fromCache,
		})
	}
	d.synthesizingMu.Lock()
	for _, paneID := range done {
		delete(d.synthesizingPanes, paneID)
	}
	d.synthesizingMu.Unlock()
	d.nudge()

	r := resultResponse(SynthesizeAllResultData{Results: results})
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

func (d *Daemon) handleRawTranscript(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	raw, err := claude.ReadRawTranscript(req.SessionID)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(RawTranscriptData{JSON: raw})
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

// removeExistingBookmark removes any existing Later bookmark for a pane,
// using the in-memory session data to avoid a disk scan.
func (d *Daemon) removeExistingBookmark(paneID string) {
	for _, s := range d.currentSessions() {
		if s.PaneID == paneID && s.LaterBookmarkID != "" {
			claude.RemoveLaterBookmark(s.LaterBookmarkID)
			return
		}
	}
}

func (d *Daemon) handleLater(data json.RawMessage) *Response {
	var req LaterData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.removeExistingBookmark(req.PaneID)
	bm := d.buildBookmarkFromSession(req.PaneID)
	if err := claude.WriteLaterBookmark(bm); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	d.nudge()
	log.Printf("later: bookmarked pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleLaterKill(data json.RawMessage) *Response {
	var req LaterKillData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.removeExistingBookmark(req.PaneID)
	bm := d.buildBookmarkFromSession(req.PaneID)
	if err := claude.WriteLaterBookmark(bm); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	if req.PID > 0 {
		syscall.Kill(req.PID, syscall.SIGTERM) //nolint:errcheck
	}
	tmux.KillPane(req.PaneID) //nolint:errcheck
	claude.RemoveStatus(req.PaneID)
	d.nudge()
	log.Printf("later+kill: bookmarked and killed pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleUnlater(data json.RawMessage) *Response {
	var req UnlaterData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	// Find the bookmark to get its paneID for status cleanup
	bm, _ := claude.ReadLaterBookmark(req.BookmarkID)
	claude.RemoveLaterBookmark(req.BookmarkID)
	if bm != nil {
		// Restore status: check if Claude process is running for this pane
		restoredStatus := claude.StatusUserTurn
		for _, s := range d.currentSessions() {
			if s.PaneID == bm.PaneID && s.PID > 0 {
				restoredStatus = claude.StatusAgentTurn
				break
			}
		}
		claude.WriteStatus(bm.PaneID, restoredStatus)
	}
	d.nudge()
	log.Printf("unlater: removed bookmark %s", req.BookmarkID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleOpenLater(data json.RawMessage) *Response {
	var req OpenLaterData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	paneID, err := tmux.NewWindow(req.TmuxSession, req.CWD)
	if err != nil {
		r := errResponse("new window: " + err.Error())
		return &r
	}
	tmux.SendKeysLiteral(paneID, "claude") //nolint:errcheck
	claude.RemoveLaterBookmark(req.BookmarkID)
	d.nudge()
	log.Printf("open-later: created window in %s at %s, pane %s", req.TmuxSession, req.CWD, paneID)
	r := resultResponse("ok")
	return &r
}

// buildBookmarkFromSession extracts session metadata from current sessions to create a bookmark.
func (d *Daemon) buildBookmarkFromSession(paneID string) claude.LaterBookmark {
	sessions := d.currentSessions()
	bm := claude.LaterBookmark{
		ID:        claude.GenerateBookmarkID(),
		PaneID:    paneID,
		CreatedAt: time.Now(),
	}
	for _, s := range sessions {
		if s.PaneID == paneID {
			bm.Project = s.Project
			bm.CWD = s.CWD
			bm.GitBranch = s.GitBranch
			bm.Headline = s.Headline
			bm.CustomTitle = s.CustomTitle
			bm.FirstMessage = s.FirstMessage
			bm.SessionID = s.SessionID
			break
		}
	}
	return bm
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

func (d *Daemon) handleCommit(data json.RawMessage, killOnDone bool) *Response {
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
	// Register the pending commit and nudge so subscribers see CommitDonePending immediately
	d.commitDoneMu.Lock()
	d.commitDonePanes[req.PaneID] = commitDoneEntry{PaneID: req.PaneID, PID: req.PID, KillOnDone: killOnDone, CreatedAt: time.Now()}
	d.commitDoneMu.Unlock()
	d.nudge()
	tag := "commit"
	if killOnDone {
		tag = "commit-done"
	}
	log.Printf("%s: registered pane %s", tag, req.PaneID)
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

func (d *Daemon) handleQueue(data json.RawMessage) *Response {
	var req QueueData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	if err := claude.WriteQueueMessage(req.PaneID, req.Message); err != nil {
		r := errResponse("write queue: " + err.Error())
		return &r
	}
	d.queueMu.Lock()
	d.queuePanes[req.PaneID] = req.Message
	d.queueMu.Unlock()
	d.nudge()
	log.Printf("queue: registered pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleCancelQueue(data json.RawMessage) *Response {
	var req PaneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.queueMu.Lock()
	delete(d.queuePanes, req.PaneID)
	d.queueMu.Unlock()
	claude.RemoveQueueMessage(req.PaneID)
	d.nudge()
	log.Printf("queue: cancelled pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}




