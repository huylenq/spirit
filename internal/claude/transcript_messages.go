package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Codex rollout records can contain the complete injected instruction/tool
// context in one JSONL record. Real sessions exceed 2 MiB, so Scanner's 64 KiB
// default and the former 1 MiB cap both truncate otherwise valid transcripts.
// Keep a protective bound, but make it large enough for provider records.
const maxTranscriptRecordSize = 32 * 1024 * 1024

// --- Full transcript message cache (incremental) ---

type cachedTranscript struct {
	messages []string
	modTime  time.Time
	size     int64
}

var (
	transcriptCache   = map[string]cachedTranscript{}
	transcriptCacheMu sync.Mutex
)

// --- First user message cache — permanent (first message never changes) ---

var (
	firstMsgCache   = make(map[string]string)
	firstMsgCacheMu sync.Mutex
)

// ReadFirstUserMessage returns the first user-typed message from a transcript.
// Only caches non-empty results so new sessions are retried on next tick.
func ReadFirstUserMessage(sessionID string) string {
	firstMsgCacheMu.Lock()
	if msg, ok := firstMsgCache[sessionID]; ok {
		firstMsgCacheMu.Unlock()
		return msg
	}
	firstMsgCacheMu.Unlock()

	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return ""
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var result string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), maxTranscriptRecordSize)
	for scanner.Scan() {
		if text := extractUserText(scanner.Bytes()); text != "" {
			result = text
			break
		}
	}

	if result != "" {
		firstMsgCacheMu.Lock()
		firstMsgCache[sessionID] = result
		firstMsgCacheMu.Unlock()
	}
	return result
}

// --- Last user message cache (separate from full transcript cache) ---

type lastMsgCacheEntry struct {
	message string
	modTime time.Time
}

var (
	lastMsgCache   = make(map[string]lastMsgCacheEntry)
	lastMsgCacheMu sync.Mutex
)

// ReadLastUserMessage returns the last user-typed message from a transcript.
// Reads only the tail of the file and caches by mtime.
func ReadLastUserMessage(sessionID string) string {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return ""
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return ""
	}

	// Check mtime cache — avoid re-reading if file hasn't changed
	lastMsgCacheMu.Lock()
	if cached, ok := lastMsgCache[sessionID]; ok && cached.modTime.Equal(info.ModTime()) {
		lastMsgCacheMu.Unlock()
		return cached.message
	}
	lastMsgCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Read only the last 128KB — enough to find the last user message
	const tailSize = 128 * 1024
	size := info.Size()
	offset := size - tailSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, size-offset)
	n, _ := f.ReadAt(buf, offset)
	if n == 0 {
		return ""
	}

	// Scan lines in reverse to find the last user message
	var result string
	lines := strings.Split(string(buf[:n]), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == "" {
			continue
		}
		text := extractUserText([]byte(lines[i]))
		if text != "" {
			result = text
			break
		}
	}

	lastMsgCacheMu.Lock()
	lastMsgCache[sessionID] = lastMsgCacheEntry{message: result, modTime: info.ModTime()}
	lastMsgCacheMu.Unlock()

	return result
}

// --- Last assistant info cache (single scan for message + insight) ---

// AssistantInfo holds the last assistant text message and all ★ Insight blocks
// extracted from a single scan of the transcript tail.
type AssistantInfo struct {
	Message  string   // last assistant text
	UUID     string   // transcript uuid of the last assistant text entry (Claude; empty for providers without entry uuids)
	Recap    string   // most recent away_summary content (system recap)
	Insights []string // all ★ Insight blocks found (oldest first)
}

type assistantInfoCacheEntry struct {
	info    AssistantInfo
	modTime time.Time
}

var (
	assistantInfoCache   = make(map[string]assistantInfoCacheEntry)
	assistantInfoCacheMu sync.Mutex
)

const (
	insightMarker = "★ Insight"
	insightDelim  = "─────────"
)

// extractInsight pulls the content between ★ Insight delimiters from text.
// Uses LastIndex because earlier occurrences may be conversational references,
// while the actual block is always near the end.
func extractInsight(text string) string {
	idx := strings.LastIndex(text, insightMarker)
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		return ""
	}
	rest = rest[nl+1:]
	if end := strings.Index(rest, insightDelim); end > 0 {
		return strings.TrimSpace(rest[:end])
	}
	return strings.TrimSpace(rest)
}

// ReadLastAssistantInfo does a single reverse scan of the transcript tail,
// extracting both the last assistant message and the most recent ★ Insight block.
// Caches by mtime — one file read serves both values.
func ReadLastAssistantInfo(sessionID string) AssistantInfo {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return AssistantInfo{}
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return AssistantInfo{}
	}

	assistantInfoCacheMu.Lock()
	if cached, ok := assistantInfoCache[sessionID]; ok && cached.modTime.Equal(info.ModTime()) {
		assistantInfoCacheMu.Unlock()
		return cached.info
	}
	assistantInfoCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return AssistantInfo{}
	}
	defer f.Close()

	const tailSize = 256 * 1024
	size := info.Size()
	offset := size - tailSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, size-offset)
	n, _ := f.ReadAt(buf, offset)
	if n == 0 {
		return AssistantInfo{}
	}

	var result AssistantInfo
	raw := string(buf[:n])
	lines := strings.Split(raw, "\n")
	assistantTag := []byte(`"type":"assistant"`)
	awaySummaryTag := []byte(`"away_summary"`)
	isCodex := ReadSessionMeta(sessionID).Provider == ProviderCodex
	// Reverse scan: collect last message + all insights + most recent recap
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == "" {
			continue
		}
		line := []byte(lines[i])
		if result.Recap == "" && bytes.Contains(line, awaySummaryTag) {
			var entry struct {
				Type    string `json:"type"`
				Subtype string `json:"subtype"`
				Content string `json:"content"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Type == "system" && entry.Subtype == "away_summary" && entry.Content != "" {
				result.Recap = entry.Content
			}
		}
		// Cheap pre-filter: skip JSON unmarshal for non-assistant lines
		if !isCodex && !bytes.Contains(line, assistantTag) {
			continue
		}
		text := extractAssistantText(line)
		if text == "" {
			continue
		}
		if result.Message == "" {
			result.Message = text
			// Capture the entry's transcript uuid alongside the text: it is the
			// stable per-turn discriminator the perception ledger anchors
			// turn_completed on (every Claude transcript entry carries one).
			var meta struct {
				UUID string `json:"uuid"`
			}
			if json.Unmarshal(line, &meta) == nil {
				result.UUID = meta.UUID
			}
		}
		if insight := extractInsight(text); insight != "" {
			result.Insights = append(result.Insights, insight)
		}
	}
	// Reverse collected insights so they're in chronological order (oldest first)
	for i, j := 0, len(result.Insights)-1; i < j; i, j = i+1, j-1 {
		result.Insights[i], result.Insights[j] = result.Insights[j], result.Insights[i]
	}

	assistantInfoCacheMu.Lock()
	assistantInfoCache[sessionID] = assistantInfoCacheEntry{info: result, modTime: info.ModTime()}
	assistantInfoCacheMu.Unlock()

	return result
}

// ReadUserMessages extracts user-typed messages from a session transcript.
// Uses incremental reads — only parses new content appended since last read.
func ReadUserMessages(sessionID string) ([]string, error) {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	transcriptCacheMu.Lock()
	cached, hasCached := transcriptCache[sessionID]
	if hasCached && cached.modTime.Equal(info.ModTime()) {
		transcriptCacheMu.Unlock()
		return cached.messages, nil
	}

	// Prepare for incremental read if cache exists and file grew
	var messages []string
	var readOffset int64
	if hasCached && info.Size() >= cached.size && cached.size > 0 {
		messages = make([]string, len(cached.messages))
		copy(messages, cached.messages)
		readOffset = cached.size
	}
	transcriptCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if readOffset > 0 {
		f.Seek(readOffset, 0)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), maxTranscriptRecordSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		text := extractUserText(line)
		if text != "" {
			messages = append(messages, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	transcriptCacheMu.Lock()
	transcriptCache[sessionID] = cachedTranscript{messages: messages, modTime: info.ModTime(), size: info.Size()}
	transcriptCacheMu.Unlock()

	return messages, nil
}

// TextMessage represents a user or assistant text message from a transcript.
type TextMessage struct {
	Role string // "user" or "assistant"
	Text string
}

// ReadAllTextMessages extracts user and assistant text messages from a session transcript.
// Called on-demand (no incremental caching).
func ReadAllTextMessages(sessionID string) ([]TextMessage, error) {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), maxTranscriptRecordSize)

	var messages []TextMessage
	for scanner.Scan() {
		line := scanner.Bytes()
		if text := extractUserText(line); text != "" {
			messages = append(messages, TextMessage{Role: "user", Text: text})
		} else if text := extractAssistantText(line); text != "" {
			messages = append(messages, TextMessage{Role: "assistant", Text: text})
		}
	}
	return messages, scanner.Err()
}

// --- Custom title cache (mtime-based) ---

type customTitleCacheEntry struct {
	title   string
	modTime time.Time
}

var (
	customTitleCache   = make(map[string]customTitleCacheEntry)
	customTitleCacheMu sync.Mutex

	codexTitleIndexCache   codexTitleIndexCacheEntry
	codexTitleIndexCacheMu sync.Mutex
	codexStateTitleCache   codexStateTitleCacheEntry
	codexStateTitleCacheMu sync.Mutex
)

type codexTitleIndexCacheEntry struct {
	path    string
	modTime time.Time
	titles  map[string]string
}

type codexStateTitleCacheEntry struct {
	path     string
	modTime  time.Time
	size     int64
	walStamp fileStamp
	titles   map[string]string
}

type fileStamp struct {
	modTime time.Time
	size    int64
}

// ReadCustomTitle returns the most recent native /rename title for a session.
// Claude records it as a custom-title transcript entry. Modern Codex stores it
// in state_*.sqlite (threads.name); older versions expose it through
// session_index.jsonl or thread_name_updated transcript events.
func ReadCustomTitle(sessionID string) string {
	if ReadSessionMeta(sessionID).Provider == ProviderCodex {
		if title := readCodexStateThreadName(sessionID); title != "" {
			return title
		}
		if title := readCodexThreadName(sessionID); title != "" {
			return title
		}
		return readCodexTranscriptTitle(sessionID)
	}

	return readClaudeCustomTitle(sessionID)
}

func readClaudeCustomTitle(sessionID string) string {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return ""
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return ""
	}

	// Check mtime cache — avoid re-reading if file hasn't changed
	customTitleCacheMu.Lock()
	if cached, ok := customTitleCache[sessionID]; ok && cached.modTime.Equal(info.ModTime()) {
		customTitleCacheMu.Unlock()
		return cached.title
	}
	customTitleCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	const tailSize = 64 * 1024
	offset := info.Size() - tailSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, info.Size()-offset)
	n, _ := f.ReadAt(buf, offset)
	if n == 0 {
		return ""
	}

	type customTitleEntry struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
	}

	var last string
	for _, line := range bytes.Split(buf[:n], []byte("\n")) {
		if !bytes.Contains(line, []byte(`"custom-title"`)) {
			continue
		}
		var e customTitleEntry
		if json.Unmarshal(line, &e) == nil && e.Type == "custom-title" && e.CustomTitle != "" {
			last = e.CustomTitle
		}
	}

	customTitleCacheMu.Lock()
	customTitleCache[sessionID] = customTitleCacheEntry{title: last, modTime: info.ModTime()}
	customTitleCacheMu.Unlock()

	return last
}

func codexHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// readCodexStateThreadName reads names from Codex's current SQLite state.
// Spirit deliberately uses the sqlite3 command-line client instead of linking
// a driver into the TUI: this is an optional compatibility path, while the
// JSONL index and transcript readers remain usable on older/non-macOS setups.
func readCodexStateThreadName(sessionID string) string {
	path, info, ok := codexStateDatabase()
	if !ok {
		return ""
	}
	walStamp := codexStateWALStamp(path)

	codexStateTitleCacheMu.Lock()
	if codexStateTitleCache.path == path &&
		codexStateTitleCache.modTime.Equal(info.ModTime()) &&
		codexStateTitleCache.size == info.Size() &&
		codexStateTitleCache.walStamp == walStamp {
		title := codexStateTitleCache.titles[sessionID]
		codexStateTitleCacheMu.Unlock()
		return title
	}
	codexStateTitleCacheMu.Unlock()

	titles, ok := loadCodexStateTitles(path)
	if !ok {
		return ""
	}

	codexStateTitleCacheMu.Lock()
	codexStateTitleCache = codexStateTitleCacheEntry{
		path: path, modTime: info.ModTime(), size: info.Size(), walStamp: walStamp, titles: titles,
	}
	title := titles[sessionID]
	codexStateTitleCacheMu.Unlock()
	return title
}

func codexStateWALStamp(path string) fileStamp {
	info, err := os.Stat(path + "-wal")
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}
}

func codexStateDatabase() (string, os.FileInfo, bool) {
	matches, err := filepath.Glob(filepath.Join(codexHomeDir(), "state_*.sqlite"))
	if err != nil {
		return "", nil, false
	}
	var bestPath string
	var bestInfo os.FileInfo
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || (bestInfo != nil && !info.ModTime().After(bestInfo.ModTime())) {
			continue
		}
		bestPath, bestInfo = path, info
	}
	return bestPath, bestInfo, bestInfo != nil
}

func loadCodexStateTitles(path string) (map[string]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	query := `SELECT id, name FROM threads WHERE name IS NOT NULL AND trim(name) <> '';`
	out, err := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", path, query).Output()
	if err != nil {
		return nil, false
	}
	var rows []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, false
	}
	titles := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.ID != "" && strings.TrimSpace(row.Name) != "" {
			titles[row.ID] = strings.TrimSpace(row.Name)
		}
	}
	return titles, true
}

type codexSessionIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
}

// readCodexThreadName reads the latest name for each thread from Codex's
// append-only session index. The parsed index is shared across sessions and
// invalidated only when the index mtime changes.
func readCodexThreadName(sessionID string) string {
	path := filepath.Join(codexHomeDir(), "session_index.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	codexTitleIndexCacheMu.Lock()
	if codexTitleIndexCache.path == path && codexTitleIndexCache.modTime.Equal(info.ModTime()) {
		title := codexTitleIndexCache.titles[sessionID]
		codexTitleIndexCacheMu.Unlock()
		return title
	}
	codexTitleIndexCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	titles := make(map[string]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry codexSessionIndexEntry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil && entry.ID != "" {
			// The index is append-only; the newest row wins, including an
			// empty name if Codex ever adds a clear-name operation.
			titles[entry.ID] = strings.TrimSpace(entry.ThreadName)
		}
	}

	codexTitleIndexCacheMu.Lock()
	codexTitleIndexCache = codexTitleIndexCacheEntry{path: path, modTime: info.ModTime(), titles: titles}
	title := titles[sessionID]
	codexTitleIndexCacheMu.Unlock()
	return title
}

// readCodexTranscriptTitle keeps title discovery compatible with older Codex
// rollouts where the session index may be absent or not yet flushed.
func readCodexTranscriptTitle(sessionID string) string {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	type threadNameEvent struct {
		Type    string `json:"type"`
		Payload struct {
			Type       string `json:"type"`
			ThreadName string `json:"thread_name"`
		} `json:"payload"`
	}
	var last string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), maxTranscriptRecordSize)
	for scanner.Scan() {
		var event threadNameEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Type == "event_msg" &&
			event.Payload.Type == "thread_name_updated" && event.Payload.ThreadName != "" {
			last = event.Payload.ThreadName
		}
	}
	return strings.TrimSpace(last)
}
