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
			bm.ProblemType = s.ProblemType
			bm.CustomTitle = s.CustomTitle
			bm.FirstMessage = s.FirstMessage
			bm.SessionID = s.SessionID
			break
		}
	}
	return bm
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
