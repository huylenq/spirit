package daemon

import (
	"encoding/json"
	"log"
	"strings"
	"syscall"
	"time"

	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/tmux"
)

// commitCmd is the slash command sent to Claude Code to trigger a git commit.
const commitCmd = "/commit-commands:commit your changes, constraint to involved files on this session"

func (d *Daemon) handleCommit(data json.RawMessage, killOnDone, runSimplify bool) *Response {
	var req CommitDoneData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	var firstCmd string
	var tag string
	if runSimplify {
		// simplify → commit → kill: send /simplify first
		firstCmd = "/simplify"
		tag = "simplify-commit-done"
	} else {
		firstCmd = commitCmd
		if killOnDone {
			tag = "commit-done"
		} else {
			tag = "commit"
		}
	}
	if err := tmux.SendKeysLiteral(req.PaneID, firstCmd); err != nil {
		r := errResponse("send failed: " + err.Error())
		return &r
	}
	d.commitDoneMu.Lock()
	d.commitDonePanes[req.SessionID] = commitDoneEntry{
		PaneID:        req.PaneID,
		PID:           req.PID,
		KillOnDone:    killOnDone,
		SimplifyPhase: runSimplify, // true = waiting for /simplify; false = waiting for commit
		CreatedAt:     time.Now(),
	}
	d.commitDoneMu.Unlock()
	d.nudge()
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

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
