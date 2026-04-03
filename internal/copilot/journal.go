package copilot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// EventsDir returns the default events directory path (~/.cache/spirit/copilot/events).
func EventsDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cache", "spirit", "copilot", "events")
	os.MkdirAll(dir, 0o755) //nolint:errcheck
	return dir
}

// Journal is an append-only NDJSON event log.
// Storage: baseDir/YYYY-MM-DD.ndjson (one file per day).
type Journal struct {
	mu      sync.Mutex
	baseDir string
}

// NewJournal creates a Journal rooted at baseDir (typically ~/.cache/spirit/copilot/events/).
// Creates the directory if it doesn't exist.
func NewJournal(baseDir string) *Journal {
	_ = os.MkdirAll(baseDir, 0o755)
	return &Journal{baseDir: baseDir}
}

// Append marshals event to JSON and appends it to today's NDJSON file.
func (j *Journal) Append(event CopilotEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("copilot journal: marshal event: %w", err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if err := os.MkdirAll(j.baseDir, 0o755); err != nil {
		return fmt.Errorf("copilot journal: create dir: %w", err)
	}

	f, err := os.OpenFile(j.todayFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("copilot journal: open file: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("copilot journal: write: %w", err)
	}
	return nil
}

// ReadToday reads all events from today's NDJSON file.
func (j *Journal) ReadToday() ([]CopilotEvent, error) {
	return j.ReadDate(time.Now().Format("2006-01-02"))
}

// ReadDate reads all events from a specific date's NDJSON file.
// Date format: YYYY-MM-DD.
func (j *Journal) ReadDate(date string) ([]CopilotEvent, error) {
	return readNDJSON(j.dateFile(date))
}

// RecentEvents returns the last n events from today's file.
// If today has fewer than n events, also reads yesterday's file and combines,
// returning the most recent n total (sorted by time ascending).
func (j *Journal) RecentEvents(n int) ([]CopilotEvent, error) {
	today, err := j.ReadToday()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	if len(today) >= n {
		return today[len(today)-n:], nil
	}

	// Not enough from today — pull yesterday too.
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	prev, err := j.ReadDate(yesterday)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	combined := append(prev, today...)
	sort.Slice(combined, func(i, k int) bool {
		return combined[i].Time.Before(combined[k].Time)
	})

	if len(combined) > n {
		combined = combined[len(combined)-n:]
	}
	return combined, nil
}

// RecentEventsOrEmpty is like RecentEvents but returns an empty slice on error.
func (j *Journal) RecentEventsOrEmpty(n int) []CopilotEvent {
	events, err := j.RecentEvents(n)
	if err != nil {
		return []CopilotEvent{}
	}
	return events
}

// ReadForSession returns the last n events for a specific session ID from today's file.
func (j *Journal) ReadForSession(sessionID string, n int) ([]CopilotEvent, error) {
	all, err := j.ReadToday()
	if err != nil {
		return nil, err
	}

	var filtered []CopilotEvent
	for _, e := range all {
		if e.SessionID == sessionID {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) > n {
		filtered = filtered[len(filtered)-n:]
	}
	return filtered, nil
}

func (j *Journal) todayFile() string {
	return j.dateFile(time.Now().Format("2006-01-02"))
}

func (j *Journal) dateFile(date string) string {
	return filepath.Join(j.baseDir, date+".ndjson")
}

// readNDJSON reads an NDJSON file and returns all parsed events.
func readNDJSON(path string) ([]CopilotEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []CopilotEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e CopilotEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("copilot journal: parse line %q: %w", line, err)
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("copilot journal: scan: %w", err)
	}
	return events, nil
}
