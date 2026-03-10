package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"strings"
	"syscall"
	"time"

	"sort"

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

	case ReqAllHookEffects:
		return d.handleAllHookEffects()

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

	case ReqCancelQueueItem:
		return d.handleCancelQueueItem(req.Data)

	case ReqDiffHunks:
		return d.handleDiffHunks(req.Data)

	case ReqPendingPrompt:
		return d.handlePendingPrompt(req.Data)

	case ReqRegisterOrchestrator:
		return d.handleRegisterOrchestrator(req.Data)

	case ReqUnregisterOrchestrator:
		return d.handleUnregisterOrchestrator(req.Data)

	case ReqSessions:
		return d.handleSessions(req.Data)

	case ReqSend:
		return d.handleSend(req.Data)

	case ReqSpawn:
		return d.handleSpawn(req.Data)

	case ReqKill:
		return d.handleKill(req.Data)

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
		result := d.patchSession(req)
		if result == patchNotFound {
			// Pane not in session list yet — need full discovery
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
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	events, _ := claude.ReadHookEvents(req.SessionID)
	r := resultResponse(HookEventsData{Events: events})
	return &r
}

func (d *Daemon) handleAllHookEffects() *Response {
	d.mu.RLock()
	sessions := d.sessions
	d.mu.RUnlock()

	var all []claude.GlobalHookEffect
	for _, s := range sessions {
		if s.SessionID == "" {
			continue
		}
		events, _ := claude.ReadHookEvents(s.SessionID)
		for _, ev := range events {
			if ev.Effect == "" || ev.Effect == claude.HookEffectNone {
				continue
			}
			all = append(all, claude.GlobalHookEffect{
				Time:      ev.Time,
				HookType:  ev.HookType,
				Effect:    ev.Effect,
				AnimalIdx: s.AvatarAnimalIdx,
				ColorIdx:  s.AvatarColorIdx,
			})
		}
	}
	// Sort by time descending (newest first). HH:MM:SS is lexicographically sortable.
	sort.Slice(all, func(i, j int) bool { return all[i].Time > all[j].Time })

	// Merge consecutive identical entries (same hook type, effect, and avatar)
	var merged []claude.GlobalHookEffect
	for _, e := range all {
		e.Count = 1
		if n := len(merged); n > 0 {
			prev := &merged[n-1]
			if prev.HookType == e.HookType && prev.Effect == e.Effect &&
				prev.AnimalIdx == e.AnimalIdx && prev.ColorIdx == e.ColorIdx {
				prev.Count++
				continue
			}
		}
		merged = append(merged, e)
	}
	if len(merged) > 25 {
		merged = merged[:25]
	}
	r := resultResponse(AllHookEffectsData{Effects: merged})
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
	if req.SessionID != "" {
		claude.RemoveSessionFiles(req.SessionID)
	}
	claude.RemovePaneMapping(req.PaneID)
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
		// Restore status from the in-memory session (hook-derived truth).
		// Don't infer from PID — Claude's process stays alive in user-turn too.
		for _, s := range d.currentSessions() {
			if s.PaneID == bm.PaneID && s.SessionID != "" {
				claude.WriteStatus(s.SessionID, s.Status)
				break
			}
		}
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
	if err := tmux.SendKeysLiteral(req.PaneID, "/commit-commands:commit only your changes"); err != nil {
		r := errResponse("send failed: " + err.Error())
		return &r
	}
	// Register the pending commit keyed by sessionID
	d.commitDoneMu.Lock()
	d.commitDonePanes[req.SessionID] = commitDoneEntry{PaneID: req.PaneID, PID: req.PID, KillOnDone: killOnDone, CreatedAt: time.Now()}
	d.commitDoneMu.Unlock()
	d.nudge()
	tag := "commit"
	if killOnDone {
		tag = "commit-done"
	}
	log.Printf("%s: registered session %s (pane %s)", tag, req.SessionID, req.PaneID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleCancelCommitDone(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.commitDoneMu.Lock()
	delete(d.commitDonePanes, req.SessionID)
	d.commitDoneMu.Unlock()
	log.Printf("commit-done: cancelled session %s", req.SessionID)
	r := resultResponse("ok")
	return &r
}

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

func (d *Daemon) handlePendingPrompt(data json.RawMessage) *Response {
	var req PendingPromptData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.pendingPromptMu.Lock()
	d.pendingPromptPanes[req.PaneID] = pendingPromptEntry{Prompt: req.Prompt, CreatedAt: time.Now()}
	d.pendingPromptMu.Unlock()
	d.nudge()
	log.Printf("pending-prompt: registered pane %s", req.PaneID)
	r := resultResponse("ok")
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

func (d *Daemon) handleSend(data json.RawMessage) *Response {
	var req SendData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	// Resolve sessionID → paneID
	sessions := d.currentSessions()
	var paneID string
	for _, s := range sessions {
		if s.SessionID == req.SessionID {
			paneID = s.PaneID
			break
		}
	}
	if paneID == "" {
		r := errResponse("session not found: " + req.SessionID)
		return &r
	}
	if err := tmux.SendKeysLiteral(paneID, req.Message); err != nil {
		r := errResponse("send failed: " + err.Error())
		return &r
	}
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleSpawn(data json.RawMessage) *Response {
	var req SpawnData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	if req.CWD == "" {
		r := errResponse("cwd is required")
		return &r
	}
	tmuxSession := req.TmuxSession
	if tmuxSession == "" {
		// Use first available tmux session
		panes, err := tmux.ListAllPanes()
		if err != nil || len(panes) == 0 {
			r := errResponse("no tmux sessions available")
			return &r
		}
		tmuxSession = panes[0].SessionName
	}

	paneID, err := tmux.NewWindow(tmuxSession, req.CWD)
	if err != nil {
		r := errResponse("new window: " + err.Error())
		return &r
	}

	// Launch claude in the new pane
	launchCmd := "claude --dangerously-skip-permissions"
	if req.Message != "" {
		launchCmd = "claude --dangerously-skip-permissions " + shellQuote(req.Message)
	}
	if err := tmux.SendKeysLiteral(paneID, launchCmd); err != nil {
		r := errResponse("send claude: " + err.Error())
		return &r
	}

	// Poll until session appears (up to 30s).
	// Nudge once to trigger immediate discovery, then rely on the normal poll loop.
	d.nudge()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		sessions := d.currentSessions()
		for _, s := range sessions {
			if s.PaneID == paneID && s.SessionID != "" {
				log.Printf("spawn: session %s appeared in pane %s", s.SessionID, paneID)
				r := resultResponse(SpawnResultData{SessionID: s.SessionID, PaneID: paneID})
				return &r
			}
		}
	}

	// Timed out but pane exists — return paneID without sessionID
	log.Printf("spawn: timed out waiting for session in pane %s", paneID)
	r := resultResponse(SpawnResultData{PaneID: paneID})
	return &r
}

func (d *Daemon) handleKill(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	sessions := d.currentSessions()
	var found *claude.ClaudeSession
	for i := range sessions {
		if sessions[i].SessionID == req.SessionID {
			found = &sessions[i]
			break
		}
	}
	if found == nil {
		r := errResponse("session not found: " + req.SessionID)
		return &r
	}
	if found.PID > 0 {
		syscall.Kill(found.PID, syscall.SIGTERM) //nolint:errcheck
	}
	tmux.KillPane(found.PaneID) //nolint:errcheck
	claude.RemoveSessionFiles(req.SessionID)
	claude.RemovePaneMapping(found.PaneID)
	d.nudge()
	log.Printf("kill: killed session %s (pane %s)", req.SessionID, found.PaneID)
	r := resultResponse("ok")
	return &r
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
