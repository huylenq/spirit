package daemon

import (
	"log"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

// autoSynthesize runs synthesis for a session that just became idle.
// Called as a goroutine from patchSession on agent-turn → user-turn transitions.
func (d *Daemon) autoSynthesize(paneID, sessionID string) {
	// Check pref — default on (only skip if explicitly "false")
	if d.readPref("autoSynthesize") == "false" {
		return
	}

	// Skip if session already has a user-set custom title — synthesis
	// headline wouldn't be displayed anyway (CustomTitle takes priority).
	if claude.ReadCustomTitle(sessionID) != "" {
		return
	}

	// Atomically check debounce + claim synthesizing slot.
	// Single lock acquisition prevents TOCTOU between debounce check and slot claim.
	d.autoSynthMu.Lock()
	if last, ok := d.lastAutoSynthTime[sessionID]; ok && time.Since(last) < 30*time.Second {
		d.autoSynthMu.Unlock()
		return
	}
	d.synthesizingMu.Lock()
	if d.synthesizingPanes[paneID] {
		d.synthesizingMu.Unlock()
		d.autoSynthMu.Unlock()
		return
	}
	d.synthesizingPanes[paneID] = true
	d.synthesizingMu.Unlock()
	d.lastAutoSynthTime[sessionID] = time.Now()
	d.autoSynthMu.Unlock()

	d.nudge() // show spinner immediately

	_, _, err := claude.Summarize(sessionID)

	d.synthesizingMu.Lock()
	delete(d.synthesizingPanes, paneID)
	d.synthesizingMu.Unlock()
	d.nudge()

	if err != nil {
		log.Printf("auto-synth: session %s: %v", sessionID, err)
		return
	}

	// No /rename SendKeys here — auto-synth must not inject keystrokes into
	// the user's input buffer. The Headline will appear in the TUI on the
	// next poll cycle via DiscoverSessions → ReadCachedSummary.

	// Trigger digest regeneration after synthesis
	go d.triggerDigest()
}

// triggerDigest regenerates the workspace digest after synthesis.
// Uses TryLock to prevent overlap.
func (d *Daemon) triggerDigest() {
	if !d.digestMu.TryLock() {
		return
	}
	defer d.digestMu.Unlock()

	// Debounce: skip if last digest was < 60s ago
	if time.Since(d.lastDigestTime) < 60*time.Second {
		return
	}

	sessions := d.currentSessions()
	_, err := claude.GenerateDigest(sessions)
	if err != nil {
		log.Printf("digest: %v", err)
		return
	}
	d.lastDigestTime = time.Now()

	// Bump version so subscribers receive digest update
	d.mu.Lock()
	d.version++
	s := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(s)
}

func (d *Daemon) usageLoop(stop chan struct{}) {
	// Only fetch immediately if we have no cached data yet.
	if d.currentUsage() == nil {
		go d.fetchUsage()
	}

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			go d.fetchUsage()
		}
	}
}

func (d *Daemon) fetchUsage() {
	// Skip if a fetch is already in flight.
	if !d.usageFetching.TryLock() {
		return
	}
	defer d.usageFetching.Unlock()

	stats, err := claude.FetchUsage()
	if err != nil {
		log.Printf("usage fetch: %v", err)
		return
	}
	d.usageMu.Lock()
	d.usageStats = stats
	d.usageMu.Unlock()

	// Bump version and notify subscribers so they receive the new usage data,
	// even if sessions haven't changed.
	d.mu.Lock()
	d.version++
	sessions := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(sessions)
}

func (d *Daemon) currentUsage() *claude.UsageStats {
	d.usageMu.RLock()
	defer d.usageMu.RUnlock()
	return d.usageStats
}
