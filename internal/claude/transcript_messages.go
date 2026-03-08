package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

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

	const headSize = 32 * 1024
	buf := make([]byte, headSize)
	n, _ := f.Read(buf)
	if n == 0 {
		return ""
	}

	var result string
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if line == "" {
			continue
		}
		if text := extractUserText([]byte(line)); text != "" {
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
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

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
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

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
)

// ReadCustomTitle returns the most recent /rename title set via Claude Code's custom-title entry.
// Reads only the tail of the file and caches by mtime.
func ReadCustomTitle(sessionID string) string {
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
