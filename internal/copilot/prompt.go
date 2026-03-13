package copilot

import (
	"fmt"
	"strings"
	"time"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

const (
	maxMemoryChars   = 4000
	maxEvents        = 50
	maxPreambleChars = 12000
)

// BuildContextPreamble constructs the dynamic context injected before each copilot prompt.
// It provides live situational awareness without requiring tool calls.
func BuildContextPreamble(
	longTermMemory string,
	recentEvents []CopilotEvent,
	sessions []claude.ClaudeSession,
	digest string,
) string {
	var b strings.Builder

	b.WriteString("<context>\n")

	// --- Current Time ---
	b.WriteString("## Current Time\n")
	b.WriteString(time.Now().Format("2006-01-02 15:04:05 MST"))
	b.WriteString("\n\n")

	// --- Live Sessions ---
	activeCount := 0
	for _, s := range sessions {
		if s.Status == claude.StatusAgentTurn {
			activeCount++
		}
	}
	fmt.Fprintf(&b, "## Live Sessions (%d active)\n", activeCount)
	if len(sessions) == 0 {
		b.WriteString("(no sessions)\n")
	} else {
		for _, s := range sessions {
			b.WriteString(formatSession(s))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	// --- Workspace Digest ---
	b.WriteString("## Workspace Digest\n")
	if digest == "" {
		b.WriteString("(no digest available)\n")
	} else {
		b.WriteString(digest)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// --- Memory ---
	memorySection := truncateMemory(longTermMemory)
	b.WriteString("## Your Memory (MEMORY.md)\n")
	if memorySection == "" {
		b.WriteString("(empty)\n")
	} else {
		b.WriteString(memorySection)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// --- Recent Activity ---
	events := recentEvents
	if len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
	}
	fmt.Fprintf(&b, "## Recent Activity (last %d events)\n", len(events))
	if len(events) == 0 {
		b.WriteString("(no recent activity)\n")
	} else {
		for _, e := range events {
			b.WriteString(formatEvent(e))
			b.WriteString("\n")
		}
	}

	b.WriteString("</context>")

	result := b.String()

	// Enforce total cap: truncate events first, then memory.
	if len(result) > maxPreambleChars {
		result = enforceCapWithTruncation(longTermMemory, events, sessions, digest)
	}

	return result
}

func formatSession(s claude.ClaudeSession) string {
	icon := "\u26AA" // white circle for user-turn (idle)
	if s.Status == claude.StatusAgentTurn {
		icon = "\U0001F7E2" // green circle for agent-turn (working)
	}

	name := s.DisplayName()
	if name == "" {
		name = "(New session)"
	}

	var flags []string
	if s.HasOverlap {
		flags = append(flags, "[overlap]")
	}
	if s.CompactCount > 0 {
		flags = append(flags, fmt.Sprintf("[compact\u00D7%d]", s.CompactCount))
	}
	if s.IsWaiting {
		flags = append(flags, "[waiting]")
	}

	flagStr := ""
	if len(flags) > 0 {
		flagStr = " " + strings.Join(flags, " ")
	}

	branch := s.GitBranch
	if branch == "" {
		branch = "-"
	}

	return fmt.Sprintf("%s | %s | %s | %s%s", icon, s.Project, name, branch, flagStr)
}

func formatEvent(e CopilotEvent) string {
	ts := e.Time.Format("15:04:05")
	project := e.Project
	if project == "" {
		project = "system"
	}
	return fmt.Sprintf("%s [%s] %s: %s", ts, string(e.Type), project, e.Detail)
}

func truncateMemory(memory string) string {
	if len(memory) <= maxMemoryChars {
		return memory
	}
	return memory[:maxMemoryChars] + "...[truncated]"
}

// enforceCapWithTruncation rebuilds the preamble, progressively trimming
// events then memory until the total fits within maxPreambleChars.
func enforceCapWithTruncation(
	longTermMemory string,
	events []CopilotEvent,
	sessions []claude.ClaudeSession,
	digest string,
) string {
	// Strategy: halve events, then halve memory, repeat until it fits.
	evts := events
	mem := longTermMemory

	for range 10 { // bounded iterations
		// Try trimming events first
		if len(evts) > 5 {
			evts = evts[len(evts)/2:]
		} else if len(mem) > 500 {
			// Trim memory
			mem = mem[:len(mem)/2]
		} else {
			// Both are small; just hard-truncate the result
			break
		}

		result := BuildContextPreambleRaw(mem, evts, sessions, digest)
		if len(result) <= maxPreambleChars {
			return result
		}
	}

	// Final hard truncation as last resort
	result := BuildContextPreambleRaw(mem, evts, sessions, digest)
	if len(result) > maxPreambleChars {
		return result[:maxPreambleChars-len("...[truncated]</context>")] + "...[truncated]\n</context>"
	}
	return result
}

// BuildContextPreambleRaw is the non-recursive inner builder (no cap enforcement).
func BuildContextPreambleRaw(
	longTermMemory string,
	recentEvents []CopilotEvent,
	sessions []claude.ClaudeSession,
	digest string,
) string {
	var b strings.Builder

	b.WriteString("<context>\n")

	b.WriteString("## Current Time\n")
	b.WriteString(time.Now().Format("2006-01-02 15:04:05 MST"))
	b.WriteString("\n\n")

	activeCount := 0
	for _, s := range sessions {
		if s.Status == claude.StatusAgentTurn {
			activeCount++
		}
	}
	fmt.Fprintf(&b, "## Live Sessions (%d active)\n", activeCount)
	if len(sessions) == 0 {
		b.WriteString("(no sessions)\n")
	} else {
		for _, s := range sessions {
			b.WriteString(formatSession(s))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	b.WriteString("## Workspace Digest\n")
	if digest == "" {
		b.WriteString("(no digest available)\n")
	} else {
		b.WriteString(digest)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	memorySection := truncateMemory(longTermMemory)
	b.WriteString("## Your Memory (MEMORY.md)\n")
	if memorySection == "" {
		b.WriteString("(empty)\n")
	} else {
		b.WriteString(memorySection)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## Recent Activity (last %d events)\n", len(recentEvents))
	if len(recentEvents) == 0 {
		b.WriteString("(no recent activity)\n")
	} else {
		for _, e := range recentEvents {
			b.WriteString(formatEvent(e))
			b.WriteString("\n")
		}
	}

	b.WriteString("</context>")
	return b.String()
}
