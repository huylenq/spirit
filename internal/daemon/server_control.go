package daemon

import (
	"encoding/json"
	"log"
	"syscall"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/tmux"
)

// commitCmd is the slash command sent to Claude Code to trigger a git commit.
const commitCmd = "/commit-commands:commit your changes, constraint to involved files on this session"

func (d *Daemon) handleCommit(data json.RawMessage, killOnDone bool) *Response {
	var req CommitDoneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	tag := "commit"
	if killOnDone {
		tag = "commit-done"
	}
	session, ok := d.sessionByPaneID(req.PaneID)
	if !ok {
		r := errResponse("session not found for pane: " + req.PaneID)
		return &r
	}
	if err := d.require(session, agent.CapabilityCommit); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	if err := d.sendCommand(session, commitCmd); err != nil {
		r := errResponse("send failed: " + err.Error())
		return &r
	}
	d.commitDoneMu.Lock()
	d.commitDonePanes[req.SessionID] = commitDoneEntry{
		PaneID:     req.PaneID,
		PID:        req.PID,
		KillOnDone: killOnDone,
		CreatedAt:  time.Now(),
	}
	d.commitDoneMu.Unlock()
	d.nudge()
	log.Printf("%s: registered session %s (pane %s)", tag, req.SessionID, req.PaneID)
	r := resultResponse("ok")
	return &r
}

// handleQueueCommitDone queues the standard commit message behind any pending
// work on the session and registers a *persistent* kill-on-commit watcher.
// Unlike handleCommit, this returns immediately without typing into tmux — useful
// from macros that want to chain "run X first, then commit-and-done" without
// blocking the macro on the X cycle. The watcher survives intermediate
// working→idle cycles (e.g. /simplify finishing) and only resolves when an
// actual commit is detected.
func (d *Daemon) handleQueueCommitDone(data json.RawMessage) *Response {
	var req CommitDoneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	session, ok := d.sessionByPaneID(req.PaneID)
	if !ok {
		r := errResponse("session not found for pane: " + req.PaneID)
		return &r
	}
	if err := d.require(session, agent.CapabilityCommit); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	d.queueMu.Lock()
	d.queuePanes[req.SessionID] = append(d.queuePanes[req.SessionID], agent.QueueItem{
		ID:         agent.NewQueueItemID(),
		Message:    commitCmd,
		EnqueuedAt: time.Now().UTC(),
	})
	msgs := d.queuePanes[req.SessionID]
	queueErr := claude.WriteQueueItems(req.SessionID, msgs)
	d.queueMu.Unlock()
	if queueErr != nil {
		r := errResponse("write queue: " + queueErr.Error())
		return &r
	}
	d.commitDoneMu.Lock()
	d.commitDonePanes[req.SessionID] = commitDoneEntry{
		PaneID:     req.PaneID,
		PID:        req.PID,
		KillOnDone: true,
		Persistent: true,
		CreatedAt:  time.Now(),
	}
	d.commitDoneMu.Unlock()
	d.nudge()
	log.Printf("queue-commit-done: registered session %s (pane %s, queue=%d)", req.SessionID, req.PaneID, len(msgs))
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

func (d *Daemon) handleKill(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	sessions := d.currentSessions()
	var found *agent.Session
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
	if err := d.require(*found, agent.CapabilityKill); err != nil {
		r := errResponse(err.Error())
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
	providerID := req.Provider
	if providerID == "" {
		providerID = agent.ProviderClaude
	}
	provider, err := d.providers.Resolve(providerID)
	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	seed := agent.Session{Provider: providerID, CWD: req.CWD}
	if err := d.require(seed, agent.CapabilitySpawn); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	launchCmd, err := provider.Lifecycle(seed).LaunchCommand(seed, agent.LaunchOptions{Message: req.Message})
	if err != nil {
		r := errResponse("launch command: " + err.Error())
		return &r
	}
	var paneID string
	if req.SplitFromPane != "" {
		// Split a new pane next to the caller's pane in the same window.
		p, err := tmux.SplitWindow(req.SplitFromPane, req.CWD)
		if err != nil {
			r := errResponse("split window: " + err.Error())
			return &r
		}
		paneID = p
	} else {
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

		p, err := tmux.NewWindow(tmuxSession, req.CWD)
		if err != nil {
			r := errResponse("new window: " + err.Error())
			return &r
		}
		paneID = p
	}

	if err := tmux.SendKeysLiteral(paneID, launchCmd); err != nil {
		r := errResponse("send agent command: " + err.Error())
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

func (d *Daemon) handleSend(data json.RawMessage) *Response {
	var req SendData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	// Resolve sessionID → paneID
	sessions := d.currentSessions()
	var session *agent.Session
	for i := range sessions {
		s := &sessions[i]
		if s.SessionID == req.SessionID {
			session = s
			break
		}
	}
	if session == nil {
		r := errResponse("session not found: " + req.SessionID)
		return &r
	}
	if err := d.sendPrompt(*session, req.Message); err != nil {
		r := errResponse("send failed: " + err.Error())
		return &r
	}
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleRelay(data json.RawMessage) *Response {
	var req RelayData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	session, ok := d.sessionByPaneID(req.PaneID)
	if !ok {
		r := errResponse("session not found for pane: " + req.PaneID)
		return &r
	}
	capability := req.Capability
	if capability == "" {
		capability = agent.CapabilityRelayPrompt
	}
	if err := d.require(session, capability); err != nil {
		r := errResponse("relay failed: " + err.Error())
		return &r
	}
	var err error
	if capability == agent.CapabilityRelayCommand || capability == agent.CapabilityRelayBang {
		err = d.sendCommand(session, req.Message)
	} else {
		err = d.sendPrompt(session, req.Message)
	}
	if err != nil {
		r := errResponse("relay failed: " + err.Error())
		return &r
	}
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
	d.pendingPromptPanes[req.PaneID] = pendingPromptEntry{Prompt: req.Prompt, PlanMode: req.PlanMode, CreatedAt: time.Now()}
	d.pendingPromptMu.Unlock()
	d.nudge()
	log.Printf("pending-prompt: registered pane %s", req.PaneID)
	r := resultResponse("ok")
	return &r
}
