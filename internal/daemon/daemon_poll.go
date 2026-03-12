package daemon

import (
	"slices"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (d *Daemon) pollLoop(stop chan struct{}) {
	// Do one immediate poll before accepting clients
	d.poll()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			d.poll()
		case <-d.nudgeCh:
			d.poll()
		}
	}
}

func (d *Daemon) poll() {
	sessions, err := claude.DiscoverSessions()
	if err != nil {
		return
	}

	// Resolve pending commit-and-done operations
	d.resolveCommitDone(sessions)

	// Resolve pending queued messages
	d.resolveQueue(sessions)

	// Resolve pending prompts for newly spawned sessions
	d.resolvePendingPrompts(sessions)

	// Refresh overlap detection (pure in-memory, uses cached DiffStats)
	d.refreshOverlaps(sessions)

	// Annotate sessions with daemon-side pending states
	d.commitDoneMu.Lock()
	d.queueMu.Lock()
	d.synthesizingMu.Lock()
	d.overlapMu.RLock()
	for i := range sessions {
		sid := sessions[i].SessionID
		if sid != "" {
			if _, pending := d.commitDonePanes[sid]; pending {
				sessions[i].CommitDonePending = true
			}
			if msgs, pending := d.queuePanes[sid]; pending && len(msgs) > 0 {
				sessions[i].QueuePending = msgs
			}
		}
		if d.synthesizingPanes[sessions[i].PaneID] {
			sessions[i].SynthesizePending = true
		}
		if d.overlapPanes[sessions[i].PaneID] {
			sessions[i].HasOverlap = true
		}
	}
	d.overlapMu.RUnlock()
	d.synthesizingMu.Unlock()
	d.queueMu.Unlock()
	d.commitDoneMu.Unlock()

	claude.AssignAvatars(sessions)

	d.mu.Lock()
	if sessionsEqual(d.sessions, sessions) {
		d.mu.Unlock()
		return
	}
	d.sessions = sessions
	d.version++
	d.mu.Unlock()
	d.notifySubscribers(sessions)
}

type patchResult int

const (
	patchNotFound patchResult = iota
	patchApplied
	patchDeduped
)

// patchSession applies a targeted status update from a hook, bypassing full discovery.
// Matches by SessionID (primary) with PaneID fallback.
// Returns patchNotFound if the session isn't tracked, patchApplied if state changed,
// or patchDeduped if the nudge was redundant (no version bump, no subscriber notify).
func (d *Daemon) patchSession(nudge NudgeData) patchResult {
	d.mu.Lock()

	// Find session: match by SessionID first, then PaneID fallback
	idx := -1
	for i := range d.sessions {
		if nudge.SessionID != "" && d.sessions[i].SessionID == nudge.SessionID {
			idx = i
			break
		}
		if d.sessions[i].PaneID == nudge.PaneID {
			idx = i
			// Don't break — keep looking for a SessionID match
		}
	}

	// SessionEnd: remove session from memory
	if nudge.Remove {
		if idx < 0 {
			d.mu.Unlock()
			return patchNotFound
		}
		// Capture paneID + sessionID before removal for auto-synthesis
		endPaneID := d.sessions[idx].PaneID
		endSessionID := d.sessions[idx].SessionID
		d.sessions = append(d.sessions[:idx], d.sessions[idx+1:]...)
		d.version++
		sessions := d.sessions
		d.mu.Unlock()
		d.notifySubscribers(sessions)
		if endSessionID != "" {
			go d.autoSynthesize(endPaneID, endSessionID)
			// Defer cleanup of debounce entry (after auto-synth has a chance to check it)
			go func() {
				time.Sleep(35 * time.Second)
				d.autoSynthMu.Lock()
				delete(d.lastAutoSynthTime, endSessionID)
				d.autoSynthMu.Unlock()
			}()
		}
		return patchApplied
	}

	if idx < 0 {
		d.mu.Unlock()
		return patchNotFound
	}

	s := &d.sessions[idx]
	changed := false
	becameUserTurn := false

	// Session moved panes (e.g. --resume in a new pane)
	if nudge.PaneID != "" && s.PaneID != nudge.PaneID {
		s.PaneID = nudge.PaneID
		changed = true
	}

	status := claude.ParseStatus(nudge.Status)

	if nudge.Status != "" && s.Status != status {
		if status == claude.StatusUserTurn && s.Status == claude.StatusAgentTurn {
			becameUserTurn = true
		}
		s.Status = status
		changed = true
	}
	if nudge.LastUserMessage != "" && s.LastUserMessage != nudge.LastUserMessage {
		s.LastUserMessage = nudge.LastUserMessage
		changed = true
	}
	if status == claude.StatusAgentTurn {
		if nudge.PermissionMode != "" && s.PermissionMode != nudge.PermissionMode {
			s.PermissionMode = nudge.PermissionMode
			changed = true
		}
		if s.StopReason != "" {
			s.StopReason = ""
			changed = true
		}
		if s.IsWaiting {
			s.IsWaiting = false
			changed = true
		}
	}
	if nudge.StopReason != "" && s.StopReason != nudge.StopReason {
		s.StopReason = nudge.StopReason
		changed = true
	}
	if nudge.IsWaiting != nil && s.IsWaiting != *nudge.IsWaiting {
		s.IsWaiting = *nudge.IsWaiting
		changed = true
	}
	if nudge.IsGitCommit != nil && *nudge.IsGitCommit && !s.LastActionCommit {
		s.LastActionCommit = true
		changed = true
	}
	if nudge.IsFileEdit != nil && *nudge.IsFileEdit && s.LastActionCommit {
		s.LastActionCommit = false
		changed = true
	}
	if nudge.SkillSet && s.SkillName != nudge.SkillName {
		s.SkillName = nudge.SkillName
		changed = true
	}
	if nudge.Compacted {
		s.CompactCount++
		changed = true
	}

	if !changed {
		d.mu.Unlock()
		return patchDeduped
	}

	s.LastChanged = time.Now()
	paneID := s.PaneID
	sessionID := s.SessionID
	d.version++
	sessions := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(sessions)
	if becameUserTurn && sessionID != "" {
		go d.autoSynthesize(paneID, sessionID)
	}
	return patchApplied
}

// sessionsEqual checks if two session slices are equivalent (same pane IDs, statuses, timestamps).
func sessionsEqual(a, b []claude.ClaudeSession) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PaneID != b[i].PaneID ||
			a[i].Status != b[i].Status ||
			a[i].SessionID != b[i].SessionID ||
			a[i].LastChanged != b[i].LastChanged ||
			a[i].LaterBookmarkID != b[i].LaterBookmarkID ||
			a[i].IsPhantom != b[i].IsPhantom ||
			a[i].Headline != b[i].Headline ||
			a[i].LastUserMessage != b[i].LastUserMessage ||
			a[i].PermissionMode != b[i].PermissionMode ||
			a[i].LastActionCommit != b[i].LastActionCommit ||
			a[i].StopReason != b[i].StopReason ||
			a[i].SkillName != b[i].SkillName ||
			a[i].IsWaiting != b[i].IsWaiting ||
			a[i].CompactCount != b[i].CompactCount ||
			a[i].CommitDonePending != b[i].CommitDonePending ||
			a[i].SynthesizePending != b[i].SynthesizePending ||
			a[i].HasOverlap != b[i].HasOverlap ||
			!slices.Equal(a[i].QueuePending, b[i].QueuePending) ||
			!slices.Equal(a[i].Tags, b[i].Tags) ||
			a[i].Note != b[i].Note {
			return false
		}
	}
	return true
}

// refreshOverlaps detects file-level overlaps between sessions.
// Pure in-memory computation using cached DiffStats.
func (d *Daemon) refreshOverlaps(sessions []claude.ClaudeSession) {
	overlaps := claude.DetectOverlaps(sessions)
	panes := make(map[string]bool)
	for _, o := range overlaps {
		for _, pid := range o.PaneIDs {
			panes[pid] = true
		}
	}

	d.overlapMu.Lock()
	d.overlaps = overlaps
	d.overlapPanes = panes
	d.overlapMu.Unlock()
}
