package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// localCmdTagRe strips injected local-command XML blocks from user message content.
// When a user runs /clear (or other local commands) and immediately types a message,
// Claude Code merges them into one user turn, so we must strip the command blocks
// rather than reject the entire message.
var localCmdTagRe = regexp.MustCompile(`(?s)<(?:local-command-caveat|command-name|command-message|command-args|local-command-stdout)[^>]*>.*?</(?:local-command-caveat|command-name|command-message|command-args|local-command-stdout)>`)

// systemInjectedMsgs are messages injected by Claude Code internals (e.g. context
// clear after plan tool exit) that should not be treated as real user messages.
var systemInjectedMsgs = map[string]bool{
	"[Request interrupted by user for tool use]": true,
}

type cachedTranscript struct {
	messages []string
	modTime  time.Time
	size     int64
}

var (
	transcriptCache   = map[string]cachedTranscript{}
	transcriptCacheMu sync.Mutex
)

// --- Transcript path cache ---

var (
	transcriptPathCache   = make(map[string]string)
	transcriptPathCacheMu sync.Mutex
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

// findTranscriptPath locates the JSONL transcript for a session ID.
// Results are cached permanently (verified on access).
func findTranscriptPath(sessionID string) (string, error) {
	transcriptPathCacheMu.Lock()
	cached, ok := transcriptPathCache[sessionID]
	transcriptPathCacheMu.Unlock()

	if ok {
		if _, err := os.Stat(cached); err == nil {
			return cached, nil
		}
		// Cached path no longer valid — re-discover
		transcriptPathCacheMu.Lock()
		delete(transcriptPathCache, sessionID)
		transcriptPathCacheMu.Unlock()
	}

	home, _ := os.UserHomeDir()
	projectsDir := filepath.Join(home, ".claude", "projects")
	filename := sessionID + ".jsonl"

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, e.Name(), filename)
		if _, err := os.Stat(candidate); err == nil {
			transcriptPathCacheMu.Lock()
			transcriptPathCache[sessionID] = candidate
			transcriptPathCacheMu.Unlock()
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

type transcriptLine struct {
	Type     string          `json:"type"`
	IsMeta   bool            `json:"isMeta"`
	Message  json.RawMessage `json:"message"`
	UserType string          `json:"userType"`
}

type messageContent struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// --- Diff stats ---

type FileDiffStat struct {
	Added   int
	Removed int
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

type toolUseBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
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

// --- Custom title cache (mtime-based, like last message cache) ---

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

// commitSuccessRe matches Claude's commit success output like "Commit 0141d9c created successfully."
var commitSuccessRe = regexp.MustCompile(`(?i)\bcommit\s+[0-9a-f]{7,}.*\bcreated\b`)

// --- Last action commit cache (mtime-based) ---

type lastActionCommitCacheEntry struct {
	isCommit bool
	modTime  time.Time
}

var (
	lastActionCommitCache   = make(map[string]lastActionCommitCacheEntry)
	lastActionCommitCacheMu sync.Mutex
)

// ReadLastActionCommit returns true if the session's last action was a git commit.
// Two heuristics (checked within the last 3 assistant messages each):
//   - Assistant text matches commit success pattern (e.g. "Commit abc1234 created successfully.")
//   - Bash tool_use input contains a git commit command
func ReadLastActionCommit(sessionID string) bool {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return false
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return false
	}

	lastActionCommitCacheMu.Lock()
	if cached, ok := lastActionCommitCache[sessionID]; ok && cached.modTime.Equal(info.ModTime()) {
		lastActionCommitCacheMu.Unlock()
		return cached.isCommit
	}
	lastActionCommitCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	const tailSize = 8 * 1024
	offset := info.Size() - tailSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, info.Size()-offset)
	n, _ := f.ReadAt(buf, offset)
	if n == 0 {
		return false
	}

	result := scanLastActionCommit(buf[:n])

	lastActionCommitCacheMu.Lock()
	lastActionCommitCache[sessionID] = lastActionCommitCacheEntry{isCommit: result, modTime: info.ModTime()}
	lastActionCommitCacheMu.Unlock()

	return result
}

// scanLastActionCommit walks lines backwards looking for commit evidence.
// Returns true if either:
//   - The last 3 assistant text messages contain a commit success pattern
//     (e.g. "Commit 0141d9c created successfully.")
//   - The last 3 Bash tool_use calls contain a git commit command
func scanLastActionCommit(data []byte) bool {
	lines := bytes.Split(data, []byte("\n"))
	toolUseCount := 0
	textCount := 0
	const maxChecks = 3

	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if len(line) == 0 || !bytes.Contains(line, []byte(`"assistant"`)) {
			continue
		}

		// Check assistant text for commit success pattern
		if textCount < maxChecks {
			text := extractAssistantText(line)
			if text != "" {
				textCount++
				if commitSuccessRe.MatchString(text) {
					return true
				}
			}
		}

		// Check Bash tool_use blocks for git commit command
		if toolUseCount < maxChecks && bytes.Contains(line, []byte(`"Bash"`)) {
			found, n := checkLineForGitCommit(line)
			if found {
				return true
			}
			toolUseCount += n
		}

		if toolUseCount >= maxChecks && textCount >= maxChecks {
			break
		}
	}

	return false
}

// checkLineForGitCommit parses an assistant line for Bash tool_use blocks
// containing a git commit command. Returns (found, bashBlockCount).
func checkLineForGitCommit(line []byte) (bool, int) {
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil || tl.Type != "assistant" {
		return false, 0
	}

	var msg messageContent
	if json.Unmarshal(tl.Message, &msg) != nil {
		return false, 0
	}

	var blocks []toolUseBlock
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return false, 0
	}

	count := 0
	for _, b := range blocks {
		if b.Type != "tool_use" || b.Name != "Bash" {
			continue
		}
		count++

		var inp struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(b.Input, &inp) != nil {
			continue
		}
		if isGitCommitCommand(inp.Command) {
			return true, count
		}
	}
	return false, count
}

// isGitCommitCommand checks if a shell command contains a git commit invocation.
// Matches "git commit" as a complete command or followed by a space/flag.
func isGitCommitCommand(cmd string) bool {
	for _, sep := range []string{"&&", ";"} {
		for _, part := range strings.Split(cmd, sep) {
			p := strings.TrimSpace(part)
			if p == "git commit" || strings.HasPrefix(p, "git commit ") {
				return true
			}
		}
	}
	// Single command (no separators)
	cmd = strings.TrimSpace(cmd)
	return cmd == "git commit" || strings.HasPrefix(cmd, "git commit ")
}

func extractAssistantText(line []byte) string {
	var tl transcriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return ""
	}
	if tl.Type != "assistant" {
		return ""
	}

	var msg messageContent
	if err := json.Unmarshal(tl.Message, &msg); err != nil {
		return ""
	}

	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return ""
	}

	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	if len(texts) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(texts, " "))
}

func extractUserText(line []byte) string {
	var tl transcriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return ""
	}
	if tl.Type != "user" || tl.IsMeta {
		return ""
	}

	var msg messageContent
	if err := json.Unmarshal(tl.Message, &msg); err != nil {
		return ""
	}

	// Content can be a string or an array of content blocks
	// Try string first
	var textContent string
	if err := json.Unmarshal(msg.Content, &textContent); err == nil {
		stripped := strings.TrimSpace(localCmdTagRe.ReplaceAllString(textContent, ""))
		if systemInjectedMsgs[stripped] {
			return ""
		}
		return stripped
	}

	// Try array of content blocks
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return ""
	}

	var texts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			stripped := strings.TrimSpace(localCmdTagRe.ReplaceAllString(b.Text, ""))
			if stripped != "" {
				texts = append(texts, stripped)
			}
		}
		// Skip tool_result, tool_reference, image blocks
	}
	if len(texts) == 0 {
		return ""
	}
	result := strings.TrimSpace(strings.Join(texts, " "))
	if systemInjectedMsgs[result] {
		return ""
	}
	return result
}
