package copilot

import (
	"testing"
	"time"
)

func TestParseHeartbeat_Empty(t *testing.T) {
	cfg := ParseHeartbeat(`# HEARTBEAT.md

# Keep this file empty to skip heartbeat checks.
# Add tasks below when you want the copilot to check something periodically.
`)
	if cfg.IsActive() {
		t.Errorf("expected inactive for default file, got tasks: %q", cfg.Tasks)
	}
	if cfg.Interval != defaultHeartbeatInterval {
		t.Errorf("expected default interval %v, got %v", defaultHeartbeatInterval, cfg.Interval)
	}
}

func TestParseHeartbeat_WithTasks(t *testing.T) {
	cfg := ParseHeartbeat(`# HEARTBEAT.md

# Periodic checks
- Check idle sessions older than 1 hour
- Flag file overlaps between active sessions
`)
	if !cfg.IsActive() {
		t.Fatal("expected active heartbeat")
	}
	if cfg.Tasks == "" {
		t.Fatal("expected non-empty tasks")
	}
}

func TestParseHeartbeat_CustomInterval(t *testing.T) {
	cfg := ParseHeartbeat(`# HEARTBEAT.md
<!-- interval: 15m -->

- Check idle sessions
`)
	if cfg.Interval != 15*time.Minute {
		t.Errorf("expected 15m interval, got %v", cfg.Interval)
	}
	if !cfg.IsActive() {
		t.Fatal("expected active heartbeat")
	}
}

func TestParseHeartbeat_MinimumInterval(t *testing.T) {
	cfg := ParseHeartbeat(`# HEARTBEAT.md
<!-- interval: 30s -->
- Check stuff
`)
	// 30s is below 1m minimum, should use default
	if cfg.Interval != defaultHeartbeatInterval {
		t.Errorf("expected default interval for sub-minute value, got %v", cfg.Interval)
	}
}

func TestParseHeartbeat_SubHeaders(t *testing.T) {
	cfg := ParseHeartbeat(`# HEARTBEAT.md

## Session Health
- Check idle sessions older than 1 hour

## Overlaps
- Flag file overlaps
`)
	if !cfg.IsActive() {
		t.Fatal("expected active heartbeat")
	}
	// Sub-headers (##) should be preserved as task content
	if cfg.Tasks == "" {
		t.Fatal("expected tasks with sub-headers")
	}
}
