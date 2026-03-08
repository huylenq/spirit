package claude

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
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

// FetchUsage spawns claude in an internal pty, sends /usage, parses the output.
// No tmux session is created — completely invisible to the user.
func FetchUsage() (*UsageStats, error) {
	cmd := exec.Command("claude",
		"--no-session-persistence", "--tools", "", "--setting-sources", "")
	// Unset env vars that trigger nested-session detection
	cmd.Env = filterEnv(filterEnv(os.Environ(), "CLAUDECODE"), "CLAUDE_CODE_ENTRYPOINT")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 220})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}
	defer ptmx.Close()
	defer cmd.Process.Kill()

	// Accumulate pty output in background
	var buf bytes.Buffer
	var mu sync.Mutex
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}

	// Wait for Claude Code to be ready
	if err := pollFor(snapshot, "Claude Code v", 30*time.Second); err != nil {
		return nil, fmt.Errorf("waiting for claude ready: %w", err)
	}

	// Send /usage command
	if _, err := ptmx.Write([]byte("/usage\n")); err != nil {
		return nil, fmt.Errorf("send /usage: %w", err)
	}

	// Wait for usage dialog to render
	if err := pollFor(snapshot, "% used", 15*time.Second); err != nil {
		return nil, fmt.Errorf("waiting for usage dialog: %w", err)
	}

	// Small extra wait for the full dialog to render
	time.Sleep(500 * time.Millisecond)

	return parseUsageDialog(stripANSI(snapshot()))
}

// pollFor polls snapshotFn until the output contains needle or timeout expires.
func pollFor(snapshotFn func() string, needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(snapshotFn(), needle) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q", needle)
}

var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return reANSI.ReplaceAllString(s, "")
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
