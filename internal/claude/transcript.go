package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// localCmdTagRe strips injected local-command XML blocks from user message content.
// When a user runs /clear (or other local commands) and immediately types a message,
// Claude Code merges them into one user turn, so we must strip the command blocks
// rather than reject the entire message.
var localCmdTagRe = regexp.MustCompile(`(?s)<(?:local-command-caveat|command-name|command-message|command-args|local-command-stdout)[^>]*>.*?</(?:local-command-caveat|command-name|command-message|command-args|local-command-stdout)>`)

// planMsgPrefix is the prefix Claude Code injects when a plan is approved via ExitPlanMode.
// The full message is "Implement the following plan:\n\n# Plan Title\n\n## Context..."
const planMsgPrefix = "Implement the following plan:"

// systemInjectedMsgs are messages injected by Claude Code internals (e.g. context
// clear after plan tool exit) that should not be treated as real user messages.
var systemInjectedMsgs = map[string]bool{
	"[Request interrupted by user for tool use]": true,
}

// --- Transcript path cache ---

var (
	transcriptPathCache   = make(map[string]string)
	transcriptPathCacheMu sync.Mutex
)

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

// TranscriptPath returns the filesystem path to the JSONL transcript for a session ID.
func TranscriptPath(sessionID string) (string, error) {
	return findTranscriptPath(sessionID)
}

// --- Shared JSONL types ---

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

type toolUseBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// TranscriptEntry represents one parsed JSONL line from a transcript.
type TranscriptEntry struct {
	Index       int    `json:"index"`       // 0-based JSONL line number
	Type        string `json:"type"`        // "user", "assistant", "progress", "system", etc.
	ContentType string `json:"contentType"` // message.content block type: "text", "tool_use", "tool_result", "thinking", etc.
	Summary     string `json:"summary"`     // pre-computed one-line summary (plain text, styled at render time)
	Timestamp   string `json:"timestamp"`   // "HH:MM:SS" or "" if absent
	RawJSON     string `json:"rawJSON"`     // verbatim JSONL line
}

// ReadTranscriptEntries reads the JSONL transcript and returns structured entries
// in reverse order (newest first).
func ReadTranscriptEntries(sessionID string) ([]TranscriptEntry, error) {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Single pass: accumulate tool_use_id → name map as we go (assistant entries
	// always precede their tool_result responses in the transcript).
	toolNames := make(map[string]string)
	var entries []TranscriptEntry
	for i, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		typ := jsonString(raw["type"])
		if typ == "assistant" {
			extractToolNames(raw, toolNames)
		}
		entry := TranscriptEntry{
			Index:   i,
			RawJSON: line,
		}
		entry.Type = typ
		entry.ContentType = extractContentType(raw)
		entry.Timestamp = extractTimestamp(raw)
		entry.Summary = buildEntrySummary(typ, raw, toolNames)
		entries = append(entries, entry)
	}

	// Reverse: newest first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}

// extractToolNames collects tool_use block IDs → names from an assistant entry.
func extractToolNames(raw map[string]json.RawMessage, out map[string]string) {
	msgRaw, ok := raw["message"]
	if !ok {
		return
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(msgRaw, &msg) != nil {
		return
	}
	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		var block struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &block) == nil && block.Type == "tool_use" && block.ID != "" {
			out[block.ID] = block.Name
		}
	}
}

// extractContentType returns the type of the first content block in message.content.
// For string content, returns "text". For arrays, returns the first block's type.
func extractContentType(raw map[string]json.RawMessage) string {
	msgRaw, ok := raw["message"]
	if !ok {
		return ""
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(msgRaw, &msg) != nil || msg.Content == nil {
		return ""
	}
	// String content
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return "text"
	}
	// Array of blocks
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(msg.Content, &blocks) == nil && len(blocks) > 0 {
		return blocks[0].Type
	}
	return ""
}

// jsonString extracts a JSON string value, returning "" on failure.
func jsonString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return ""
}

// extractTimestamp tries to pull an ISO timestamp and format it as HH:MM:SS.
func extractTimestamp(raw map[string]json.RawMessage) string {
	for _, key := range []string{"timestamp", "createdAt", "time"} {
		if v, ok := raw[key]; ok {
			s := jsonString(v)
			// Try to find HH:MM:SS in the string (works for ISO 8601)
			if len(s) >= 19 && s[10] == 'T' {
				return s[11:19]
			}
		}
	}
	return ""
}

// buildEntrySummary creates a one-line plain-text summary for a transcript entry.
// toolNames maps tool_use_id → tool name for resolving tool_result entries.
func buildEntrySummary(typ string, raw map[string]json.RawMessage, toolNames map[string]string) string {
	switch typ {
	case "user":
		return buildUserSummary(raw, toolNames)
	case "assistant":
		return buildAssistantSummary(raw)
	case "progress":
		return "" // empty summary — type column is sufficient
	case "system":
		return ""
	case "custom-title":
		title := jsonString(raw["title"])
		if title != "" {
			return truncStr(title, 50)
		}
		return ""
	default:
		return ""
	}
}

func buildUserSummary(raw map[string]json.RawMessage, toolNames map[string]string) string {
	msgRaw, ok := raw["message"]
	if !ok {
		return ""
	}
	var msg messageContent
	if json.Unmarshal(msgRaw, &msg) != nil {
		return ""
	}

	// Content can be a string
	var textContent string
	if json.Unmarshal(msg.Content, &textContent) == nil {
		return truncStr(flattenText(textContent), 60)
	}

	// Or an array of content blocks
	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil || len(blocks) == 0 {
		return ""
	}

	// Check first block type
	var firstBlock struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		ToolUseID string `json:"tool_use_id"`
	}
	json.Unmarshal(blocks[0], &firstBlock) //nolint:errcheck

	switch firstBlock.Type {
	case "tool_result":
		name := toolNames[firstBlock.ToolUseID]
		if name == "" {
			name = "result"
		}
		suffix := ""
		if len(blocks) > 1 {
			suffix = fmt.Sprintf(" +%d", len(blocks)-1)
		}
		return name + suffix
	default:
		text := firstBlock.Text
		if text == "" {
			return ""
		}
		return truncStr(flattenText(text), 60)
	}
}

func buildAssistantSummary(raw map[string]json.RawMessage) string {
	msgRaw, ok := raw["message"]
	if !ok {
		return ""
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(msgRaw, &msg) != nil {
		return ""
	}

	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil || len(blocks) == 0 {
		return ""
	}

	// Pick the most interesting block: tool_use > text > thinking
	var bestType string
	var bestSummary string
	for _, b := range blocks {
		var block struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		json.Unmarshal(b, &block) //nolint:errcheck

		switch block.Type {
		case "tool_use":
			if bestType != "tool_use" {
				bestType = "tool_use"
				param := extractFirstParam(block.Input)
				if param != "" {
					bestSummary = block.Name + " " + truncStr(param, 40)
				} else {
					bestSummary = block.Name
				}
			}
		case "text":
			if bestType != "tool_use" && bestType != "text" {
				bestType = "text"
				bestSummary = truncStr(flattenText(block.Text), 60)
			}
		case "thinking":
			if bestType == "" {
				bestType = "thinking"
				bestSummary = "(thinking)"
			}
		}
	}

	if bestSummary == "" {
		return ""
	}

	extra := len(blocks) - 1
	if extra > 0 {
		bestSummary += fmt.Sprintf(" +%d", extra)
	}
	return bestSummary
}


// extractFirstParam pulls the first short string value from a JSON object (for tool_use summary).
func extractFirstParam(input json.RawMessage) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	// Prefer common param names
	for _, key := range []string{"command", "file_path", "path", "query", "pattern", "url", "content"} {
		if v, ok := m[key]; ok {
			s := jsonString(v)
			if s != "" {
				return s
			}
		}
	}
	// Fall back to first string value
	for _, v := range m {
		s := jsonString(v)
		if s != "" {
			return s
		}
	}
	return ""
}

func truncStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func flattenText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// --- Text extraction helpers ---

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
		return extractPlanTitle(stripped)
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
	return extractPlanTitle(result)
}

// extractPlanTitle detects Claude-generated plan implementation messages
// ("Implement the following plan:\n\n# Title...") and returns just "[plan] Title".
// Returns the original text unchanged for non-plan messages.
func extractPlanTitle(text string) string {
	after, ok := strings.CutPrefix(text, planMsgPrefix)
	if !ok {
		return text
	}
	after = strings.TrimSpace(after)
	title, _ := strings.CutPrefix(after, "# ")
	// Take only the first line of the title (stop at next newline)
	if idx := strings.IndexByte(title, '\n'); idx >= 0 {
		title = title[:idx]
	}
	// Strip redundant "Plan: " prefix if present
	title = strings.TrimPrefix(title, "Plan: ")
	if title == "" {
		return "[plan]"
	}
	return "[plan] " + title
}
