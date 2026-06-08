package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// ptyCols is the width of the hidden PTY used to scrape /usage. The screen
// emulator in stripANSI must use the same width so absolute column addressing
// (\x1b[NG) lands where Claude Code intended.
const ptyCols = 220

// usageCachePath is the on-disk location for the last-known UsageStats.
// Persisting across daemon restarts means the TUI can show the previous
// bar immediately instead of rendering blank while /usage refetches.
func usageCachePath() string {
	return filepath.Join(StatusDir(), "usage.json")
}

// LoadCachedUsage reads the last persisted UsageStats, or nil if missing/invalid.
func LoadCachedUsage() *UsageStats {
	data, err := os.ReadFile(usageCachePath())
	if err != nil {
		return nil
	}
	var s UsageStats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

// SaveCachedUsage writes UsageStats to disk for next daemon startup.
func SaveCachedUsage(s *UsageStats) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(usageCachePath(), data, 0o644)
}

// UsageStats holds account-level subscription usage fetched via the /usage TUI command.
type UsageStats struct {
	SessionPct       int    // current 5-hour session utilization %
	SessionResets    string // human-readable reset time, e.g. "6pm (Asia/Saigon)"
	WeekAllPct       int    // current week usage % (all models)
	WeekAllResets    string
	WeekSonnetPct    int // current week usage % (Sonnet only)
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

// launchUsagePTY starts a hidden claude process, sends /usage, and returns a snapshot
// function for reading accumulated output plus a cleanup func. The caller is responsible
// for calling cleanup (via defer) and for any further polling/parsing.
func launchUsagePTY() (snapshot func() string, cleanup func(), err error) {
	cmd := exec.Command("claude")
	// Unset env vars that trigger nested-session detection
	cmd.Env = filterEnv(filterEnv(os.Environ(), "CLAUDECODE"), "CLAUDE_CODE_ENTRYPOINT")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: ptyCols})
	if err != nil {
		return nil, nil, fmt.Errorf("start pty: %w", err)
	}
	cleanup = func() {
		ptmx.Close()       // unblocks reader goroutine first
		cmd.Process.Kill() // ensure dead
		cmd.Wait()         // reap zombie
	}

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
	snapshot = func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}

	if err := pollFor(snapshot, "Claude Code v", 30*time.Second); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("waiting for claude ready: %w", err)
	}
	// Type /usage char-by-char (autocomplete intercepts bulk writes) then Enter
	for _, ch := range "/usage" {
		if _, err := ptmx.Write([]byte(string(ch))); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("send /usage: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("send enter: %w", err)
	}
	return snapshot, cleanup, nil
}

// fetchUsagePTYRaw runs /usage and returns the raw PTY output for debugging.
func fetchUsagePTYRaw() (string, error) {
	snapshot, cleanup, err := launchUsagePTY()
	if err != nil {
		return "", err
	}
	defer cleanup()

	// Best-effort: wait for a "%" to signal data loaded; timeout is fine — parse will handle missing data
	pollFor(snapshot, "%", 10*time.Second) //nolint:errcheck
	time.Sleep(500 * time.Millisecond)

	text := stripANSI(snapshot())
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

// FetchUsage spawns claude in an internal pty, sends /usage, parses the output.
// No tmux session is created — completely invisible to the user.
func FetchUsage() (*UsageStats, error) {
	snapshot, cleanup, err := launchUsagePTY()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// Wait for usage dialog to open ("Esc to cancel" is always present once the dialog is visible)
	if err := pollFor(snapshot, "Esc to cancel", 15*time.Second); err != nil {
		return nil, fmt.Errorf("waiting for usage dialog: %w", err)
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
			return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
		}
	}

	return parseUsageDialog(text)
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

// stripANSI renders a PTY byte stream to its final visible text.
//
// Claude Code's TUI doesn't emit a clean stream of characters and spaces — it
// positions text with absolute cursor addressing (CHA \x1b[NG, CUP \x1b[r;cH),
// repaints with screen/line clears (\x1b[2J, \x1b[K), and moves the cursor
// freely (\x1b[A/B/C/D). A regex that merely deletes escape sequences collapses
// inter-word spacing and lets stale frames bleed into the result. So we run a
// minimal terminal emulator over a cell grid and read back the final screen,
// which is exactly what a human sees and what the parsers below expect.
func stripANSI(s string) string {
	g := newScreen(ptyCols)
	g.feed(s)
	return g.text()
}

// screen is a minimal VT cell grid: enough of an emulator to faithfully
// reconstruct horizontally- and vertically-addressed TUI layout.
type screen struct {
	rows [][]rune
	r, c int
	cols int
}

func newScreen(cols int) *screen { return &screen{cols: cols} }

func (s *screen) ensureRow(r int) {
	for len(s.rows) <= r {
		row := make([]rune, s.cols)
		for i := range row {
			row[i] = ' '
		}
		s.rows = append(s.rows, row)
	}
}

func (s *screen) put(ch rune) {
	if s.c >= s.cols { // autowrap
		s.c = 0
		s.r++
	}
	if s.c < 0 {
		s.c = 0
	}
	s.ensureRow(s.r)
	s.rows[s.r][s.c] = ch
	s.c++
}

func (s *screen) eraseLine(mode int) {
	s.ensureRow(s.r)
	row := s.rows[s.r]
	switch mode {
	case 0: // cursor → end of line
		for i := s.c; i < s.cols; i++ {
			row[i] = ' '
		}
	case 1: // start of line → cursor
		for i := 0; i <= s.c && i < s.cols; i++ {
			row[i] = ' '
		}
	case 2: // whole line
		for i := range row {
			row[i] = ' '
		}
	}
}

func atoiDefault(p string, def int) int {
	if p == "" {
		return def
	}
	if v, err := strconv.Atoi(p); err == nil {
		return v
	}
	return def
}

func (s *screen) feed(in string) {
	rs := []rune(in)
	n := len(rs)
	for i := 0; i < n; i++ {
		r := rs[i]
		switch {
		case r == 0x1b:
			if i+1 >= n {
				return
			}
			switch rs[i+1] {
			case '[': // CSI: params (0x20-0x3f) then a final byte (0x40-0x7e)
				j := i + 2
				for j < n && rs[j] >= 0x20 && rs[j] <= 0x3f {
					j++
				}
				if j >= n {
					return
				}
				params := string(rs[i+2 : j])
				p0, p1 := params, ""
				if k := strings.IndexByte(params, ';'); k >= 0 {
					p0, p1 = params[:k], params[k+1:]
				}
				switch rs[j] {
				case 'H', 'f': // cursor position (row;col, 1-based)
					s.r = atoiDefault(p0, 1) - 1
					s.c = atoiDefault(p1, 1) - 1
				case 'A': // up
					s.r -= atoiDefault(p0, 1)
				case 'B': // down
					s.r += atoiDefault(p0, 1)
				case 'C': // forward
					s.c += atoiDefault(p0, 1)
				case 'D': // back
					s.c -= atoiDefault(p0, 1)
				case 'G': // horizontal absolute
					s.c = atoiDefault(p0, 1) - 1
				case 'd': // vertical absolute
					s.r = atoiDefault(p0, 1) - 1
				case 'J': // erase display (2/3 = clear all)
					if m := atoiDefault(p0, 0); m == 2 || m == 3 {
						s.rows, s.r, s.c = nil, 0, 0
					}
				case 'K': // erase line
					s.eraseLine(atoiDefault(p0, 0))
				}
				if s.r < 0 {
					s.r = 0
				}
				if s.c < 0 {
					s.c = 0
				}
				i = j
			case ']': // OSC: consume until BEL or ST (ESC \)
				j := i + 2
				for j < n && rs[j] != 0x07 {
					if rs[j] == 0x1b && j+1 < n && rs[j+1] == '\\' {
						j++
						break
					}
					j++
				}
				i = j
			default: // two-byte escape (charset selectors, etc.)
				i++
			}
		case r == '\r':
			s.c = 0
		case r == '\n':
			s.r++
			s.ensureRow(s.r)
		case r == '\t':
			s.c = (s.c/8 + 1) * 8
		default:
			s.put(r)
		}
	}
}

func (s *screen) text() string {
	var b strings.Builder
	for i, row := range s.rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimRight(string(row), " "))
	}
	return b.String()
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

			// Check the marker line itself — new dialog collapses session data onto one line.
			// Restrict search to the region between this marker and the next section marker
			// on the same line; otherwise FindStringSubmatch always grabs the first %, which
			// is the session % when multiple sections share one line.
			trimmed := strings.TrimSpace(line)
			markerIdx := strings.Index(trimmed, s.marker)
			region := trimmed[markerIdx+len(s.marker):]
			for _, other := range allMarkers {
				if other == s.marker {
					continue
				}
				if idx := strings.Index(region, other); idx >= 0 {
					region = region[:idx]
				}
			}
			if m := rePct.FindStringSubmatch(region); m != nil {
				fmt.Sscanf(m[1], "%d", s.pct)
				found++
			}
			if found == 1 {
				if m := reResets.FindStringSubmatch(region); m != nil {
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
