package claude

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// UsageStats holds account-level subscription usage fetched via the /usage TUI command.
type UsageStats struct {
	SessionPct      int    // current 5-hour session utilization %
	SessionResets   string // human-readable reset time, e.g. "6pm (Asia/Saigon)"
	WeekAllPct      int    // current week usage % (all models)
	WeekAllResets   string
	WeekSonnetPct   int    // current week usage % (Sonnet only)
	WeekSonnetResets string
}

var (
	rePct    = regexp.MustCompile(`(\d+)%\s+used`)
	reResets = regexp.MustCompile(`Resets\s+(.+)`)
)

// FetchUsage spawns a hidden tmux session, starts an interactive claude session,
// sends /usage, captures the dialog output, parses it, then cleans up.
func FetchUsage() (*UsageStats, error) {
	session := fmt.Sprintf("cmc-usage-%d", time.Now().UnixNano())

	// Create hidden tmux session (1 line tall is enough — we resize after)
	if err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "220", "-y", "50").Run(); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", session).Run()

	// Start claude with CLAUDECODE unset so nested-session check passes
	if err := exec.Command("tmux", "send-keys", "-t", session,
		"env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT claude", "Enter").Run(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	// Wait for the claude status bar to appear — "Claude Code v" is specific to the
	// claude TUI and won't match the shell prompt that precedes it.
	if err := waitForText(session, "Claude Code v", 30*time.Second); err != nil {
		return nil, fmt.Errorf("waiting for claude ready: %w", err)
	}

	// Send /usage
	if err := exec.Command("tmux", "send-keys", "-t", session, "/usage", "Enter").Run(); err != nil {
		return nil, fmt.Errorf("send /usage: %w", err)
	}

	// Wait for the dialog to render (look for "% used")
	if err := waitForText(session, "% used", 15*time.Second); err != nil {
		return nil, fmt.Errorf("waiting for usage dialog: %w", err)
	}

	content, err := capturePane(session)
	if err != nil {
		return nil, err
	}

	return parseUsageDialog(content)
}

func waitForText(session, needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		content, err := capturePane(session)
		if err == nil && strings.Contains(content, needle) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q", needle)
}

func capturePane(session string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", session).Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w", err)
	}
	return string(out), nil
}

// parseUsageDialog extracts usage % and reset times from the /usage dialog text.
//
// Expected format (each block):
//
//	Current session
//	████████  36% used
//	Resets 6pm (Asia/Saigon)
func parseUsageDialog(text string) (*UsageStats, error) {
	lines := strings.Split(text, "\n")
	stats := &UsageStats{}

	type section struct {
		marker string
		pct    *int
		resets *string
	}
	sections := []section{
		{"Current session", &stats.SessionPct, &stats.SessionResets},
		{"Current week (all models)", &stats.WeekAllPct, &stats.WeekAllResets},
		{"Current week (Sonnet only)", &stats.WeekSonnetPct, &stats.WeekSonnetResets},
	}

	for i, line := range lines {
		for _, s := range sections {
			if strings.Contains(line, s.marker) {
				// Next two non-empty lines are: progress bar with %, then resets
				remaining := lines[i+1:]
				found := 0
				for _, l := range remaining {
					l = strings.TrimSpace(l)
					if l == "" {
						continue
					}
					switch found {
					case 0:
						if m := rePct.FindStringSubmatch(l); m != nil {
							fmt.Sscanf(m[1], "%d", s.pct)
							found++
						}
					case 1:
						if m := reResets.FindStringSubmatch(l); m != nil {
							*s.resets = strings.TrimSpace(m[1])
						}
						found++
					}
					if found >= 2 {
						break
					}
				}
			}
		}
	}

	if stats.SessionPct == 0 && stats.WeekAllPct == 0 {
		return nil, fmt.Errorf("could not parse usage from dialog output")
	}
	return stats, nil
}
