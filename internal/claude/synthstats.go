package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SynthKindSummary = "summary"
	SynthKindDigest  = "digest"
)

// SynthCall is a single synthesizer invocation record, written to the usage log.
type SynthCall struct {
	Time  time.Time `json:"t"`
	Kind  string    `json:"kind"`
	Words int       `json:"words"`
}

// SynthPeriod aggregates synthesizer calls over a time window.
type SynthPeriod struct {
	Calls int
	Words int
}

// SynthStats holds aggregated synthesizer usage across rolling time windows.
type SynthStats struct {
	Today SynthPeriod
	Week  SynthPeriod
	Month SynthPeriod
}

func synthUsageLogPath() string {
	return filepath.Join(statusDir(), "synth-usage.ndjson")
}

var synthLogMu sync.Mutex

// RecordSynthCall appends one call record to the usage log.
// Safe to call concurrently from multiple goroutines.
// Silently ignores write errors — stats are best-effort.
func RecordSynthCall(kind string, words int) {
	entry := SynthCall{Time: time.Now(), Kind: kind, Words: words}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := synthUsageLogPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	synthLogMu.Lock()
	defer synthLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n')) //nolint:errcheck
}

var (
	synthStatsMu       sync.Mutex
	synthStatsCache    SynthStats
	synthStatsCachedAt time.Time
	synthPruning       atomic.Bool
)

const synthStatsTTL = 10 * time.Second

// ReadSynthStats reads the usage log and returns aggregated stats.
// Results are cached for 10 seconds to avoid re-scanning on every render.
// Entries older than 90 days are pruned automatically in the background.
func ReadSynthStats() SynthStats {
	synthStatsMu.Lock()
	if time.Since(synthStatsCachedAt) < synthStatsTTL {
		s := synthStatsCache
		synthStatsMu.Unlock()
		return s
	}
	synthStatsMu.Unlock()

	stats, pruneNeeded, kept := readAndAggregateSynthStats()

	synthStatsMu.Lock()
	synthStatsCache = stats
	synthStatsCachedAt = time.Now()
	synthStatsMu.Unlock()

	// Guard against concurrent prune goroutines — at most one in flight at a time.
	if pruneNeeded && synthPruning.CompareAndSwap(false, true) {
		logPath := synthUsageLogPath()
		go func() {
			pruneUsageLog(logPath, kept)
			synthPruning.Store(false)
		}()
	}
	return stats
}

func readAndAggregateSynthStats() (stats SynthStats, pruneNeeded bool, kept [][]byte) {
	path := synthUsageLogPath()
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	now := time.Now()
	cutoff90 := now.AddDate(0, 0, -90)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekAgo := now.AddDate(0, 0, -7)
	monthAgo := now.AddDate(0, -1, 0)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var c SynthCall
		if json.Unmarshal(line, &c) != nil {
			continue
		}
		if c.Time.Before(cutoff90) {
			pruneNeeded = true
			continue
		}
		kept = append(kept, slices.Clone(line))

		if !c.Time.Before(dayStart) {
			stats.Today.Calls++
			stats.Today.Words += c.Words
		}
		if c.Time.After(weekAgo) {
			stats.Week.Calls++
			stats.Week.Words += c.Words
		}
		if c.Time.After(monthAgo) {
			stats.Month.Calls++
			stats.Month.Words += c.Words
		}
	}
	return
}

// pruneUsageLog rewrites the log keeping only the provided lines.
func pruneUsageLog(path string, lines [][]byte) {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	for _, l := range lines {
		f.Write(append(l, '\n')) //nolint:errcheck
	}
	f.Close()
	os.Rename(tmp, path) //nolint:errcheck
}
