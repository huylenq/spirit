package daemon

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/tmux"
)

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

	// Collect targets
	type target struct {
		paneID    string
		sessionID string
	}
	var targets []target
	for _, s := range sessions {
		if s.PaneID != skipPaneID && s.SessionID != "" {
			targets = append(targets, target{s.PaneID, s.SessionID})
		}
	}

	// Mark all target panes as synthesizing and nudge for immediate spinner display
	d.synthesizingMu.Lock()
	for _, t := range targets {
		d.synthesizingPanes[t.paneID] = true
	}
	d.synthesizingMu.Unlock()
	d.nudge()

	// Fan out with bounded concurrency (max 4 parallel)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []SynthesizeResultData

	for _, t := range targets {
		wg.Add(1)
		go func(paneID, sessionID string) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			summary, fromCache, err := claude.Summarize(sessionID)

			// Clear spinner for this pane immediately
			d.synthesizingMu.Lock()
			delete(d.synthesizingPanes, paneID)
			d.synthesizingMu.Unlock()
			d.nudge() // incremental UI update

			if err != nil {
				log.Printf("synthesize %s: %v", sessionID, err)
				return
			}
			if !fromCache && summary != nil && summary.Headline != "" {
				tmux.SendKeys(paneID, "/rename "+summary.Headline, "Enter")
			}
			mu.Lock()
			results = append(results, SynthesizeResultData{
				PaneID:    paneID,
				Summary:   summary,
				FromCache: fromCache,
			})
			mu.Unlock()
		}(t.paneID, t.sessionID)
	}
	wg.Wait()

	// Trigger digest after batch synthesis
	go d.triggerDigest()

	r := resultResponse(SynthesizeAllResultData{Results: results})
	return &r
}
