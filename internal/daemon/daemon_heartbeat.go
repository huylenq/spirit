package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/copilot"
)

// heartbeatLoop periodically reads HEARTBEAT.md from the copilot workspace.
// If tasks are configured, it fires a copilot prompt with heartbeat context.
// Re-reads the file each tick so edits take effect without daemon restart.
func (d *Daemon) heartbeatLoop(stop chan struct{}) {
	// Start with a short initial tick to pick up the config, then adjust.
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastInterval time.Duration

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			cfg := d.readHeartbeatConfig()
			if !cfg.IsActive() {
				continue
			}

			// Adjust ticker if interval changed
			if cfg.Interval != lastInterval {
				lastInterval = cfg.Interval
				ticker.Reset(cfg.Interval)
				log.Printf("heartbeat: interval set to %v", cfg.Interval)
			}

			d.runHeartbeat(cfg)
		}
	}
}

// readHeartbeatConfig reads and parses HEARTBEAT.md from the workspace.
func (d *Daemon) readHeartbeatConfig() copilot.HeartbeatConfig {
	path := filepath.Join(d.copilotWorkspace.Dir, "HEARTBEAT.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return copilot.HeartbeatConfig{}
	}
	return copilot.ParseHeartbeat(string(data))
}

// runHeartbeat fires a single heartbeat copilot prompt if no other prompt is in-flight.
func (d *Daemon) runHeartbeat(cfg copilot.HeartbeatConfig) {
	// Don't run if a copilot prompt is already in-flight
	d.copilotMu.Lock()
	if d.copilotCancel != nil {
		d.copilotMu.Unlock()
		log.Printf("heartbeat: skipped (copilot busy)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	d.copilotCancel = cancel
	d.copilotMu.Unlock()

	log.Printf("heartbeat: running tasks")
	defer d.clearCopilotCancel()

	// Build the heartbeat prompt
	preamble := d.buildCopilotPreamble()
	prompt := fmt.Sprintf(
		"%s\n\n[HEARTBEAT] This is an autonomous heartbeat check. Execute the following tasks and report findings concisely.\n\n%s",
		preamble, cfg.Tasks,
	)

	// Run in foreground (blocking) — the ticker won't fire again until this completes
	output, err := d.runCopilotPromptStreaming(ctx, prompt)
	if err != nil {
		if !strings.Contains(err.Error(), "cancelled") {
			log.Printf("heartbeat: error: %v", err)
		}
		return
	}

	// Persist to history
	now := time.Now()
	d.appendCopilotHistory(
		CopilotHistoryMsg{Role: "heartbeat", Content: cfg.Tasks, Time: now},
		CopilotHistoryMsg{Role: "copilot", Content: output, Time: now},
	)

	// Log to event journal
	d.copilotJournal.Append(copilot.CopilotEvent{
		Time:   now,
		Type:   copilot.EventHeartbeat,
		Detail: fmt.Sprintf("heartbeat completed (%d chars)", len(output)),
	})

	log.Printf("heartbeat: completed (%d chars output)", len(output))
}
