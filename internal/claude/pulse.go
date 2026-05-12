package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Pulse is a meta-summary across all session summaries — a snapshot of what's
// happening across every Claude session spirit currently tracks. Named for the
// "current state of the whole organism in one beat" feel: it refreshes on
// every agent→user transition and stales out when nothing's been happening.
type Pulse struct {
	Summary      string    `json:"summary"`
	SessionCount int       `json:"sessionCount"`
	FileCount    int       `json:"fileCount"`
	GeneratedAt  time.Time `json:"generatedAt"`
}

func pulseFilePath() string {
	return filepath.Join(statusDir(), "pulse.json")
}

var (
	pulseReadMu    sync.Mutex
	pulseReadCache *Pulse
	pulseReadMtime time.Time
)

// ReadCachedPulse returns the last generated pulse, or nil. The result is
// memoized against the on-disk mtime so calling this on every render tick
// (Bubble Tea View()) only pays a stat syscall, not a read+unmarshal.
func ReadCachedPulse() *Pulse {
	path := pulseFilePath()
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	pulseReadMu.Lock()
	defer pulseReadMu.Unlock()
	if pulseReadCache != nil && pulseReadMtime.Equal(info.ModTime()) {
		return pulseReadCache
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	var p Pulse
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	pulseReadCache = &p
	pulseReadMtime = info.ModTime()
	return pulseReadCache
}

// GeneratePulse creates a pulse from all sessions' summaries.
func GeneratePulse(sessions []ClaudeSession) (*Pulse, error) {
	var headlines []string
	fileSet := make(map[string]bool)
	summarized := 0

	for _, s := range sessions {
		if s.SessionID == "" || s.IsPhantom {
			continue
		}
		if sum := ReadCachedSummary(s.SessionID); sum != nil && sum.SynthesizedTitle != "" {
			headlines = append(headlines, sum.SynthesizedTitle)
			summarized++
		}
		for f := range ReadDiffStats(s.SessionID) {
			fileSet[f] = true
		}
	}

	if len(headlines) == 0 {
		return nil, nil
	}

	input := "Parallel coding session headlines:\n\n" + strings.Join(headlines, "\n") +
		"\n\nWrite 1-2 sentences (under 40 words total) describing what's being worked on right now. " +
		"Rules:\n" +
		"- Use the specific names and topics from the headlines (bug names, feature names, files). Quote or reuse the exact nouns.\n" +
		"- No generic framing: avoid 'development work', 'core features', 'multiple areas', 'overall activity'.\n" +
		"- No hedging: avoid 'appears to', 'seems to', 'involves', 'reflects'.\n" +
		"- No meta-summary: avoid 'the common theme is', 'this represents'.\n" +
		"- Style: terse status line a colleague would read in the sidebar, not an executive summary."

	out, err := LightweightText("You write terse, concrete status updates. One short paragraph. No headers, bullets, or markdown.", input)
	if err != nil {
		return nil, err
	}

	inputWords := len(strings.Fields(input))
	go RecordSynthCall(SynthKindPulse, inputWords)

	pulse := &Pulse{
		Summary:      strings.TrimSpace(out),
		SessionCount: summarized,
		FileCount:    len(fileSet),
		GeneratedAt:  time.Now(),
	}
	writePulse(pulse)
	return pulse, nil
}

func writePulse(p *Pulse) {
	data, _ := json.Marshal(p)
	os.MkdirAll(filepath.Dir(pulseFilePath()), 0o755)
	os.WriteFile(pulseFilePath(), data, 0o644) //nolint:errcheck
}
