package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WorkspaceDigest is a meta-summary across all session summaries.
type WorkspaceDigest struct {
	Summary      string    `json:"summary"`
	SessionCount int       `json:"sessionCount"`
	FileCount    int       `json:"fileCount"`
	GeneratedAt  time.Time `json:"generatedAt"`
}

func digestFilePath() string {
	return filepath.Join(statusDir(), "digest.json")
}

// ReadCachedDigest returns the last generated workspace digest, or nil.
func ReadCachedDigest() *WorkspaceDigest {
	data, err := os.ReadFile(digestFilePath())
	if err != nil || len(data) == 0 {
		return nil
	}
	var d WorkspaceDigest
	if json.Unmarshal(data, &d) != nil {
		return nil
	}
	return &d
}

// GenerateDigest creates a workspace-level digest from all sessions' summaries.
func GenerateDigest(sessions []ClaudeSession) (*WorkspaceDigest, error) {
	var headlines []string
	fileSet := make(map[string]bool)
	summarized := 0

	for _, s := range sessions {
		if s.SessionID == "" || s.IsPhantom {
			continue
		}
		if sum := ReadCachedSummary(s.SessionID); sum != nil && sum.Headline != "" {
			headlines = append(headlines, sum.Headline)
			summarized++
		}
		for f := range ReadDiffStats(s.SessionID) {
			fileSet[f] = true
		}
	}

	if len(headlines) == 0 {
		return nil, nil
	}

	input := "Sessions:\n" + strings.Join(headlines, "\n") +
		"\n\nSummarize what's happening across these coding sessions in 2-3 sentences. " +
		"Focus on the overall workspace activity and any common themes."

	cmd := newLightweightClaude("Output only a plain text summary, no JSON, no markdown.", input)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	inputWords := len(strings.Fields(input))
	go RecordSynthCall(SynthKindDigest, inputWords)

	digest := &WorkspaceDigest{
		Summary:      strings.TrimSpace(string(out)),
		SessionCount: summarized,
		FileCount:    len(fileSet),
		GeneratedAt:  time.Now(),
	}
	writeDigest(digest)
	return digest, nil
}

func writeDigest(d *WorkspaceDigest) {
	data, _ := json.Marshal(d)
	os.MkdirAll(filepath.Dir(digestFilePath()), 0o755)
	os.WriteFile(digestFilePath(), data, 0o644) //nolint:errcheck
}
