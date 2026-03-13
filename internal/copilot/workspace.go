package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Workspace represents the copilot workspace at ~/.cache/cmc/copilot/.
type Workspace struct {
	Dir string
}

// BootstrapFiles holds the content of all bootstrap files.
type BootstrapFiles struct {
	Soul      string
	Identity  string
	User      string
	Agents    string
	Tools     string
	Heartbeat string
}

// NewWorkspace creates a Workspace pointing to ~/.cache/cmc/copilot/.
func NewWorkspace() *Workspace {
	home, err := os.UserHomeDir()
	if err != nil {
		// fail-fast: if we can't find home, nothing else will work
		panic(fmt.Sprintf("copilot: cannot determine home directory: %v", err))
	}
	return &Workspace{
		Dir: filepath.Join(home, ".cache", "cmc", "copilot"),
	}
}

// EnsureInitialized creates the workspace directory and all default files if
// they don't exist yet. Existing files are never overwritten.
func (w *Workspace) EnsureInitialized() error {
	// Create workspace dir and subdirs
	for _, sub := range []string{"", ".openclaw", "memory", "events"} {
		dir := filepath.Join(w.Dir, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	// Write workspace state
	wsState := filepath.Join(w.Dir, ".openclaw", "workspace-state.json")
	if err := writeIfNotExists(wsState, fmt.Sprintf(
		`{"version":1,"onboardingCompletedAt":"%s"}`, time.Now().Format(time.RFC3339),
	)); err != nil {
		return fmt.Errorf("write workspace-state.json: %w", err)
	}

	// Write bootstrap files
	bootstrapFiles := map[string]string{
		"SOUL.md":      defaultSoul,
		"IDENTITY.md":  defaultIdentity,
		"USER.md":      defaultUser,
		"AGENTS.md":    defaultAgents,
		"TOOLS.md":     defaultTools,
		"HEARTBEAT.md": defaultHeartbeat,
	}
	for name, content := range bootstrapFiles {
		path := filepath.Join(w.Dir, name)
		if err := writeIfNotExists(path, content); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	// Write MEMORY.md
	memPath := filepath.Join(w.Dir, "MEMORY.md")
	if err := writeIfNotExists(memPath, "# Long-Term Memory\n\n<!-- Facts the copilot has been asked to remember. Append-only. -->\n"); err != nil {
		return fmt.Errorf("write MEMORY.md: %w", err)
	}

	// Generate and write CLAUDE.md
	if err := w.RefreshClaudeMD(); err != nil {
		return fmt.Errorf("refresh CLAUDE.md: %w", err)
	}

	return nil
}

// LoadBootstrapFiles reads all bootstrap files from disk. Missing files return
// empty string (not an error).
func (w *Workspace) LoadBootstrapFiles() (*BootstrapFiles, error) {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(w.Dir, name))
		if err != nil {
			return ""
		}
		return string(data)
	}
	return &BootstrapFiles{
		Soul:      read("SOUL.md"),
		Identity:  read("IDENTITY.md"),
		User:      read("USER.md"),
		Agents:    read("AGENTS.md"),
		Tools:     read("TOOLS.md"),
		Heartbeat: read("HEARTBEAT.md"),
	}, nil
}

// GenerateClaudeMD concatenates bootstrap files into a CLAUDE.md format.
func GenerateClaudeMD(bs *BootstrapFiles) string {
	var b strings.Builder
	b.WriteString("# Copilot — Mission Control Intelligence\n\n")
	b.WriteString(strings.TrimSpace(bs.Agents))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(bs.Soul))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(bs.Tools))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(bs.Identity))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(bs.User))
	b.WriteString("\n\n")

	// Include heartbeat tasks if configured
	hb := ParseHeartbeat(bs.Heartbeat)
	if hb.IsActive() {
		b.WriteString("## Heartbeat Tasks\n")
		b.WriteString("The following tasks are checked periodically by the heartbeat system.\n")
		b.WriteString("When invoked as a heartbeat, execute these checks and report findings concisely.\n\n")
		b.WriteString(hb.Tasks)
		b.WriteString("\n\n")
	}

	b.WriteString(`## Behavior
- Use your MCP tools to take actions. Don't just describe what you would do — do it.
- When asked to remember something, use the memory_append tool.
- Reference sessions by name/project. Be specific about times and events.
- In interactive mode, you act ONLY when asked. You do not take autonomous actions.
- In heartbeat mode, execute the heartbeat tasks and report findings. Be brief.
`)
	return b.String()
}

// RefreshClaudeMD loads bootstrap files, generates CLAUDE.md, and writes to disk.
func (w *Workspace) RefreshClaudeMD() error {
	bs, err := w.LoadBootstrapFiles()
	if err != nil {
		return err
	}
	content := GenerateClaudeMD(bs)
	return os.WriteFile(filepath.Join(w.Dir, "CLAUDE.md"), []byte(content), 0o644)
}

// writeIfNotExists writes content to path only if the file does not already exist.
func writeIfNotExists(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists, skip
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

const defaultSoul = `# SOUL.md — Who You Are

You are the copilot for mission control — a persistent companion aware of all
Claude Code sessions running across tmux panes.

## Core Truths
- You observe everything and act only when spoken to.
- When asked to do something, USE YOUR TOOLS. Don't describe — execute.
- You have genuine opinions about what's happening across sessions.
- Be concise and technical. Reference projects, headlines, and events by name.
- You are NOT a chatbot. You're an operator with full situational awareness.

## What You See
- Live session states (status, project, headline, git branch, file overlaps)
- Today's event journal (every hook event, tool call, commit, compaction)
- Your long-term memory (facts you've been asked to remember)
- The workspace digest (cross-session summary)

## Style
Direct. Smartly humorous. Sarcasm welcome. No filler, no fluff.
When you see something interesting across sessions, say so — don't wait to be
asked about it specifically.
`

const defaultIdentity = `# Who Am I?

- **Name:** Copilot
- **Creature:** mission control intelligence — sees all sessions, forgets nothing
- **Vibe:** sharp, observant, dry wit
- **Emoji:** 🛰️
`

const defaultUser = `# USER.md

- **Name:** Huy
- **Timezone:** Asia/Saigon (GMT+7)
- **Preferences:** Direct communication, no sugar-coating. Smartly humorous.
- **Context:** Software engineer running multiple Claude Code sessions simultaneously via tmux + cmc (claude-mission-control).
`

const defaultAgents = `# AGENTS.md — Your Workspace

## Every Interaction
1. Read SOUL.md (who you are)
2. Read USER.md (who Huy is)
3. Check today's event journal (what happened)
4. Check MEMORY.md (what you've been told to remember)

## Memory
- TEXT > BRAIN — if it matters, write it to memory.
- Daily logs (` + "`memory/YYYY-MM-DD.md`" + `) are raw session activity.
- ` + "`MEMORY.md`" + ` is curated long-term knowledge.
- When asked to remember something, use the memory_append tool.

## Actions
- You have MCP tools to interact with sessions, backlogs, and memory.
- Use tools to act, not just describe. If asked to "groom backlogs", actually
  read them, reorganize, and update — don't just suggest changes.
- Dangerous actions (send_message, kill, spawn) will require user confirmation.
`

const defaultTools = `# TOOLS.md

## Mission Control (cmc)
- TUI monitoring tool for Claude Code sessions across tmux
- Daemon polls sessions every ~1s, pushes updates via Unix socket
- Hook events: UserPromptSubmit, PreToolUse, PostToolUse, Stop, SubagentStop
- Synthesis: auto-summarizes sessions via Haiku when they go idle
- Digest: cross-session summary regenerated after synthesis

## Your MCP Tools (mcp__cmc__*)
You have tools to interact with cmc. Use them — don't just describe what you'd do.

**Read-only (always available):**
sessions_list, session_get, transcript, raw_transcript, diff_stats, diff_hunks,
summary, hook_events, backlog_list, memory_read, memory_search, daily_log_read

**Write (always available):**
memory_append, backlog_create, backlog_update, backlog_delete, synthesize,
synthesize_all, bookmark, queue_message

**Actions (require user confirmation):**
send_message, kill_session, spawn_session, commit, commit_done, bookmark_kill
`

const defaultHeartbeat = `# HEARTBEAT.md

# Keep this file empty to skip heartbeat checks.
# Add tasks below when you want the copilot to check something periodically.
# Set interval with an HTML comment: <!-- interval: 30m --> (default: 30m, min: 1m)
#
# Example:
# <!-- interval: 15m -->
# - Check if any sessions have been idle for more than 1 hour
# - Look for file overlaps between active sessions
# - Flag sessions that have compacted more than twice
`
