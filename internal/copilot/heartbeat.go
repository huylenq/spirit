package copilot

import (
	"regexp"
	"strings"
	"time"
)

const defaultHeartbeatInterval = 30 * time.Minute

// HeartbeatConfig holds the parsed contents of HEARTBEAT.md.
type HeartbeatConfig struct {
	Interval time.Duration
	Tasks    string // non-empty means heartbeat is active
}

var intervalRe = regexp.MustCompile(`<!--\s*interval:\s*(\d+[smh])\s*-->`)

// ParseHeartbeat extracts interval and task content from HEARTBEAT.md.
// Returns empty Tasks if the file is blank or contains only comments.
func ParseHeartbeat(content string) HeartbeatConfig {
	cfg := HeartbeatConfig{Interval: defaultHeartbeatInterval}

	// Extract interval from HTML comment: <!-- interval: 30m -->
	if m := intervalRe.FindStringSubmatch(content); len(m) == 2 {
		if d, err := time.ParseDuration(m[1]); err == nil && d >= 1*time.Minute {
			cfg.Interval = d
		}
	}

	// Strip the interval directive and leading comment lines to find actual tasks
	var taskLines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip empty, comment-only lines, and the interval directive
		if trimmed == "" || intervalRe.MatchString(trimmed) {
			continue
		}
		// Skip top-level "# " headers (title, comment lines). Preserve all
		// sub-headers (##, ###, etc.) as task structure.
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			continue
		}
		taskLines = append(taskLines, line)
	}

	cfg.Tasks = strings.TrimSpace(strings.Join(taskLines, "\n"))
	return cfg
}

// IsActive returns true if the heartbeat has tasks configured.
func (h HeartbeatConfig) IsActive() bool {
	return h.Tasks != ""
}
