package claude

import (
	"encoding/json"
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

// ReadRawTranscript reads the JSONL transcript for a session and returns it
// as a pretty-printed JSON array string (for the raw transcript viewer).
func ReadRawTranscript(sessionID string) (string, error) {
	path, err := findTranscriptPath(sessionID)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entries []json.RawMessage
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Validate it's valid JSON before including
		var raw json.RawMessage
		if json.Unmarshal([]byte(line), &raw) == nil {
			entries = append(entries, raw)
		}
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
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
