package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionSummary holds the structured synthesis of a coding session.
type SessionSummary struct {
	Objective  string `json:"objective"`
	Status     string `json:"status"`
	Headline   string `json:"headline"`    // brief one-liner for list display
	InputWords int    `json:"input_words"` // word count of user messages fed to haiku
}

func summaryFilePath(sessionID string) string {
	return filepath.Join(statusDir(), sessionID+".summary")
}

// ReadCachedSummary returns a previously cached synthesis if it exists.
// The synthesis persists even when the transcript gets new messages —
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

// SummaryCacheInfo returns debug info about the synthesis cache for a session.
func SummaryCacheInfo(sessionID string) (summaryMod, transcriptMod string, isFresh bool) {
	summaryPath := summaryFilePath(sessionID)
	transcriptPath, _ := findTranscriptPath(sessionID)
	sInfo, sErr := os.Stat(summaryPath)
	tInfo, tErr := os.Stat(transcriptPath)
	if sErr == nil {
		summaryMod = sInfo.ModTime().Format("15:04:05")
	}
	if tErr == nil {
		transcriptMod = tInfo.ModTime().Format("15:04:05")
	}
	if sErr == nil && tErr == nil {
		isFresh = !sInfo.ModTime().Before(tInfo.ModTime())
	}
	return
}

// Synthesize generates a structured synthesis of a session via claude --model haiku.
// Results are cached to disk as JSON; returns cached synthesis if transcript hasn't changed.
// Returns (summary, fromCache, error). fromCache is true when the cached synthesis is still fresh.
func Summarize(sessionID string) (*SessionSummary, bool, error) {
	// Return cached only if synthesis is still fresh (newer than transcript)
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
		`{"objective":"<what the user is trying to build/fix/accomplish, 1-2 lines max>","status":"<what is currently happening or last completed, 1 line max>","headline":"<objective condensed to a single short phrase under 60 chars, no quotes, no period>"}` +
		"\n\n" + input

	cmd := newLightweightClaude("Output ONLY valid JSON. No markdown, no explanation.", prompt)
	out, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("claude CLI: %w", err)
	}

	raw := strings.TrimSpace(string(out))
	// Strip markdown fences haiku occasionally adds despite being told not to
	if start := strings.Index(raw, "{"); start > 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			raw = raw[start : end+1]
		}
	}
	var summary SessionSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		summary = SessionSummary{Objective: raw}
	}
	summary.InputWords = inputWords
	// Fallback: derive headline from objective if model omitted it
	if summary.Headline == "" && summary.Objective != "" {
		h := summary.Objective
		if len(h) > 60 {
			h = h[:57] + "..."
		}
		summary.Headline = h
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
