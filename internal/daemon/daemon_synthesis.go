package daemon

import (
	"log"
	"time"

	"github.com/huylenq/spirit/internal/claude"
)

// Debounce windows for auto-synthesis. Short window applies while a session
// is young and its objective is still shifting; long window kicks in once it
// has matured past autoSynthMatureMsgs user messages.
const (
	autoSynthShortWindow   = 30 * time.Second
	autoSynthLongWindow    = 15 * time.Minute
	autoSynthMatureMsgs    = 3
)

// autoSynthesize runs synthesis for a session that just became idle.
// Called as a goroutine from patchSession on agent-turn → user-turn transitions.
func (d *Daemon) autoSynthesize(paneID, sessionID string) {
	if d.autoSynthDisabled { // test override: keep synthesis out of ingest tests
		return
	}
	if d.readPref("autoSynthesize") == "false" {
		return
	}

	// Cheap path first: always-skip if inside the short window; always-proceed
	// if outside the long window. Only when we're between the two do we need
	// to read the transcript to disambiguate by message count.
	d.lastSynthMu.Lock()
	last, hasLast := d.lastSynthTime[sessionID]
	d.lastSynthMu.Unlock()
	if hasLast {
		elapsed := time.Since(last)
		if elapsed < autoSynthShortWindow {
			return
		}
		if elapsed < autoSynthLongWindow {
			msgs, err := claude.ReadUserMessages(sessionID)
			if err != nil {
				log.Printf("auto-synth: read messages %s: %v", sessionID, err)
				return
			}
			if len(msgs) > autoSynthMatureMsgs {
				return
			}
		}
	}

	d.synthesizingMu.Lock()
	if d.synthesizingPanes[paneID] {
		d.synthesizingMu.Unlock()
		return
	}
	d.synthesizingPanes[paneID] = true
	d.synthesizingMu.Unlock()

	d.lastSynthMu.Lock()
	d.lastSynthTime[sessionID] = time.Now()
	d.lastSynthMu.Unlock()

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
	// the user's input buffer. The SynthesizedTitle will appear in the TUI on the
	// next poll cycle via DiscoverSessions → ReadCachedSummary.

	// Trigger pulse regeneration after synthesis
	go d.triggerPulse()
}

// triggerPulse regenerates the workspace pulse after synthesis.
// Uses TryLock to prevent overlap.
func (d *Daemon) triggerPulse() {
	if !d.pulseMu.TryLock() {
		return
	}
	defer d.pulseMu.Unlock()

	// Debounce: skip if last pulse was < 60s ago
	if time.Since(d.lastPulseTime) < 60*time.Second {
		return
	}

	sessions := d.currentSessions()
	_, err := claude.GeneratePulse(sessions)
	if err != nil {
		log.Printf("pulse: %v", err)
		return
	}
	d.lastPulseTime = time.Now()

	// Bump version so subscribers receive pulse update
	d.mu.Lock()
	d.version++
	s := d.sessions
	d.mu.Unlock()
	d.notifySubscribers(s)
}

func (d *Daemon) usageLoop(stop chan struct{}) {
	// Hydrate from disk so the bar renders the last-known value immediately
	// instead of going blank for a few seconds while /usage refetches.
	if d.currentUsage() == nil {
		if cached := claude.LoadCachedUsage(); cached != nil {
			d.usageMu.Lock()
			d.usageStats = cached
			d.usageMu.Unlock()
			d.mu.Lock()
			d.version++
			sessions := d.sessions
			d.mu.Unlock()
			d.notifySubscribers(sessions)
		}
	}

	go d.fetchUsage()

	ticker := time.NewTicker(15 * time.Minute)
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

	if err := claude.SaveCachedUsage(stats); err != nil {
		log.Printf("usage cache save: %v", err)
	}

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
