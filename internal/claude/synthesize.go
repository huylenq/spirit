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
	Objective              string `json:"objective"`
	Status                 string `json:"status"`
	ProblemType            string `json:"problem_type"`             // bug, feature, refactoring, etc.
	SynthesizedTitle       string `json:"headline"`                 // AI-generated one-liner for list display
	AppliedSynthesizedTitle string `json:"applied_synthesized_title"` // last title sent via /rename (empty = never applied)
	InputWords             int    `json:"input_words"`              // word count of user messages fed to haiku
}

// ApplySynthesizedTitle marks the current SynthesizedTitle as applied in the cache.
// Called after successfully sending /rename to the tmux pane.
func ApplySynthesizedTitle(sessionID string) {
	cached := ReadCachedSummary(sessionID)
	if cached == nil || cached.SynthesizedTitle == "" {
		return
	}
	cached.AppliedSynthesizedTitle = cached.SynthesizedTitle
	data, _ := json.Marshal(cached)
	os.WriteFile(summaryFilePath(sessionID), data, 0o644)
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
	cached := ReadCachedSummary(sessionID)
	if cached != nil {
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

	// If there's an existing title, instruct the LLM to keep it unless the
	// conversation goal has meaningfully changed.
	prevTitle := ""
	if cached != nil {
		prevTitle = cached.SynthesizedTitle
	}

	titleInstruction := "<objective condensed to a single short phrase under 60 chars, no quotes, no period>"
	if prevTitle != "" {
		titleInstruction = fmt.Sprintf(
			"<keep %q if the conversation goal is the same; only change if the purpose shifted significantly>",
			prevTitle)
	}

	prompt := "Analyze these user messages from a coding session. Output ONLY valid JSON, no markdown fences:\n" +
		`{"objective":"<what the user is trying to build/fix/accomplish, 1-2 lines max>","status":"<what is currently happening or last completed, 1 line max>","problem_type":"<one of: bug, feature, refactoring, chore, docs, test, exploration, debug, performance>","headline":"` + titleInstruction + `"}` +
		"\n\n" + input

	out, err := LightweightJSON("Output ONLY valid JSON. No markdown, no explanation.", prompt)
	if err != nil {
		return nil, false, fmt.Errorf("lightweight infer: %w", err)
	}

	raw := extractJSONObject(out)
	var summary SessionSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		summary = SessionSummary{Objective: raw}
	}
	summary.InputWords = inputWords
	if cached != nil {
		summary.AppliedSynthesizedTitle = cached.AppliedSynthesizedTitle
	}
	go RecordSynthCall(SynthKindSummary, inputWords)

	// Fallback: derive title from objective if model omitted it
	if summary.SynthesizedTitle == "" && summary.Objective != "" {
		h := summary.Objective
		if len(h) > 60 {
			h = h[:57] + "..."
		}
		summary.SynthesizedTitle = h
	}

	// Write JSON to disk cache
	data, _ := json.Marshal(summary)
	summaryPath := summaryFilePath(sessionID)
	os.MkdirAll(filepath.Dir(summaryPath), 0o755)
	os.WriteFile(summaryPath, data, 0o644)

	return &summary, false, nil
}

// newLightweightClaude builds a `claude` CLI command for quick, isolated
// prompts. Flags strip everything claude can skip while still using OAuth
// auth: settings sources, hooks, MCP, plugins, slash commands, chrome,
// session persistence. cwd is set to a no-CLAUDE.md location so auto-
// discovery doesn't traverse the project tree.
func newLightweightClaude(systemPrompt, input string) *exec.Cmd {
	cmd := exec.Command("claude", "--model", "haiku", "-p",
		"--no-session-persistence",
		"--tools", "",
		"--effort", "low",
		"--setting-sources", "",
		"--disable-slash-commands",
		"--no-chrome",
		"--strict-mcp-config",
		"--system-prompt", systemPrompt,
		input)
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	cmd.Dir = os.TempDir()
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
