package daemon

import (
	"encoding/json"
	"log"
	"syscall"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

func (d *Daemon) handleLater(data json.RawMessage) *Response {
	var req LaterData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.removeExistingLater(req.PaneID)
	bm := d.buildLaterRecordFromSession(req.PaneID)
	applyWait(&bm, req.Wait)
	if err := claude.WriteLaterRecord(bm); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	d.nudge()
	log.Printf("later: marked pane %s (wait=%s)", req.PaneID, req.Wait)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleLaterKill(data json.RawMessage) *Response {
	var req LaterKillData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	d.removeExistingLater(req.PaneID)
	bm := d.buildLaterRecordFromSession(req.PaneID)
	applyWait(&bm, req.Wait)
	if err := claude.WriteLaterRecord(bm); err != nil {
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
	log.Printf("later+kill: marked later and killed pane %s (wait=%s)", req.PaneID, req.Wait)
	r := resultResponse("ok")
	return &r
}

// applyWait parses a duration string and sets WakeAt on the Later record.
func applyWait(bm *claude.LaterRecord, wait string) {
	if wait == "" {
		return
	}
	d, err := time.ParseDuration(wait)
	if err != nil || d <= 0 {
		return
	}
	t := time.Now().Add(d)
	bm.WakeAt = &t
}

func (d *Daemon) handleUnlater(data json.RawMessage) *Response {
	var req UnlaterData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	// Find the Later record to get its paneID for status cleanup
	bm, _ := claude.ReadLaterRecord(req.LaterID)
	claude.RemoveLaterRecord(req.LaterID)
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
	log.Printf("unlater: removed later %s", req.LaterID)
	r := resultResponse("ok")
	return &r
}

func (d *Daemon) handleOpenLater(data json.RawMessage) *Response {
	var req OpenLaterData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	// Read the record before removing it so we can resume by session ID.
	record, _ := claude.ReadLaterRecord(req.LaterID)
	paneID, err := tmux.NewWindow(req.TmuxSession, req.CWD)
	if err != nil {
		r := errResponse("new window: " + err.Error())
		return &r
	}
	cmd := "claude --dangerously-skip-permissions"
	if record != nil && record.SessionID != "" {
		cmd = "claude --dangerously-skip-permissions --resume " + shellQuote(record.SessionID)
	}
	tmux.SendKeysLiteral(paneID, cmd) //nolint:errcheck
	claude.RemoveLaterRecord(req.LaterID)
	d.nudge()
	log.Printf("open-later: created window in %s at %s, pane %s", req.TmuxSession, req.CWD, paneID)
	r := resultResponse("ok")
	return &r
}

// buildLaterRecordFromSession extracts session metadata from current sessions to create a Later record.
func (d *Daemon) buildLaterRecordFromSession(paneID string) claude.LaterRecord {
	sessions := d.currentSessions()
	bm := claude.LaterRecord{
		ID:        claude.GenerateLaterID(),
		PaneID:    paneID,
		CreatedAt: time.Now(),
	}
	for _, s := range sessions {
		if s.PaneID == paneID {
			bm.Project = s.Project
			bm.CWD = s.CWD
			bm.GitBranch = s.GitBranch
			bm.SynthesizedTitle = s.SynthesizedTitle
			bm.ProblemType = s.ProblemType
			bm.CustomTitle = s.CustomTitle
			bm.FirstMessage = s.FirstMessage
			bm.SessionID = s.SessionID
			break
		}
	}
	return bm
}

// resolveExpiredLaters removes Later records whose WakeAt has passed.
// Skips disk I/O entirely when no cached session has a WakeAt set.
func (d *Daemon) resolveExpiredLaters() {
	hasWaked := false
	for _, s := range d.sessions {
		if s.LaterWakeAt != nil {
			hasWaked = true
			break
		}
	}
	if !hasWaked {
		return
	}
	records, err := claude.ReadAllLaterRecords()
	if err != nil {
		log.Printf("later: read records for expiry check: %v", err)
		return
	}
	now := time.Now()
	for _, bm := range records {
		if bm.WakeAt != nil && now.After(*bm.WakeAt) {
			claude.RemoveLaterRecord(bm.ID)
			log.Printf("later: auto-expired %s (pane %s)", bm.ID, bm.PaneID)
		}
	}
}

// removeExistingLater removes any existing Later record for a pane,
// using the in-memory session data to avoid a disk scan.
func (d *Daemon) removeExistingLater(paneID string) {
	for _, s := range d.currentSessions() {
		if s.PaneID == paneID && s.LaterID != "" {
			claude.RemoveLaterRecord(s.LaterID)
			return
		}
	}
}
