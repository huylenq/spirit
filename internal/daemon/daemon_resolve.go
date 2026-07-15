package daemon

import (
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
	"github.com/huylenq/spirit/internal/tmux"
)

// resolveCommitDone checks pending commit-done operations against current sessions.
// If a session is back to Done: if committed → kill pane, else → drop the pending entry.
func (d *Daemon) resolveCommitDone(sessions []agent.Session) {
	d.commitDoneMu.Lock()
	defer d.commitDoneMu.Unlock()

	if len(d.commitDonePanes) == 0 {
		return
	}

	sessionByID := make(map[string]*agent.Session, len(sessions))
	for i := range sessions {
		if sessions[i].SessionID != "" {
			sessionByID[sessions[i].SessionID] = &sessions[i]
		}
	}

	for sessionID, entry := range d.commitDonePanes {
		s, exists := sessionByID[sessionID]
		if !exists {
			// Session disappeared — clean up
			log.Printf("commit-done: session %s disappeared, removing", sessionID)
			delete(d.commitDonePanes, sessionID)
			continue
		}
		if s.Status == claude.StatusAgentTurn {
			// Mark that we've seen the session enter agent-turn
			if !entry.SawWorking {
				entry.SawWorking = true
				d.commitDonePanes[sessionID] = entry
				log.Printf("commit-done: session %s now agent-turn", sessionID)
			}
			continue
		}
		if s.Status != claude.StatusUserTurn {
			continue
		}
		// Session is user-turn — but only resolve if it went through agent-turn first
		if !entry.SawWorking {
			// Expire if the session never reached agent-turn (e.g. user interrupted the prompt)
			if time.Since(entry.CreatedAt) > 30*time.Second {
				log.Printf("commit-done: session %s timed out waiting for agent-turn, removing", sessionID)
				delete(d.commitDonePanes, sessionID)
			}
			continue
		}
		shouldKill := false
		if s.LastActionCommit && entry.KillOnDone {
			log.Printf("commit-done: session %s committed, killing pane %s", sessionID, s.PaneID)
			shouldKill = true
		} else if s.LastActionCommit {
			log.Printf("commit: session %s committed", sessionID)
		} else {
			log.Printf("commit: session %s done but no commit detected", sessionID)
		}
		if shouldKill {
			if entry.PID > 0 {
				syscall.Kill(entry.PID, syscall.SIGTERM) //nolint:errcheck
			}
			tmux.KillPane(s.PaneID) //nolint:errcheck
			claude.RemoveSessionFiles(sessionID)
			claude.RemovePaneMapping(s.PaneID)
			delete(d.commitDonePanes, sessionID)
			continue
		}
		if entry.Persistent && !s.LastActionCommit {
			// Wait for a later cycle to produce the commit (e.g. queued /commit-commands:commit
			// runs after the current /simplify cycle finishes). Reset SawWorking and extend
			// the timeout window so we keep watching.
			entry.SawWorking = false
			entry.CreatedAt = time.Now()
			d.commitDonePanes[sessionID] = entry
			continue
		}
		delete(d.commitDonePanes, sessionID)
	}
}

// recoverQueue scans *.queue files on startup to rebuild the in-memory map.
func (d *Daemon) recoverQueue() {
	dir := claude.StatusDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".queue") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".queue")
		msgs := claude.ReadQueueMessages(sessionID)
		if len(msgs) > 0 {
			d.queuePanes[sessionID] = msgs
			log.Printf("queue: recovered session %s (%d messages)", sessionID, len(msgs))
		}
	}
}

// resolveQueue delivers the first queued message to sessions that have become Done.
// Only one message per session per poll cycle — the next waits for the session to
// become Done again after processing.
func (d *Daemon) resolveQueue(sessions []agent.Session) {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()

	if len(d.queuePanes) == 0 {
		return
	}

	sessionByID := make(map[string]*agent.Session, len(sessions))
	for i := range sessions {
		if sessions[i].SessionID != "" {
			sessionByID[sessions[i].SessionID] = &sessions[i]
		}
	}

	for sessionID, msgs := range d.queuePanes {
		s, exists := sessionByID[sessionID]
		if !exists {
			log.Printf("queue: session %s disappeared, removing", sessionID)
			delete(d.queuePanes, sessionID)
			claude.RemoveQueueMessage(sessionID)
			for _, msg := range msgs {
				d.signalQueueOutcome(false, sessionID, sessionID, "", msg, "session disappeared before delivery")
			}
			continue
		}
		if s.Status != claude.StatusUserTurn || len(msgs) == 0 {
			continue
		}
		// Session is Done — deliver the first message only
		if err := d.sendPrompt(*s, msgs[0]); err != nil {
			log.Printf("queue: send to pane %s (session %s) failed: %v (will retry)", s.PaneID, sessionID, err)
			d.signalQueueOutcome(false, sessionID, sessionID, s.Project, msgs[0], err.Error())
			continue
		}
		log.Printf("queue: delivered 1/%d to pane %s (session %s)", len(msgs), s.PaneID, sessionID)
		d.signalQueueOutcome(true, sessionID, sessionID, s.Project, msgs[0], "")
		remaining := msgs[1:]
		if len(remaining) == 0 {
			delete(d.queuePanes, sessionID)
			claude.RemoveQueueMessage(sessionID)
		} else {
			d.queuePanes[sessionID] = remaining
			claude.WriteQueueMessages(sessionID, remaining) //nolint:errcheck
		}
	}
}

// resolvePendingPrompts delivers initial prompts to newly spawned sessions
// once they reach user-turn (ready to receive input). Keyed by paneID since
// the sessionID doesn't exist yet when the prompt is registered.
func (d *Daemon) resolvePendingPrompts(sessions []agent.Session) {
	d.pendingPromptMu.Lock()
	defer d.pendingPromptMu.Unlock()

	if len(d.pendingPromptPanes) == 0 {
		return
	}

	sessionByPane := make(map[string]*agent.Session, len(sessions))
	for i := range sessions {
		sessionByPane[sessions[i].PaneID] = &sessions[i]
	}

	for paneID, entry := range d.pendingPromptPanes {
		// Expire entries that have been waiting too long (pane likely died)
		if time.Since(entry.CreatedAt) > 60*time.Second {
			log.Printf("pending-prompt: pane %s expired after 60s", paneID)
			delete(d.pendingPromptPanes, paneID)
			d.signalQueueOutcome(false, "pane:"+paneID, "", "", entry.Prompt, "pending prompt expired: pane never became ready")
			continue
		}
		s, exists := sessionByPane[paneID]
		if !exists {
			continue // pane not yet discovered
		}
		if s.Status != claude.StatusUserTurn {
			continue // session not ready yet
		}
		text := entry.Prompt
		if entry.PlanMode {
			if text != "" {
				text = "/plan " + text
			} else {
				text = "/plan"
			}
		}
		if err := d.sendPrompt(*s, text); err != nil {
			log.Printf("pending-prompt: send to pane %s failed: %v (will retry)", paneID, err)
			continue
		}
		log.Printf("pending-prompt: delivered to pane %s", paneID)
		delete(d.pendingPromptPanes, paneID)
		d.signalQueueOutcome(true, s.SessionID, s.SessionID, s.Project, text, "")
	}
}
