package daemon

import (
	"slices"
	"time"

	"github.com/huylenq/spirit/internal/agent"
	"github.com/huylenq/spirit/internal/claude"
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
		d.ledgerBaselined = true // an unchanged fleet is still a baseline
		return
	}
	old := d.sessions
	d.sessions = sessions
	d.version++
	d.mu.Unlock()

	// Perception ingest: diff the previous snapshot against the new truth.
	// The first poll after daemon start only seeds the baseline — a restart
	// must not replay session_started for the standing fleet.
	if d.ledgerBaselined {
		d.observeFleet(old, sessions)
	} else {
		d.ledgerBaselined = true
	}

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
		// Capture the session before removal for auto-synthesis + perception
		endSession := d.sessions[idx]
		endPaneID := endSession.PaneID
		endSessionID := endSession.SessionID
		d.sessions = append(d.sessions[:idx], d.sessions[idx+1:]...)
		d.version++
		sessions := d.sessions
		d.mu.Unlock()
		d.notifySubscribers(sessions)
		if !endSession.IsPhantom {
			d.signalSessionEnded(endSession)
		}
		if endSessionID != "" {
			go d.autoSynthesize(endPaneID, endSessionID)
			// Defer cleanup of debounce entry (after auto-synth has a chance to check it)
			go func() {
				time.Sleep(60 * time.Second)
				d.lastSynthMu.Lock()
				delete(d.lastSynthTime, endSessionID)
				d.lastSynthMu.Unlock()
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
	waitingRose := false
	waitingFell := false
	turnStarted := s.LastChanged // when the (possibly) ending turn last changed state

	// Session moved panes (e.g. --resume in a new pane)
	if nudge.PaneID != "" && s.PaneID != nudge.PaneID {
		s.PaneID = nudge.PaneID
		changed = true
	}

	status := claude.ParseStatus(nudge.Status)

	if nudge.Status != "" && s.Status != status {
		// Only a genuine turn completion (Stop) should trigger auto-synthesis.
		// A permission/elicitation Notification also flips to user-turn but carries
		// IsWaiting=true — that's a mid-turn pause, not a finished turn, so exclude it
		// or synthesis fires repeatedly while the user works through prompts.
		if status == claude.StatusUserTurn && s.Status == claude.StatusAgentTurn &&
			!(nudge.IsWaiting != nil && *nudge.IsWaiting) {
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
			waitingFell = true
			changed = true
		}
	}
	if nudge.StopReason != "" && s.StopReason != nudge.StopReason {
		s.StopReason = nudge.StopReason
		changed = true
	}
	if nudge.IsWaiting != nil && s.IsWaiting != *nudge.IsWaiting {
		s.IsWaiting = *nudge.IsWaiting
		if s.IsWaiting {
			waitingRose = true
			waitingFell = false
		} else {
			waitingFell = true
		}
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
	sCopy := *s // snapshot for post-unlock perception ingest
	d.version++
	sessions := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(sessions)

	// Perception ingest (edge-confirmed + content-anchored; see daemon_ingest.go).
	if becameUserTurn {
		d.signalTurnCompleted(sCopy, turnStarted)
	}
	if waitingRose {
		d.signalWaitingRise(sCopy)
	}
	if waitingFell && sessionID != "" {
		d.signalWaitingFall(sessionID)
	}

	if becameUserTurn && sessionID != "" {
		go d.autoSynthesize(paneID, sessionID)
	}
	return patchApplied
}

// sessionsEqual checks if two session slices are equivalent (same pane IDs, statuses, timestamps).
func sessionsEqual(a, b []agent.Session) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PaneID != b[i].PaneID ||
			a[i].Status != b[i].Status ||
			a[i].SessionID != b[i].SessionID ||
			a[i].LastChanged != b[i].LastChanged ||
			a[i].LaterID != b[i].LaterID ||
			!timePointerEqual(a[i].LaterWakeAt, b[i].LaterWakeAt) ||
			a[i].IsPhantom != b[i].IsPhantom ||
			a[i].SynthesizedTitle != b[i].SynthesizedTitle ||
			a[i].TitleDrift != b[i].TitleDrift ||
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
func (d *Daemon) refreshOverlaps(sessions []agent.Session) {
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

	// Perception ingest: overlap rising edges (deduped per session-set+file
	// anchor) and resolution once the fleet is overlap-free.
	d.observeOverlaps(overlaps, sessions)
}

// timePointerEqual compares two *time.Time pointers for equality.
func timePointerEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}
