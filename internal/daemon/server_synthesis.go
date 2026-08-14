package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/huylenq/spirit/internal/agent"

	"github.com/huylenq/spirit/internal/claude"
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
	d.lastSynthMu.Lock()
	d.lastSynthTime[req.SessionID] = time.Now()
	d.lastSynthMu.Unlock()

	summary, fromCache, err := claude.Summarize(req.SessionID)

	d.synthesizingMu.Lock()
	delete(d.synthesizingPanes, req.PaneID)
	d.synthesizingMu.Unlock()
	d.nudge()

	if err != nil {
		r := errResponse(err.Error())
		return &r
	}
	if summary == nil || strings.TrimSpace(summary.SynthesizedTitle) == "" {
		r := errResponse("synthesis produced no title")
		return &r
	}
	if err := d.applySynthesizedTitle(req.PaneID, req.SessionID, summary.SynthesizedTitle); err != nil {
		r := errResponse("synthesized title but could not apply it: " + err.Error())
		return &r
	}
	r := resultResponse(SynthesizeResultData{
		PaneID:       req.PaneID,
		Summary:      summary,
		FromCache:    fromCache,
		TitleApplied: true,
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
	now := time.Now()
	d.lastSynthMu.Lock()
	for _, t := range targets {
		d.lastSynthTime[t.sessionID] = now
	}
	d.lastSynthMu.Unlock()
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
			applyErr := ""
			applied := false
			if summary == nil || strings.TrimSpace(summary.SynthesizedTitle) == "" {
				applyErr = "synthesis produced no title"
			} else if err := d.applySynthesizedTitle(paneID, sessionID, summary.SynthesizedTitle); err != nil {
				applyErr = err.Error()
				log.Printf("synthesize %s: apply title: %v", sessionID, err)
			} else {
				applied = true
			}
			mu.Lock()
			results = append(results, SynthesizeResultData{
				PaneID:       paneID,
				Summary:      summary,
				FromCache:    fromCache,
				TitleApplied: applied,
				ApplyError:   applyErr,
			})
			mu.Unlock()
		}(t.paneID, t.sessionID)
	}
	wg.Wait()

	// Trigger pulse after batch synthesis
	go d.triggerPulse()

	r := resultResponse(SynthesizeAllResultData{Results: results})
	return &r
}

// applySynthesizedTitle is the single native-title application path used by
// manual synthesis and the explicit apply-title command. Background synthesis
// deliberately does not call it: only a user action may inject /rename into a
// provider prompt.
func (d *Daemon) applySynthesizedTitle(paneID, sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("no synthesized title to apply")
	}
	session, ok := d.sessionByPaneID(paneID)
	if !ok {
		return fmt.Errorf("session not found for pane: %s", paneID)
	}
	if session.SessionID != sessionID {
		return fmt.Errorf("pane %s now belongs to session %s", paneID, session.SessionID)
	}
	if err := d.require(session, agent.CapabilityRenameNative); err != nil {
		return err
	}
	if session.Status != agent.StatusUserTurn {
		return fmt.Errorf("session must be idle before applying a title")
	}
	if err := d.sendCommand(session, "/rename "+title); err != nil {
		return err
	}
	claude.ApplySynthesizedTitle(sessionID)
	d.nudge()
	return nil
}

func (d *Daemon) handleApplyTitle(data json.RawMessage) *Response {
	var req PaneSessionData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	cached := claude.ReadCachedSummary(req.SessionID)
	if cached == nil || cached.SynthesizedTitle == "" {
		r := errResponse("no synthesized title to apply")
		return &r
	}
	if err := d.applySynthesizedTitle(req.PaneID, req.SessionID, cached.SynthesizedTitle); err != nil {
		r := errResponse(err.Error())
		return &r
	}
	r := resultResponse(nil)
	return &r
}
