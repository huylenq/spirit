package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionSummary holds the structured summary of a coding session.
type SessionSummary struct {
	Objective  string `json:"objective"`
	Status     string `json:"status"`
	Headline   string `json:"headline"`    // brief one-liner for list display
	InputWords int    `json:"input_words"` // word count of user messages fed to haiku
}

func summaryFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".summary")
}

// ReadCachedSummary returns a previously cached summary if it exists.
// The summary persists even when the transcript gets new messages —
// pressing 's' again regenerates it.
func ReadCachedSummary(sessionID string) *SessionSummary {
	summaryPath := summaryFilePath(sessionID)
	data, err := os.ReadFile(summaryPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	var s SessionSummary
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &s
}

// Summarize generates a structured summary of a session via claude --model sonnet.
// Results are cached to disk as JSON; returns cached summary if transcript hasn't changed.
// Summarize generates a structured summary of a session via claude --model haiku.
// Returns (summary, fromCache, error). fromCache is true when the cached summary is still fresh.
func Summarize(sessionID string) (*SessionSummary, bool, error) {
	// Return cached only if summary is still fresh (newer than transcript)
	if cached := ReadCachedSummary(sessionID); cached != nil {
		transcriptPath, _ := findTranscriptPath(sessionID)
		summaryPath := summaryFilePath(sessionID)
		tInfo, tErr := os.Stat(transcriptPath)
		sInfo, sErr := os.Stat(summaryPath)
		if tErr == nil && sErr == nil && !sInfo.ModTime().Before(tInfo.ModTime()) {
			return cached, true, nil
		}
	}

	messages, err := ReadUserMessages(sessionID)
	if err != nil {
		return nil, false, fmt.Errorf("reading messages: %w", err)
	}
	if len(messages) == 0 {
		return nil, false, fmt.Errorf("no messages found")
	}

	input := strings.Join(messages, "\n")
	inputWords := len(strings.Fields(input))

	prompt := "Analyze these user messages from a coding session. Output ONLY valid JSON, no markdown fences:\n" +
		`{"objective":"<what the user is trying to build/fix/accomplish, 1-2 lines max>","status":"<what is currently happening or last completed, 1 line max>"}` +
		"\n\n" + input

	cmd := newLightweightClaude("Output ONLY valid JSON. No markdown, no explanation.", prompt)
	out, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("claude CLI: %w", err)
	}

	raw := strings.TrimSpace(string(out))
	var summary SessionSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		summary = SessionSummary{Objective: raw}
	}
	summary.InputWords = inputWords

	// Second pass (parallel): Haiku distills objective into headline + window name
	// Second pass: Haiku distills objective into a brief headline
	if summary.Objective != "" {
		headlinePrompt := "Condense this into a single short phrase (under 60 chars), no quotes, no period:\n" + summary.Objective
		headlineCmd := newLightweightClaude("Output ONLY a short phrase. No quotes, no explanation.", headlinePrompt)
		if headlineOut, err := headlineCmd.Output(); err == nil {
			summary.Headline = strings.TrimSpace(string(headlineOut))
		}
	}

	// Write JSON to disk cache
	data, _ := json.Marshal(summary)
	summaryPath := summaryFilePath(sessionID)
	os.MkdirAll(filepath.Dir(summaryPath), 0o755)
	os.WriteFile(summaryPath, data, 0o644)

	return &summary, false, nil
}

// filterEnv returns env vars excluding any whose key starts with prefix.
// newLightweightClaude builds a claude CLI command for quick, isolated prompts.
func newLightweightClaude(systemPrompt, input string) *exec.Cmd {
	cmd := exec.Command("claude", "--model", "haiku", "-p",
		"--no-session-persistence", "--tools", "", "--effort", "low",
		"--setting-sources", "",
		"--system-prompt", systemPrompt,
		input)
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	return cmd
}

func filterEnv(env []string, prefix string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, prefix+"=") || strings.HasPrefix(e, prefix+"_") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}
