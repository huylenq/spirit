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
	rePct = regexp.MustCompile(`(\d+)%(?:\s+used)?`)
	// Tolerates cursor-right ANSI mangling "Resets" → "Rese s" or "Rese ts"
	reResets = regexp.MustCompile(`Rese\s*t?s\s+(.+)`)
)

// FetchUsageRaw returns the raw ANSI-stripped dialog text for debugging.
// It doesn't require any specific text to be present — useful for diagnosing format changes.
func FetchUsageRaw() (string, error) {
	raw, err := fetchUsagePTYRaw()
	if err != nil {
		return "", err
	}
	return stripANSI(raw), nil
}

// fetchUsagePTYRaw runs /usage and returns the raw output without waiting for "% used".
func fetchUsagePTYRaw() (string, error) {
	cmd := exec.Command("claude")
	cmd.Env = filterEnv(filterEnv(os.Environ(), "CLAUDECODE"), "CLAUDE_CODE_ENTRYPOINT")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 220})
	if err != nil {
		return "", fmt.Errorf("start pty: %w", err)
	}
	defer func() {
		ptmx.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

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

	if err := pollFor(snapshot, "Claude Code v", 30*time.Second); err != nil {
		return "", fmt.Errorf("waiting for claude ready: %w", err)
	}
	for _, ch := range "/usage" {
		ptmx.Write([]byte(string(ch)))
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	ptmx.Write([]byte("\r"))

	// Wait up to 10s for the dialog then dump whatever we have
	pollFor(snapshot, "Current session", 10*time.Second)
	time.Sleep(1 * time.Second)

	return snapshot(), nil
}

// FetchUsage spawns claude in an internal pty, sends /usage, parses the output.
// No tmux session is created — completely invisible to the user.
func FetchUsage() (*UsageStats, error) {
	raw, err := fetchUsagePTY()
	if err != nil {
		return nil, err
	}
	return parseUsageDialog(stripANSI(raw))
}

func fetchUsagePTY() (string, error) {
	cmd := exec.Command("claude")
	// Unset env vars that trigger nested-session detection
	cmd.Env = filterEnv(filterEnv(os.Environ(), "CLAUDECODE"), "CLAUDE_CODE_ENTRYPOINT")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 220})
	if err != nil {
		return "", fmt.Errorf("start pty: %w", err)
	}
	defer func() {
		ptmx.Close()          // unblocks reader goroutine first
		cmd.Process.Kill()    // ensure dead
		cmd.Wait()            // reap zombie
	}()

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
		return "", fmt.Errorf("waiting for claude ready: %w", err)
	}

	// Type /usage char-by-char (autocomplete intercepts bulk writes) then Enter
	for _, ch := range "/usage" {
		if _, err := ptmx.Write([]byte(string(ch))); err != nil {
			return "", fmt.Errorf("send /usage: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		return "", fmt.Errorf("send enter: %w", err)
	}

	// Wait for usage dialog to open ("Esc to cancel" is always present once the dialog is visible)
	if err := pollFor(snapshot, "Esc to cancel", 15*time.Second); err != nil {
		return "", fmt.Errorf("waiting for usage dialog: %w", err)
	}

	// Wait for data to load (poll until we see a % or an error)
	pollFor(snapshot, "%", 10*time.Second)
	time.Sleep(500 * time.Millisecond)

	text := stripANSI(snapshot())

	// Detect API-level errors (rate limit, auth, etc.) and surface them clearly
	if strings.Contains(text, "Failed to load usage data") {
		if i := strings.Index(text, "Failed to load usage data"); i >= 0 {
			msg := text[i:]
			if end := strings.IndexAny(msg, "\n\r"); end > 0 {
				msg = msg[:end]
			}
			return "", fmt.Errorf("%s", strings.TrimSpace(msg))
		}
	}

	return text, nil
}

// pollFor polls snapshotFn until the ANSI-stripped output contains needle or timeout expires.
func pollFor(snapshotFn func() string, needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(stripANSI(snapshotFn()), needle) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q", needle)
}

var (
	// Cursor-right movement: \x1b[<N>C → replace with N spaces
	reCursorRight = regexp.MustCompile(`\x1b\[(\d+)C`)
	// All other ANSI: CSI sequences, DEC private modes, OSC (title), etc.
	reANSI = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e])`)
)

func stripANSI(s string) string {
	// First, replace cursor-right movements with actual spaces
	s = reCursorRight.ReplaceAllStringFunc(s, func(m string) string {
		sub := reCursorRight.FindStringSubmatch(m)
		n := 1
		if len(sub) > 1 {
			fmt.Sscanf(sub[1], "%d", &n)
		}
		return strings.Repeat(" ", n)
	})
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

	// Collect markers for boundary detection (stops section scanning from bleeding into the next)
	allMarkers := make([]string, len(sections))
	for i, s := range sections {
		allMarkers[i] = s.marker
	}

	for i, line := range lines {
		for _, s := range sections {
			if !strings.Contains(line, s.marker) {
				continue
			}
			found := 0

			// Check the marker line itself — new dialog collapses session data onto one line
			trimmed := strings.TrimSpace(line)
			if m := rePct.FindStringSubmatch(trimmed); m != nil {
				fmt.Sscanf(m[1], "%d", s.pct)
				found++
			}
			if found == 1 {
				if m := reResets.FindStringSubmatch(trimmed); m != nil {
					*s.resets = strings.TrimSpace(m[1])
					found++
				}
			}
			if found >= 2 {
				continue
			}

			// Scan subsequent lines (older/week-section format: one item per line)
			for _, l := range lines[i+1:] {
				// Stop if we've crossed into another section
				isBoundary := false
				for _, m := range allMarkers {
					if strings.Contains(l, m) {
						isBoundary = true
						break
					}
				}
				if isBoundary {
					break
				}

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

	if stats.SessionPct == 0 && stats.WeekAllPct == 0 {
		return nil, fmt.Errorf("could not parse usage from dialog output")
	}
	return stats, nil
}
