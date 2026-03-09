package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// FileDiffStat holds per-file line change counts extracted from Edit/Write tool calls.
type FileDiffStat struct {
	Added   int
	Removed int
	HasEdit bool // true if at least one Edit (not just Write) touched this file
}

// FileDiffHunk holds a single Edit/Write tool call's content for diff display.
type FileDiffHunk struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"` // empty for Write
	NewString string `json:"new_string"`
	IsWrite   bool   `json:"is_write"`
}

type cachedDiffStats struct {
	stats   map[string]FileDiffStat
	modTime time.Time
	size    int64
}

var (
	diffStatsCache   = make(map[string]cachedDiffStats)
	diffStatsCacheMu sync.Mutex
)

// ReadDiffStats extracts per-file line change stats from Edit/Write tool calls in a transcript.
// Uses incremental reads with mtime-based caching.
func ReadDiffStats(sessionID string) map[string]FileDiffStat {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return nil
	}

	diffStatsCacheMu.Lock()
	cached, hasCached := diffStatsCache[sessionID]
	if hasCached && cached.modTime.Equal(info.ModTime()) {
		diffStatsCacheMu.Unlock()
		return cached.stats
	}

	// Prepare for incremental read
	stats := make(map[string]FileDiffStat)
	var readOffset int64
	if hasCached && info.Size() >= cached.size && cached.size > 0 {
		for k, v := range cached.stats {
			stats[k] = v
		}
		readOffset = cached.size
	}
	diffStatsCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if readOffset > 0 {
		f.Seek(readOffset, 0)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		// Pre-filter: skip lines that can't contain Edit/Write tool calls
		if !bytes.Contains(line, []byte(`"Edit"`)) && !bytes.Contains(line, []byte(`"Write"`)) {
			continue
		}
		extractDiffStats(line, stats)
	}

	diffStatsCacheMu.Lock()
	diffStatsCache[sessionID] = cachedDiffStats{stats: stats, modTime: info.ModTime(), size: info.Size()}
	diffStatsCacheMu.Unlock()

	return stats
}

type editInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func extractDiffStats(line []byte, stats map[string]FileDiffStat) {
	var tl transcriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return
	}
	if tl.Type != "assistant" {
		return
	}

	var msg messageContent
	if err := json.Unmarshal(tl.Message, &msg); err != nil {
		return
	}

	var blocks []toolUseBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}

	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		switch b.Name {
		case "Edit":
			var inp editInput
			if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
				continue
			}
			removed := strings.Count(inp.OldString, "\n")
			added := strings.Count(inp.NewString, "\n")
			s := stats[inp.FilePath]
			s.Added += added
			s.Removed += removed
			s.HasEdit = true
			stats[inp.FilePath] = s
		case "Write":
			var inp writeInput
			if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
				continue
			}
			added := strings.Count(inp.Content, "\n")
			s := stats[inp.FilePath]
			s.Added += added
			stats[inp.FilePath] = s
		}
	}
}

// --- Diff Hunks (full content) ---

const maxHunkLines = 50

type cachedDiffHunks struct {
	hunks   []FileDiffHunk
	modTime time.Time
	size    int64
}

var (
	diffHunksCache   = make(map[string]cachedDiffHunks)
	diffHunksCacheMu sync.Mutex
)

// ReadDiffHunks extracts per-file diff hunks (actual content) from Edit/Write tool calls.
func ReadDiffHunks(sessionID string) []FileDiffHunk {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return nil
	}

	diffHunksCacheMu.Lock()
	cached, hasCached := diffHunksCache[sessionID]
	if hasCached && cached.modTime.Equal(info.ModTime()) {
		diffHunksCacheMu.Unlock()
		return cached.hunks
	}

	var hunks []FileDiffHunk
	var readOffset int64
	if hasCached && info.Size() >= cached.size && cached.size > 0 {
		hunks = append(hunks, cached.hunks...)
		readOffset = cached.size
	}
	diffHunksCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if readOffset > 0 {
		f.Seek(readOffset, 0)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"Edit"`)) && !bytes.Contains(line, []byte(`"Write"`)) {
			continue
		}
		extractDiffHunks(line, &hunks)
	}

	diffHunksCacheMu.Lock()
	diffHunksCache[sessionID] = cachedDiffHunks{hunks: hunks, modTime: info.ModTime(), size: info.Size()}
	diffHunksCacheMu.Unlock()

	return hunks
}

func extractDiffHunks(line []byte, hunks *[]FileDiffHunk) {
	var tl transcriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return
	}
	if tl.Type != "assistant" {
		return
	}

	var msg messageContent
	if err := json.Unmarshal(tl.Message, &msg); err != nil {
		return
	}

	var blocks []toolUseBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}

	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		switch b.Name {
		case "Edit":
			var inp editInput
			if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
				continue
			}
			*hunks = append(*hunks, FileDiffHunk{
				FilePath:  inp.FilePath,
				OldString: truncateHunkContent(inp.OldString),
				NewString: truncateHunkContent(inp.NewString),
			})
		case "Write":
			var inp writeInput
			if json.Unmarshal(b.Input, &inp) != nil || inp.FilePath == "" {
				continue
			}
			*hunks = append(*hunks, FileDiffHunk{
				FilePath:  inp.FilePath,
				NewString: truncateHunkContent(inp.Content),
				IsWrite:   true,
			})
		}
	}
}

func truncateHunkContent(s string) string {
	lines := strings.SplitN(s, "\n", maxHunkLines+1)
	if len(lines) <= maxHunkLines {
		return s
	}
	remaining := strings.Count(s, "\n") - maxHunkLines + 1
	return strings.Join(lines[:maxHunkLines], "\n") + fmt.Sprintf("\n... (%d more lines)", remaining)
}
