package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/copilot/search"
)

const minSearchScore = 0.35

// Memory manages two-tier persistence: evergreen long-term facts (MEMORY.md)
// and dated daily logs (memory/YYYY-MM-DD.md).
type Memory struct {
	baseDir string // ~/.cache/cmc/copilot/
}

// SearchResult represents a matched chunk from memory search.
type SearchResult struct {
	File    string  // relative path
	Content string  // matched chunk
	Score   float64 // normalized 0-1
}

// NewMemory creates a Memory instance rooted at baseDir.
func NewMemory(baseDir string) *Memory {
	return &Memory{baseDir: baseDir}
}

// ReadLongTerm reads MEMORY.md.
func (m *Memory) ReadLongTerm() (string, error) {
	data, err := os.ReadFile(filepath.Join(m.baseDir, "MEMORY.md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadLongTermOrEmpty reads MEMORY.md, returning "" on error.
func (m *Memory) ReadLongTermOrEmpty() string {
	s, _ := m.ReadLongTerm()
	return s
}

// AppendLongTerm appends a fact to MEMORY.md with a timestamp header.
func (m *Memory) AppendLongTerm(fact string) error {
	f, err := os.OpenFile(filepath.Join(m.baseDir, "MEMORY.md"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	entry := fmt.Sprintf("\n\n### %s\n%s\n", time.Now().Format(time.RFC3339), fact)
	_, err = f.WriteString(entry)
	return err
}

// ReadDailyLog reads memory/<date>.md.
func (m *Memory) ReadDailyLog(date string) (string, error) {
	data, err := os.ReadFile(filepath.Join(m.baseDir, "memory", date+".md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteDailyLog writes memory/<date>.md, creating it if needed.
func (m *Memory) WriteDailyLog(date, content string) error {
	dir := filepath.Join(m.baseDir, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, date+".md"), []byte(content), 0o644)
}

// AppendDailyLog appends to memory/<date>.md.
func (m *Memory) AppendDailyLog(date, entry string) error {
	dir := filepath.Join(m.baseDir, "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, date+".md"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}

// TodayDate returns the current date as YYYY-MM-DD.
func (m *Memory) TodayDate() string {
	return time.Now().Format("2006-01-02")
}

// ListDailyLogs returns a sorted list of YYYY-MM-DD dates that have daily logs.
func (m *Memory) ListDailyLogs() ([]string, error) {
	dir := filepath.Join(m.baseDir, "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var dates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		// Extract date from YYYY-MM-DD.md or YYYY-MM-DD-slug.md
		if _, ok := search.ParseDateFromPath(name); ok {
			dateStr := name[:10]
			dates = append(dates, dateStr)
		}
	}
	sort.Strings(dates)
	return dates, nil
}

// Search searches across MEMORY.md and all memory/*.md files using keyword
// matching, temporal decay, and MMR re-ranking. Returns up to maxResults results
// above the minimum score threshold (0.35).
func (m *Memory) Search(query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 6
	}

	keywords := search.ExtractKeywords(query)
	if len(keywords) == 0 {
		return nil, nil
	}

	// Collect all files to search
	type fileEntry struct {
		relPath string
		absPath string
	}
	var files []fileEntry

	// MEMORY.md
	memPath := filepath.Join(m.baseDir, "MEMORY.md")
	if _, err := os.Stat(memPath); err == nil {
		files = append(files, fileEntry{relPath: "MEMORY.md", absPath: memPath})
	}

	// memory/*.md
	memDir := filepath.Join(m.baseDir, "memory")
	if entries, err := os.ReadDir(memDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			rel := filepath.Join("memory", e.Name())
			files = append(files, fileEntry{relPath: rel, absPath: filepath.Join(memDir, e.Name())})
		}
	}

	if len(files) == 0 {
		return nil, nil
	}

	// Chunk all files and score
	var allChunks []search.ScoredChunk

	for _, f := range files {
		data, err := os.ReadFile(f.absPath)
		if err != nil {
			continue
		}
		text := string(data)
		if len(text) == 0 {
			continue
		}

		chunks := search.ChunkText(text, search.DefaultMaxChars, search.DefaultOverlapChars)

		// Determine temporal decay multiplier
		decayMul := 1.0 // evergreen default
		if !search.IsEvergreenPath(f.relPath) {
			if fileDate, ok := search.ParseDateFromPath(f.relPath); ok {
				decayMul = search.TemporalDecayMultiplier(fileDate, 30)
			}
		}

		for _, chunk := range chunks {
			score := search.ScoreKeywordMatch(chunk, keywords)
			if score <= 0 {
				continue
			}
			score *= decayMul

			tokens := search.ExtractKeywords(chunk)
			allChunks = append(allChunks, search.ScoredChunk{
				Content: chunk,
				File:    f.relPath,
				Score:   score,
				Tokens:  tokens,
			})
		}
	}

	if len(allChunks) == 0 {
		return nil, nil
	}

	// Normalize scores
	rawScores := make([]float64, len(allChunks))
	for i, c := range allChunks {
		rawScores[i] = c.Score
	}
	normalized := search.NormalizeScores(rawScores)
	for i := range allChunks {
		allChunks[i].Score = normalized[i]
	}

	// MMR re-rank
	reranked := search.MMRRerank(allChunks, maxResults*2, 0.7) // over-fetch then filter

	// Convert to results, filtering by min score
	var results []SearchResult
	for _, chunk := range reranked {
		if chunk.Score < minSearchScore {
			continue
		}
		results = append(results, SearchResult{
			File:    chunk.File,
			Content: chunk.Content,
			Score:   chunk.Score,
		})
		if len(results) >= maxResults {
			break
		}
	}

	return results, nil
}
